package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	DraftVersion             int32   `json:"draft_version"`
	HasDraft                 bool    `json:"has_draft"`
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
		Name: row.Name, Description: row.Description,
		LatestPublishedVersionID: uuidToPtr(row.LatestPublishedVersionID),
		CreatedBy:                uuidToString(row.CreatedBy),
		ArchivedAt:               timestampToPtr(row.ArchivedAt),
		CreatedAt:                timestampToString(row.CreatedAt),
		UpdatedAt:                timestampToString(row.UpdatedAt),
		LatestPublishedVersion:   row.LatestPublishedVersion,
		DraftVersion:             row.DraftVersion, HasDraft: row.HasDraft,
		ActivityCount: row.ActivityCount, RunCount: row.RunCount,
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
	Status             string          `json:"status"`
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
		Status:             row.Status,
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
		case "draft", "published", "archived":
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
			items[i].DraftVersion = 0
			items[i].HasDraft = false
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
		if !isAdmin && version.Status != "published" {
			continue
		}
		versionResponses = append(
			versionResponses,
			workflowWorkflowVersionToResponse(version),
		)
		if version.Status == "draft" {
			w.Header().Set("ETag", workflowDraftETag(version.Revision))
		}
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
		WorkspaceID: wsUUID, WorkflowID: template.ID, Version: 1, Status: "draft",
		Definition: definition, DefinitionChecksum: checksum,
		ChangeSummary: req.ChangeSummary, CreatedBy: userUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create workflow template draft")
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
	w.Header().Set("ETag", workflowDraftETag(version.Revision))
	writeJSON(w, http.StatusCreated, map[string]any{
		"workflow": workflowToResponse(template),
		"draft":    workflowWorkflowVersionToResponse(version),
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
		if !isAdmin && row.Status != "published" {
			continue
		}
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
	if !isAdmin && version.Status != "published" {
		writeError(w, http.StatusNotFound, "workflow template version not found")
		return
	}
	if version.Status == "draft" {
		w.Header().Set("ETag", workflowDraftETag(version.Revision))
	}
	writeJSON(w, http.StatusOK, workflowWorkflowVersionToResponse(version))
}

type updateWorkflowDraftRequest struct {
	Definition    json.RawMessage `json:"definition"`
	ChangeSummary string          `json:"change_summary"`
	Revision      int64           `json:"revision,omitempty"`
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

func (h *Handler) CreateWorkflowDraft(w http.ResponseWriter, r *http.Request) {
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
	if existing, err := h.Queries.GetWorkflowDraft(r.Context(), db.GetWorkflowDraftParams{
		WorkflowID: templateID, WorkspaceID: wsUUID,
	}); err == nil {
		w.Header().Set("ETag", workflowDraftETag(existing.Revision))
		writeJSON(w, http.StatusOK, workflowWorkflowVersionToResponse(existing))
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template draft")
		return
	}
	published, err := h.Queries.GetLatestPublishedWorkflowVersion(r.Context(), db.GetLatestPublishedWorkflowVersionParams{
		WorkflowID: templateID, WorkspaceID: wsUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "workflow template has no published version")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load published workflow template")
		return
	}
	version, err := h.Queries.GetNextWorkflowVersion(r.Context(), db.GetNextWorkflowVersionParams{
		WorkflowID: templateID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate workflow template version")
		return
	}
	draft, err := h.Queries.CreateWorkflowVersion(r.Context(), db.CreateWorkflowVersionParams{
		WorkspaceID: wsUUID, WorkflowID: templateID, Version: version, Status: "draft",
		Definition: published.Definition, DefinitionChecksum: published.DefinitionChecksum,
		ChangeSummary: "", CreatedBy: userUUID,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "workflow template already has a draft")
		return
	}
	w.Header().Set("ETag", workflowDraftETag(draft.Revision))
	h.publishWorkflowRealtime(
		protocol.EventWorkflowUpdated, workspaceID, "member", userID,
		map[string]any{"workflow_template_id": uuidToString(templateID)},
	)
	writeJSON(w, http.StatusCreated, workflowWorkflowVersionToResponse(draft))
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

func (h *Handler) UpdateWorkflowDraft(w http.ResponseWriter, r *http.Request) {
	if !h.workflowTemplateWriteEnabled(w, r) {
		return
	}
	var req updateWorkflowDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Revision <= 0 {
		writeError(w, http.StatusBadRequest, "revision is required")
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
	templateID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workflow_id")
	if !ok {
		return
	}
	if !h.ensureWorkflowWritable(w, r, wsUUID, templateID) {
		return
	}
	draft, err := h.Queries.GetWorkflowDraft(r.Context(), db.GetWorkflowDraftParams{
		WorkflowID: templateID, WorkspaceID: wsUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workflow template draft not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template draft")
		return
	}
	updatedDraft, err := h.Queries.UpdateWorkflowDraft(r.Context(), db.UpdateWorkflowDraftParams{
		Definition: definition, DefinitionChecksum: checksum, ChangeSummary: req.ChangeSummary,
		ID: draft.ID, WorkspaceID: wsUUID, ExpectedRevision: req.Revision,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		latest, latestErr := h.Queries.GetWorkflowDraft(
			r.Context(),
			db.GetWorkflowDraftParams{
				WorkflowID: templateID, WorkspaceID: wsUUID,
			},
		)
		if latestErr != nil {
			writeError(w, http.StatusConflict, "workflow template draft changed; refresh and retry")
			return
		}
		w.Header().Set("ETag", workflowDraftETag(latest.Revision))
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":           "workflow template draft changed; refresh and retry",
			"latest_revision": latest.Revision,
			"draft":           workflowWorkflowVersionToResponse(latest),
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update workflow template draft")
		return
	}
	w.Header().Set("ETag", workflowDraftETag(updatedDraft.Revision))
	h.publishWorkflowRealtime(
		protocol.EventWorkflowUpdated, workspaceID, "system", "",
		map[string]any{"workflow_template_id": uuidToString(templateID)},
	)
	writeJSON(w, http.StatusOK, workflowWorkflowVersionToResponse(updatedDraft))
}

func (h *Handler) ValidateWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	if !h.workflowTemplateWriteEnabled(w, r) {
		return
	}
	var req updateWorkflowDraftRequest
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

func workflowDraftETag(revision int64) string {
	return fmt.Sprintf(`"workflow-draft-%d"`, revision)
}

type saveWorkflowDefinitionRequest struct {
	Definition    json.RawMessage `json:"definition"`
	ChangeSummary string          `json:"change_summary"`
	Revision      int64           `json:"revision"`
}

// SaveWorkflowDefinition is the one editing action: it writes the
// definition into a new version and makes that version live if it validates.
//
// It replaces a four-step sequence — create draft, save, validate, publish —
// that made the author perform the storage model. Versions still exist and are
// still immutable once published; they are just allocated by saving rather
// than by a button.
//
// A definition that does not validate is still stored, on a version that stays
// a draft. Refusing to save it would leave half-finished work nowhere to go,
// which is the one thing the old draft state was genuinely good for.
func (h *Handler) SaveWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	if !h.workflowTemplateWriteEnabled(w, r) {
		return
	}
	var req saveWorkflowDefinitionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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

	// Reuse the open draft when there is one, so a run of saves fills the same
	// version instead of allocating one per keystroke-batch. A published
	// version is never written to — that is what keeps running instances,
	// which pin a version id, unaffected by any amount of editing.
	target, err := qtx.LockWorkflowDraft(r.Context(), db.LockWorkflowDraftParams{
		WorkflowID: templateID, WorkspaceID: wsUUID,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		version, versionErr := qtx.GetNextWorkflowVersion(
			r.Context(),
			db.GetNextWorkflowVersionParams{
				WorkflowID: templateID, WorkspaceID: wsUUID,
			},
		)
		if versionErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to allocate workflow template version")
			return
		}
		target, err = qtx.CreateWorkflowVersion(r.Context(), db.CreateWorkflowVersionParams{
			WorkspaceID: wsUUID, WorkflowID: templateID, Version: version, Status: "draft",
			Definition: definition, DefinitionChecksum: checksum,
			ChangeSummary: req.ChangeSummary, CreatedBy: userUUID,
		})
		if err != nil {
			writeError(w, http.StatusConflict, "workflow template version changed; refresh and try again")
			return
		}
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to load workflow template draft")
		return
	default:
		expected := req.Revision
		if expected <= 0 {
			expected = target.Revision
		}
		target, err = qtx.UpdateWorkflowDraft(r.Context(), db.UpdateWorkflowDraftParams{
			Definition: definition, DefinitionChecksum: checksum,
			ChangeSummary: req.ChangeSummary,
			ID:            target.ID, WorkspaceID: wsUUID, ExpectedRevision: expected,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "workflow template draft changed; refresh and try again")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save workflow template draft")
			return
		}
	}

	published := false
	var validationErr string
	if _, parseErr := workflowdomain.ParseDefinition(definition); parseErr != nil {
		validationErr = parseErr.Error()
	} else {
		target, err = qtx.PublishWorkflowVersion(r.Context(), db.PublishWorkflowVersionParams{
			PublishedBy: userUUID, ID: target.ID, WorkspaceID: wsUUID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to publish workflow template version")
			return
		}
		if _, err := qtx.SetWorkflowPublishedVersion(r.Context(), db.SetWorkflowPublishedVersionParams{
			LatestPublishedVersionID: target.ID, ID: templateID, WorkspaceID: wsUUID,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update workflow template")
			return
		}
		published = true
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow template save")
		return
	}

	event := protocol.EventWorkflowUpdated
	if published {
		event = protocol.EventWorkflowPublished
	}
	h.publishWorkflowRealtime(
		event, workspaceID, "member", userID,
		map[string]any{
			"workflow_template_id":         uuidToString(templateID),
			"workflow_template_version_id": uuidToString(target.ID),
		},
	)
	w.Header().Set("ETag", workflowDraftETag(target.Revision))
	writeJSON(w, http.StatusOK, map[string]any{
		"version":          workflowWorkflowVersionToResponse(target),
		"published":        published,
		"validation_error": validationErr,
	})
}

func (h *Handler) PublishWorkflow(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusInternalServerError, "failed to start workflow publish transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	draft, err := qtx.LockWorkflowDraft(r.Context(), db.LockWorkflowDraftParams{
		WorkflowID: templateID, WorkspaceID: wsUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workflow template draft not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template draft")
		return
	}
	if _, err := workflowdomain.ParseDefinition(draft.Definition); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	published, err := qtx.PublishWorkflowVersion(r.Context(), db.PublishWorkflowVersionParams{
		PublishedBy: userUUID, ID: draft.ID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to publish workflow template version")
		return
	}
	template, err := qtx.SetWorkflowPublishedVersion(r.Context(), db.SetWorkflowPublishedVersionParams{
		LatestPublishedVersionID: published.ID, ID: templateID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update workflow template")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow template publish")
		return
	}
	h.publishWorkflowRealtime(
		protocol.EventWorkflowPublished, workspaceID, "member", userID,
		map[string]any{
			"workflow_template_id":         uuidToString(template.ID),
			"workflow_template_version_id": uuidToString(published.ID),
		},
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"workflow": workflowToResponse(template),
		"version":  workflowWorkflowVersionToResponse(published),
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
