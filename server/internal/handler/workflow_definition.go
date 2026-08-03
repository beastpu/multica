package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type workflowResponse struct {
	ID                       string  `json:"id"`
	WorkspaceID              string  `json:"workspace_id"`
	Name                     string  `json:"name"`
	Description              string  `json:"description"`
	Status                   string  `json:"status"`
	LatestPublishedVersionID *string `json:"latest_published_version_id"`
	CreatedBy                string  `json:"created_by"`
	ArchivedAt               *string `json:"archived_at"`
	CreatedAt                string  `json:"created_at"`
	UpdatedAt                string  `json:"updated_at"`
	LatestPublishedVersion   int32   `json:"latest_published_version"`
	ActivityCount            int32   `json:"activity_count"`
	RunCount                 int64   `json:"run_count"`
	LastPublishedBy          *string `json:"last_published_by"`
	LastPublishedAt          *string `json:"last_published_at"`
	LatestChangeSummary      string  `json:"latest_change_summary"`
}

func workflowSummaryToResponse(
	row db.ListWorkflowSummariesRow,
) workflowResponse {
	return workflowResponse{
		ID: uuidToString(row.ID), WorkspaceID: uuidToString(row.WorkspaceID),
		Name: row.Name, Description: row.Description, Status: row.Status,
		LatestPublishedVersionID: uuidToPtr(row.LatestPublishedVersionID),
		CreatedBy:                uuidToString(row.CreatedBy),
		ArchivedAt:               timestampToPtr(row.ArchivedAt),
		CreatedAt:                timestampToString(row.CreatedAt),
		UpdatedAt:                timestampToString(row.UpdatedAt),
		LatestPublishedVersion:   row.LatestPublishedVersion,
		ActivityCount:            row.ActivityCount, RunCount: row.RunCount,
		LastPublishedBy:     uuidToPtr(row.LastPublishedBy),
		LastPublishedAt:     timestampToPtr(row.LastPublishedAt),
		LatestChangeSummary: row.LatestChangeSummary,
	}
}

