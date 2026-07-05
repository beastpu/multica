package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Workspace capability roles (plan C-1,
// docs/agent-fix-p4-assessment-issue-design.md): GET/PUT/DELETE
// /api/workspaces/{id}/capabilities/{capability}. One row per (workspace,
// capability) names the agent that executes that capability's derived work.
// Member-level access — the P4 assessment panel is member-visible and the
// configuration is operational (which agent runs assessments), not a
// security boundary; the write guards below are about agent validity.

// knownWorkspaceCapabilities is the closed set of configurable capability
// keys. An unknown capability is a 404, not an open-ended namespace.
var knownWorkspaceCapabilities = map[string]bool{
	service.CapabilityP4Assessment: true,
}

type workspaceCapabilityResponse struct {
	Capability         string  `json:"capability"`
	AgentID            string  `json:"agent_id"`
	AgentName          string  `json:"agent_name"`
	ProjectID          *string `json:"project_id"`
	MaxConcurrentTasks int32   `json:"max_concurrent_tasks"`
	CreatedAt          string  `json:"created_at"`
}

func workspaceCapabilityToResponse(row db.GetWorkspaceAgentCapabilityRow) workspaceCapabilityResponse {
	return workspaceCapabilityResponse{
		Capability:         row.Capability,
		AgentID:            uuidToString(row.AgentID),
		AgentName:          row.AgentName,
		ProjectID:          uuidToPtr(row.ProjectID),
		MaxConcurrentTasks: row.MaxConcurrentTasks,
		CreatedAt:          timestampToString(row.CreatedAt),
	}
}

// requestWorkspaceCapability parses and validates the {id}/{capability} pair
// shared by all three methods.
func requestWorkspaceCapability(w http.ResponseWriter, r *http.Request) (pgtype.UUID, string, bool) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return pgtype.UUID{}, "", false
	}
	capability := chi.URLParam(r, "capability")
	if !knownWorkspaceCapabilities[capability] {
		writeError(w, http.StatusNotFound, "unknown capability")
		return pgtype.UUID{}, "", false
	}
	return wsUUID, capability, true
}

func (h *Handler) GetWorkspaceCapability(w http.ResponseWriter, r *http.Request) {
	wsUUID, capability, ok := requestWorkspaceCapability(w, r)
	if !ok {
		return
	}
	row, err := h.Queries.GetWorkspaceAgentCapability(r.Context(), db.GetWorkspaceAgentCapabilityParams{
		WorkspaceID: wsUUID,
		Capability:  capability,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "capability not configured")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load capability")
		return
	}
	writeJSON(w, http.StatusOK, workspaceCapabilityToResponse(row))
}

type putWorkspaceCapabilityRequest struct {
	AgentID            string  `json:"agent_id"`
	ProjectID          *string `json:"project_id"`
	MaxConcurrentTasks *int32  `json:"max_concurrent_tasks"`
}

// PutWorkspaceCapability upserts the capability role. Fail-closed
// validation: the agent must belong to this workspace, must not be archived,
// and must run on a daemon-served local runtime (assessment needs
// inner-network P4/Swarm access; a cloud runtime cannot provide it).
func (h *Handler) PutWorkspaceCapability(w http.ResponseWriter, r *http.Request) {
	wsUUID, capability, ok := requestWorkspaceCapability(w, r)
	if !ok {
		return
	}
	var req putWorkspaceCapabilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	agent, err := h.Queries.GetAgent(r.Context(), agentID)
	if err != nil || uuidToString(agent.WorkspaceID) != uuidToString(wsUUID) {
		writeError(w, http.StatusBadRequest, "agent not found in this workspace")
		return
	}
	if agent.ArchivedAt.Valid {
		writeError(w, http.StatusBadRequest, "agent is archived")
		return
	}
	if !agent.RuntimeID.Valid {
		writeError(w, http.StatusBadRequest, "agent has no runtime")
		return
	}
	runtime, err := h.Queries.GetAgentRuntime(r.Context(), agent.RuntimeID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "agent runtime not found")
		return
	}
	if runtime.RuntimeMode != "local" {
		writeError(w, http.StatusBadRequest, "capability agent must run on a daemon (local) runtime")
		return
	}

	var projectID pgtype.UUID
	if req.ProjectID != nil && *req.ProjectID != "" {
		projectID, ok = parseUUIDOrBadRequest(w, *req.ProjectID, "project_id")
		if !ok {
			return
		}
		project, err := h.Queries.GetProject(r.Context(), projectID)
		if err != nil || uuidToString(project.WorkspaceID) != uuidToString(wsUUID) {
			writeError(w, http.StatusBadRequest, "project not found in this workspace")
			return
		}
	}

	maxConcurrent := int32(1)
	if req.MaxConcurrentTasks != nil {
		if *req.MaxConcurrentTasks < 1 {
			writeError(w, http.StatusBadRequest, "max_concurrent_tasks must be at least 1")
			return
		}
		maxConcurrent = *req.MaxConcurrentTasks
	}

	if _, err := h.Queries.UpsertWorkspaceAgentCapability(r.Context(), db.UpsertWorkspaceAgentCapabilityParams{
		WorkspaceID:        wsUUID,
		Capability:         capability,
		AgentID:            agentID,
		ProjectID:          projectID,
		MaxConcurrentTasks: maxConcurrent,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save capability")
		return
	}
	row, err := h.Queries.GetWorkspaceAgentCapability(r.Context(), db.GetWorkspaceAgentCapabilityParams{
		WorkspaceID: wsUUID,
		Capability:  capability,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load capability")
		return
	}
	writeJSON(w, http.StatusOK, workspaceCapabilityToResponse(row))
}

// DeleteWorkspaceCapability turns the capability off — the P4 assessment
// Trigger is fail-closed, so deleting the row disables new native assessment
// runs for the workspace (already-queued tasks are unaffected).
func (h *Handler) DeleteWorkspaceCapability(w http.ResponseWriter, r *http.Request) {
	wsUUID, capability, ok := requestWorkspaceCapability(w, r)
	if !ok {
		return
	}
	if _, err := h.Queries.DeleteWorkspaceAgentCapability(r.Context(), db.DeleteWorkspaceAgentCapabilityParams{
		WorkspaceID: wsUUID,
		Capability:  capability,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete capability")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
