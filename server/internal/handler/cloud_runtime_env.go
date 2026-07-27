package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/cloudruntime"
)

// Workspace cloud runtime env (LLM proxy keys etc.) now lives in the standalone
// Fleet service, which owns its storage, AES sealing, validation, and the
// merge/prune/normalize semantics (see multica-cloud). These handlers are thin
// owner/admin-gated proxies to Fleet's /api/v1/workspace-env — the routes stay
// role-gated in the router; the workspace is identified by the URL {id}.

func (h *Handler) GetWorkspaceCloudRuntimeEnv(w http.ResponseWriter, r *http.Request) {
	h.proxyWorkspaceEnv(w, r, http.MethodGet, false)
}

func (h *Handler) PutWorkspaceCloudRuntimeEnv(w http.ResponseWriter, r *http.Request) {
	h.proxyWorkspaceEnv(w, r, http.MethodPut, true)
}

func (h *Handler) DeleteWorkspaceCloudRuntimeEnv(w http.ResponseWriter, r *http.Request) {
	h.proxyWorkspaceEnv(w, r, http.MethodDelete, false)
}

// proxyWorkspaceEnv forwards a workspace-env request to Fleet, scoping it to the
// URL {id} workspace (these admin routes identify the workspace by path, not by
// the X-Workspace-ID header proxyCloudRuntime reads).
func (h *Handler) proxyWorkspaceEnv(w http.ResponseWriter, r *http.Request, method string, withBody bool) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	workspaceID := uuidToString(wsUUID)
	if !h.requireCloudRuntimeWorkspaceEnabled(w, r, workspaceID) {
		return
	}
	if h.CloudRuntime == nil || !h.CloudRuntime.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "cloud runtime is not configured")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var body []byte
	if withBody {
		var bodyOK bool
		body, bodyOK = readCloudRuntimeJSONBody(w, r)
		if !bodyOK {
			return
		}
	}

	headers := http.Header{}
	headers.Set("X-Workspace-ID", workspaceID)
	if ws, err := h.Queries.GetWorkspace(r.Context(), wsUUID); err == nil {
		headers.Set("X-Workspace-Slug", ws.Slug)
	}

	resp, err := h.CloudRuntime.Do(r.Context(), cloudruntime.Request{
		Method:    method,
		Path:      "/api/v1/workspace-env",
		Body:      body,
		UserID:    userID,
		RequestID: cloudRuntimeRequestID(r),
		Op:        "config",
		Headers:   headers,
	})
	if err != nil {
		writeCloudRuntimeError(w, r, err)
		return
	}
	writeCloudRuntimeResponse(w, resp)
}