type workflowWorkflowVersionResponse struct {
	ID                 string          `json:"id"`
	WorkspaceID        string          `json:"workspace_id"`
	WorkflowID         string          `json:"workflow_id"`
	Version            int32           `json:"version"`
	Revision           int64           `json:"revision"`
	Definition         json.RawMessage `json:"definition"`
	DefinitionChecksum string          `json:"definition_checksum"`
	ChangeSummary      string          `json:"change_summary"`
	CreatedBy          string          `json:"created_by"`
	PublishedBy        *string         `json:"published_by"`
	PublishedAt        *string         `json:"published_at"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
}

func workflowToResponse(row db.Workflow) workflowResponse {
	return workflowResponse{
		ID:                       uuidToString(row.ID),
		WorkspaceID:              uuidToString(row.WorkspaceID),
		Name:                     row.Name,
		Description:              row.Description,
		Status:                   row.Status,
		LatestPublishedVersionID: uuidToPtr(row.LatestPublishedVersionID),
		CreatedBy:                uuidToString(row.CreatedBy),
		ArchivedAt:               timestampToPtr(row.ArchivedAt),
		CreatedAt:                timestampToString(row.CreatedAt),
		UpdatedAt:                timestampToString(row.UpdatedAt),
	}
}

func workflowWorkflowVersionToResponse(row db.WorkflowVersion) workflowWorkflowVersionResponse {
	return workflowWorkflowVersionResponse{
		ID:                 uuidToString(row.ID),
		WorkspaceID:        uuidToString(row.WorkspaceID),
		WorkflowID:         uuidToString(row.WorkflowID),
		Version:            row.Version,
		Revision:           row.Revision,
		Definition:         json.RawMessage(row.Definition),
		DefinitionChecksum: row.DefinitionChecksum,
		ChangeSummary:      row.ChangeSummary,
		CreatedBy:          uuidToString(row.CreatedBy),
		PublishedBy:        uuidToPtr(row.PublishedBy),
		PublishedAt:        timestampToPtr(row.PublishedAt),
		CreatedAt:          timestampToString(row.CreatedAt),
		UpdatedAt:          timestampToString(row.UpdatedAt),
	}
}

func (h *Handler) workflowWriteEnabled(w http.ResponseWriter, r *http.Request) bool {
	if featureflags.WorkflowsActivityEngineEnabledForWorkspace(
		r.Context(),
		h.FeatureFlags,
		h.resolveWorkspaceID(r),
	) {
		return true
	}
	writeError(w, http.StatusNotFound, "workflows are not enabled")
	return false
}

func (h *Handler) workflowTemplateViewerIsAdmin(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID string,
) (bool, bool) {
	member, ok := h.requireWorkspaceMember(
		w, r, workspaceID, "workspace not found",
	)
	if !ok {
		return false, false
	}
	return roleAllowed(member.Role, "owner", "admin"), true
}

func (h *Handler) workflowTemplateWriteEnabled(
	w http.ResponseWriter,
	r *http.Request,
) bool {
	if !h.workflowWriteEnabled(w, r) {
		return false
	}
	_, ok := h.requireWorkspaceRole(
		w,
		r,
		h.resolveWorkspaceID(r),
		"workspace not found",
		"owner",
		"admin",
	)
	return ok
}

func workflowDefinitionBytes(raw json.RawMessage) ([]byte, string, error) {
	if len(raw) == 0 {
		return nil, "", errors.New("definition is required")
	}
	definition, err := workflowdomain.ParseDefinition(raw)
	if err != nil {
		return nil, "", err
	}
	definition, err = workflowdomain.NormalizeAuthoringDefinition(definition)
	if err != nil {
		return nil, "", err
	}
	normalized, err := json.Marshal(definition)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(normalized)
	return normalized, hex.EncodeToString(sum[:]), nil
}

func (h *Handler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	isAdmin, ok := h.workflowTemplateViewerIsAdmin(w, r, workspaceID)
	if !ok {
		return
	}
	var status pgtype.Text
	if value := strings.TrimSpace(r.URL.Query().Get("status")); value != "" {
		switch value {
		case "published", "archived":
			if !isAdmin && value != "published" {
				writeError(w, http.StatusForbidden, "insufficient permissions")
				return
			}
			status = pgtype.Text{String: value, Valid: true}
		default:
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
	} else if !isAdmin {
		status = pgtype.Text{String: "published", Valid: true}
	}
	rows, err := h.Queries.ListWorkflowSummaries(
		r.Context(),
		db.ListWorkflowSummariesParams{
			WorkspaceID: wsUUID,
			Status:      status,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow templates")
		return
	}
	items := make([]workflowResponse, len(rows))
	for i, row := range rows {
		items[i] = workflowSummaryToResponse(row)
		if !isAdmin {
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": items, "total": len(items)})
}

func (h *Handler) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	isAdmin, ok := h.workflowTemplateViewerIsAdmin(w, r, workspaceID)
	if !ok {
		return
	}
	templateID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workflow_id")
	if !ok {
		return
	}
	template, err := h.Queries.GetWorkflowInWorkspace(r.Context(), db.GetWorkflowInWorkspaceParams{
		ID: templateID, WorkspaceID: wsUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workflow template not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template")
		return
	}
	if !isAdmin && !template.LatestPublishedVersionID.Valid {
		writeError(w, http.StatusNotFound, "workflow template not found")
		return
	}
	versions, err := h.Queries.ListWorkflowVersions(r.Context(), db.ListWorkflowVersionsParams{
		WorkflowID: templateID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template versions")
		return
	}
	versionResponses := make([]workflowWorkflowVersionResponse, 0, len(versions))
	for _, version := range versions {
		versionResponses = append(
			versionResponses,
			workflowWorkflowVersionToResponse(version),
		)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workflow": workflowToResponse(template),
		"versions": versionResponses,
	})
}

type createWorkflowRequest struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Definition    json.RawMessage `json:"definition"`
	ChangeSummary string          `json:"change_summary"`
}

// workflowWorkflowNameTaken reports whether another live template in the
// workspace already answers to this name, writing the 409 when it does.
//
// Enforced here rather than only in the database because the message matters:
// a unique-violation surfacing as a 500 tells the user nothing about which
// field to change. The partial index behind it is the backstop for races.
//
// excludeID keeps a rename to its own current name a no-op instead of a
// self-collision.
func (h *Handler) workflowWorkflowNameTaken(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID pgtype.UUID,
	name string,
	excludeID pgtype.UUID,
) bool {
	count, err := h.Queries.CountLiveWorkflowsByName(
		r.Context(),
		db.CountLiveWorkflowsByNameParams{
			WorkspaceID: workspaceID, Name: name, ExcludeID: excludeID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check workflow template name")
		return true
	}
	if count > 0 {
		writeError(w, http.StatusConflict, "a workflow template with this name already exists")
		return true
	}
	return false
}

func (h *Handler) CreateWorkflow(w http.ResponseWriter, r *http.Request) {
	if !h.workflowTemplateWriteEnabled(w, r) {
		return
	}
	var req createWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	definition, checksum, err := workflowDefinitionBytes(req.Definition)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	if h.workflowWorkflowNameTaken(w, r, wsUUID, req.Name, pgtype.UUID{}) {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow template transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	template, err := qtx.CreateWorkflow(r.Context(), db.CreateWorkflowParams{
		WorkspaceID: wsUUID, Name: req.Name, Description: req.Description,
		CreatedBy: userUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create workflow template")
		return
	}
	version, err := qtx.CreateWorkflowVersion(r.Context(), db.CreateWorkflowVersionParams{
		WorkspaceID: wsUUID, WorkflowID: template.ID, Version: 1,
		Definition: definition, DefinitionChecksum: checksum,
		ChangeSummary: req.ChangeSummary, CreatedBy: userUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create workflow version")
		return
	}
	template, err = qtx.SetWorkflowPublishedVersion(r.Context(), db.SetWorkflowPublishedVersionParams{
		LatestPublishedVersionID: version.ID, ID: template.ID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to publish the first workflow version")
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
	writeJSON(w, http.StatusCreated, map[string]any{
		"workflow": workflowToResponse(template),
		"version":  workflowWorkflowVersionToResponse(version),
	})
}

func (h *Handler) ListWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	isAdmin, ok := h.workflowTemplateViewerIsAdmin(w, r, workspaceID)
	if !ok {
		return
	}
	templateID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workflow_id")
	if !ok {
		return
	}
	template, err := h.Queries.GetWorkflowInWorkspace(r.Context(), db.GetWorkflowInWorkspaceParams{
		ID: templateID, WorkspaceID: wsUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) ||
		(!isAdmin && !template.LatestPublishedVersionID.Valid) {
		writeError(w, http.StatusNotFound, "workflow template not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template")
		return
	}
	rows, err := h.Queries.ListWorkflowVersions(r.Context(), db.ListWorkflowVersionsParams{
		WorkflowID: templateID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow template versions")
		return
	}
	items := make([]workflowWorkflowVersionResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, workflowWorkflowVersionToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": items})
}

func (h *Handler) GetWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	isAdmin, ok := h.workflowTemplateViewerIsAdmin(w, r, workspaceID)
	if !ok {
		return
	}
	templateID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workflow_id")
	if !ok {
		return
	}
	template, err := h.Queries.GetWorkflowInWorkspace(
		r.Context(),
		db.GetWorkflowInWorkspaceParams{
			ID: templateID, WorkspaceID: wsUUID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) ||
		(!isAdmin && !template.LatestPublishedVersionID.Valid) {
		writeError(w, http.StatusNotFound, "workflow template not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template")
		return
	}
	versionNumber, err := strconv.ParseInt(chi.URLParam(r, "version"), 10, 32)
	if err != nil || versionNumber <= 0 {
		writeError(w, http.StatusBadRequest, "invalid workflow template version")
		return
	}
	version, err := h.Queries.GetWorkflowVersionByNumber(r.Context(), db.GetWorkflowVersionByNumberParams{
		WorkflowID: templateID, WorkspaceID: wsUUID, Version: int32(versionNumber),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workflow template version not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template version")
		return
	}
	writeJSON(w, http.StatusOK, workflowWorkflowVersionToResponse(version))
}

func (h *Handler) ensureWorkflowWritable(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID pgtype.UUID,
	templateID pgtype.UUID,
) bool {
	template, err := h.Queries.GetWorkflowInWorkspace(
		r.Context(),
		db.GetWorkflowInWorkspaceParams{
			ID: templateID, WorkspaceID: workspaceID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workflow template not found")
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template")
		return false
	}
	if template.Status == "archived" {
		writeError(w, http.StatusConflict, "workflow template is archived")
		return false
	}
	return true
}

type updateWorkflowMetadataRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (h *Handler) UpdateWorkflowMetadata(w http.ResponseWriter, r *http.Request) {
	if !h.workflowTemplateWriteEnabled(w, r) {
		return
	}
	var req updateWorkflowMetadataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != nil {
		value := strings.TrimSpace(*req.Name)
		if value == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		req.Name = &value
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	templateID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workflow_id")
	if !ok {
		return
	}
	if !h.ensureWorkflowWritable(w, r, wsUUID, templateID) {
		return
	}
	if req.Name != nil &&
		h.workflowWorkflowNameTaken(w, r, wsUUID, *req.Name, templateID) {
		return
	}
	template, err := h.Queries.UpdateWorkflowMetadata(r.Context(), db.UpdateWorkflowMetadataParams{
		Name: ptrToText(req.Name), Description: ptrToText(req.Description),
		ID: templateID, WorkspaceID: wsUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workflow template not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update workflow template")
		return
	}
	h.publishWorkflowRealtime(
		protocol.EventWorkflowUpdated, workspaceID, "system", "",
		map[string]any{"workflow_template_id": uuidToString(template.ID)},
	)
	writeJSON(w, http.StatusOK, workflowToResponse(template))
}

func (h *Handler) ValidateWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	if !h.workflowTemplateWriteEnabled(w, r) {
		return
	}
	var req saveWorkflowDefinitionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, _, err := workflowDefinitionBytes(req.Definition); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "errors": []string{err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "errors": []string{}})
}

type saveWorkflowDefinitionRequest struct {
	Definition    json.RawMessage `json:"definition"`
	ChangeSummary string          `json:"change_summary"`
	Revision      int64           `json:"revision"`
}

// SaveWorkflowDefinition is the one editing action: it validates the
// definition and, if it holds up, writes it as the next version and makes that
// version live.
//
// It replaces a four-step sequence — create draft, save, validate, publish —
// that made the author perform the storage model. Versions still exist and are
// still immutable once written; they are just allocated by saving rather than
// by a button.
//
// A definition that does not validate is rejected outright. Storing it would
// mean carrying a state whose only honest description is "saved but broken",
// and the author is sitting in the editor with the error and the fix in front
// of them — the one place where it costs nothing to correct.
func (h *Handler) SaveWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	if !h.workflowTemplateWriteEnabled(w, r) {
		return
	}
	var req saveWorkflowDefinitionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// workflowDefinitionBytes validates, so this rejects a broken definition
	// before anything touches the database: a refused save leaves no trace at
	// all rather than a version nobody can run.
	definition, checksum, err := workflowDefinitionBytes(req.Definition)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	templateID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workflow_id")
	if !ok {
		return
	}
	if !h.ensureWorkflowWritable(w, r, wsUUID, templateID) {
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

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow save transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	// Every save allocates its own version. A published version is never
	// written to, which is what keeps running instances — they pin a version
	// id — unaffected by any amount of editing.
	version, err := qtx.GetNextWorkflowVersion(
		r.Context(),
		db.GetNextWorkflowVersionParams{
			WorkflowID: templateID, WorkspaceID: wsUUID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate workflow version")
		return
	}
	// Two concurrent saves both read the same next number; the unique index on
	// (workflow_id, version) settles it and the loser is told to refresh
	// rather than silently overwriting the winner.
	target, err := qtx.CreateWorkflowVersion(r.Context(), db.CreateWorkflowVersionParams{
		WorkspaceID: wsUUID, WorkflowID: templateID, Version: version,
		Definition: definition, DefinitionChecksum: checksum,
		ChangeSummary: req.ChangeSummary, CreatedBy: userUUID,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "workflow was saved elsewhere; refresh and try again")
		return
	}
	if _, err := qtx.SetWorkflowPublishedVersion(r.Context(), db.SetWorkflowPublishedVersionParams{
		LatestPublishedVersionID: target.ID, ID: templateID, WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update workflow")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow save")
		return
	}

	h.publishWorkflowRealtime(
		protocol.EventWorkflowPublished, workspaceID, "member", userID,
		map[string]any{
			"workflow_template_id":         uuidToString(templateID),
			"workflow_template_version_id": uuidToString(target.ID),
		},
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"version": workflowWorkflowVersionToResponse(target),
	})
}

func (h *Handler) ArchiveWorkflow(w http.ResponseWriter, r *http.Request) {
	if !h.workflowTemplateWriteEnabled(w, r) {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	templateID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workflow_id")
	if !ok {
		return
	}
	template, err := h.Queries.ArchiveWorkflow(r.Context(), db.ArchiveWorkflowParams{
		ID: templateID, WorkspaceID: wsUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workflow template not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive workflow template")
		return
	}
	h.publishWorkflowRealtime(
		protocol.EventWorkflowUpdated, workspaceID, "system", "",
		map[string]any{"workflow_template_id": uuidToString(template.ID)},
	)
	writeJSON(w, http.StatusOK, workflowToResponse(template))
}
