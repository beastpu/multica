package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"

	"github.com/jackc/pgx/v5/pgtype"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type builtinWorkflowResponse struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Definition  json.RawMessage `json:"definition"`
}

// ListBuiltinWorkflowTemplates returns the ready-to-use templates shipped
// with the server. The catalog is static, so it only requires workspace
// membership like the other template reads.
func (h *Handler) ListBuiltinWorkflowTemplates(w http.ResponseWriter, r *http.Request) {
	builtins := workflowdomain.BuiltinTemplates()
	templates := make([]builtinWorkflowResponse, 0, len(builtins))
	for _, builtin := range builtins {
		templates = append(templates, builtinWorkflowResponse{
			Key:         builtin.Key,
			Name:        builtin.Name,
			Description: builtin.Description,
			Definition:  builtin.Definition,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

type createWorkflowFromBuiltinRequest struct {
	Key string `json:"key"`
}

// CreateWorkflowFromBuiltin copies a builtin definition into a
// workspace-owned template and publishes it as version 1 in one transaction,
// so the template is immediately startable. Later edits go through the
// regular draft flow.
func (h *Handler) CreateWorkflowFromBuiltin(w http.ResponseWriter, r *http.Request) {
	if !h.workflowTemplateWriteEnabled(w, r) {
		return
	}
	var req createWorkflowFromBuiltinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	builtin, ok := workflowdomain.FindBuiltinTemplate(strings.TrimSpace(req.Key))
	if !ok {
		writeError(w, http.StatusNotFound, "builtin workflow template not found")
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user_id")
	if !ok {
		return
	}
	definition, checksum, err := workflowDefinitionBytes(builtin.Definition)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "builtin workflow template definition invalid")
		return
	}
	existing, err := h.Queries.ListWorkflows(r.Context(), db.ListWorkflowsParams{
		WorkspaceID: wsUUID, Status: pgtype.Text{},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow templates")
		return
	}
	for _, template := range existing {
		if template.Status != "archived" && template.Name == builtin.Name {
			writeError(w, http.StatusConflict, "a workflow template with this name already exists")
			return
		}
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow template transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	template, err := qtx.CreateWorkflow(r.Context(), db.CreateWorkflowParams{
		WorkspaceID: wsUUID, Name: builtin.Name, Description: builtin.Description,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create workflow template")
		return
	}
	draft, err := qtx.CreateWorkflowVersion(r.Context(), db.CreateWorkflowVersionParams{
		WorkspaceID: wsUUID, WorkflowID: template.ID, Version: 1, Status: "draft",
		Definition: definition, DefinitionChecksum: checksum,
		ChangeSummary: "内置模板初始版本", CreatedBy: userUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create workflow template version")
		return
	}
	published, err := qtx.PublishWorkflowVersion(r.Context(), db.PublishWorkflowVersionParams{
		PublishedBy: userUUID, ID: draft.ID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to publish workflow template version")
		return
	}
	template, err = qtx.SetWorkflowPublishedVersion(r.Context(), db.SetWorkflowPublishedVersionParams{
		LatestPublishedVersionID: published.ID, ID: template.ID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update workflow template")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow template")
		return
	}
	h.publishWorkflowRealtime(
		protocol.EventWorkflowCreated, workspaceID, "member", userID,
		map[string]any{"workflow_template_id": uuidToString(template.ID)},
	)
	h.publishWorkflowRealtime(
		protocol.EventWorkflowPublished, workspaceID, "member", userID,
		map[string]any{
			"workflow_template_id":         uuidToString(template.ID),
			"workflow_template_version_id": uuidToString(published.ID),
		},
	)
	writeJSON(w, http.StatusCreated, map[string]any{
		"workflow": workflowToResponse(template),
		"version":  workflowWorkflowVersionToResponse(published),
	})
}
