package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type startWorkflowRunRequest struct {
	Title             string                        `json:"title,omitempty"`
	WorkflowVersionID string                        `json:"workflow_version_id,omitempty"`
	Input             json.RawMessage               `json:"input,omitempty"`
	RoleAssignments   []workflowRoleAssignmentInput `json:"role_assignments,omitempty"`
	IdempotencyKey    string                        `json:"idempotency_key"`
}

// StartWorkflowRun starts a durable Run without manufacturing a host
// Issue. Nodes may still materialize Issues when their own issue_policy asks
// for one; the run itself is the top-level execution record.
func (h *Handler) StartWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	var req startWorkflowRunRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
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
	if existing, err := h.Queries.GetWorkflowInstanceByStartIdempotencyKey(
		r.Context(), db.GetWorkflowInstanceByStartIdempotencyKeyParams{
			WorkspaceID: wsUUID, IdempotencyKey: req.IdempotencyKey,
		},
	); err == nil {
		h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
		h.writeWorkflowInstanceDetail(w, r, existing, http.StatusOK)
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to check workflow request")
		return
	}

	templateID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workflow_id")
	if !ok {
		return
	}
	template, err := h.Queries.GetWorkflowInWorkspace(
		r.Context(), db.GetWorkflowInWorkspaceParams{ID: templateID, WorkspaceID: wsUUID},
	)
	if errors.Is(err, pgx.ErrNoRows) || template.Status != "published" {
		writeError(w, http.StatusNotFound, "published workflow template not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template")
		return
	}
	versionID := template.LatestPublishedVersionID
	if strings.TrimSpace(req.WorkflowVersionID) != "" {
		versionID, ok = parseUUIDOrBadRequest(w, req.WorkflowVersionID, "workflow_version_id")
		if !ok {
			return
		}
	}
	version, err := h.Queries.GetWorkflowVersionInWorkspace(
		r.Context(), db.GetWorkflowVersionInWorkspaceParams{ID: versionID, WorkspaceID: wsUUID},
	)
	if errors.Is(err, pgx.ErrNoRows) || version.WorkflowID != template.ID {
		writeError(w, http.StatusBadRequest, "workflow version not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template version")
		return
	}
	definition, err := workflowdomain.ParseDefinition(version.Definition)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	plan, err := workflowdomain.BuildGraphPlan(definition)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	req.RoleAssignments = defaultWorkflowOwnerAssignment(definition, req.RoleAssignments, userID)
	assignments, missingRoles, ok := h.validateWorkflowRoleAssignments(
		w, r, wsUUID, workspaceID, definition, req.RoleAssignments,
	)
	if !ok {
		return
	}
	input := req.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	var inputObject map[string]any
	if err := json.Unmarshal(input, &inputObject); err != nil || inputObject == nil {
		writeError(w, http.StatusBadRequest, "input must be a JSON object")
		return
	}
	normalizedInput, _ := json.Marshal(inputObject)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = template.Name
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow transaction")
		return
	}
	defer tx.Rollback(r.Context())
	instance, activeNodes, err := h.createWorkflowRuntime(
		r.Context(), h.Queries.WithTx(tx), workflowRuntimeStartParams{
			WorkspaceID: wsUUID, WorkflowID: template.ID, WorkflowVersionID: version.ID,
			HostIssueID: pgtype.UUID{}, Title: title, HostStatusMode: "independent",
			Input: normalizedInput, StartedByID: userUUID, IdempotencyKey: req.IdempotencyKey,
			Definition: definition, Plan: plan, Assignments: assignments, MissingRoles: missingRoles,
		},
	)
	if err != nil {
		_ = tx.Rollback(r.Context())
		if existing, lookupErr := h.Queries.GetWorkflowInstanceByStartIdempotencyKey(
			r.Context(), db.GetWorkflowInstanceByStartIdempotencyKeyParams{
				WorkspaceID: wsUUID, IdempotencyKey: req.IdempotencyKey,
			},
		); lookupErr == nil {
			h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
			h.writeWorkflowInstanceDetail(w, r, existing, http.StatusOK)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create workflow run")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow run")
		return
	}
	h.recordWorkflowStarted(r.Context(), "standalone", instance, activeNodes, len(missingRoles))
	h.publishWorkflowInstanceUpdated(
		workspaceID, "member", userID, uuidToString(instance.ID), firstWorkflowNodeID(activeNodes),
	)
	h.applyWorkflowNodeEnterActions(r.Context(), instance, definition, activeNodes)
	for _, activeNode := range activeNodes {
		h.publishWorkflowNodeUpdated(
			workspaceID, "member", userID, uuidToString(instance.ID), uuidToString(activeNode.ID),
		)
		h.materializeWorkflowNodeTasks(r.Context(), wsUUID, instance, activeNode)
	}
	h.notifyWorkflowNeedsSetup(r.Context(), instance, missingRoles)
	h.writeWorkflowInstanceDetail(w, r, instance, http.StatusCreated)
}
