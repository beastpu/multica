package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/cloudruntime"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/logger"
	appmiddleware "github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const maxCloudRuntimeRequestBodySize = 1 << 20

type cloudRuntimeProxyOptions struct {
	withUserID bool
	withQuery  bool
	withBody   bool
	// afterSuccess runs when Fleet returns 2xx, for server-side follow-up the
	// remote Fleet cannot do itself (e.g. agent_runtime cleanup keyed on the
	// server DB). Best-effort — it must not change the response the client sees.
	afterSuccess func(ctx context.Context, workspaceID string, body []byte)
}

func (h *Handler) GetCloudRuntimeAccess(w http.ResponseWriter, r *http.Request) {
	workspaceID := appmiddleware.ResolveWorkspaceIDFromRequest(r, h.Queries)
	enabled := workspaceID != "" &&
		h.cloudRuntimeWorkspaceEnabled(r, workspaceID) &&
		h.CloudRuntime != nil && h.CloudRuntime.Enabled()
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

func (h *Handler) cloudRuntimeWorkspaceEnabled(r *http.Request, workspaceID string) bool {
	return workspaceID != "" &&
		featureflags.CloudRuntimeEnabledForWorkspace(r.Context(), h.FeatureFlags, workspaceID)
}

func (h *Handler) requireCloudRuntimeWorkspaceEnabled(w http.ResponseWriter, r *http.Request, workspaceID string) bool {
	if h.cloudRuntimeWorkspaceEnabled(r, workspaceID) {
		return true
	}
	writeError(w, http.StatusForbidden, "cloud runtime is not enabled for this workspace")
	return false
}

func (h *Handler) GetCloudRuntimeService(w http.ResponseWriter, r *http.Request) {
	h.proxyCloudRuntime(w, r, http.MethodGet, "/api/v1/", cloudRuntimeProxyOptions{
		withUserID: true,
	})
}

func (h *Handler) GetCloudRuntimeHealth(w http.ResponseWriter, r *http.Request) {
	h.proxyCloudRuntime(w, r, http.MethodGet, "/healthz", cloudRuntimeProxyOptions{})
}

func (h *Handler) GetCloudRuntimeReady(w http.ResponseWriter, r *http.Request) {
	h.proxyCloudRuntime(w, r, http.MethodGet, "/readyz", cloudRuntimeProxyOptions{})
}

func (h *Handler) ListCloudRuntimeNodes(w http.ResponseWriter, r *http.Request) {
	h.proxyCloudRuntime(w, r, http.MethodGet, "/api/v1/nodes", cloudRuntimeProxyOptions{
		withUserID: true,
		withQuery:  true,
	})
}

func (h *Handler) CreateCloudRuntimeNode(w http.ResponseWriter, r *http.Request) {
	// Cloud now mints a node-scoped mcn_ PAT itself during /api/v1/nodes
	// and injects it into the EC2 instance via SSM bootstrap (see
	// multica-cloud docs/api/node-pat.md). We no longer forward the
	// caller's mul_ PAT — Fleet doesn't need it, and propagating a
	// long-lived user PAT into a remote machine widened the blast
	// radius of any node compromise. Hence the handler now mirrors
	// the other write endpoints: just the body, no PAT plumbing.
	h.proxyCloudRuntime(w, r, http.MethodPost, "/api/v1/nodes", cloudRuntimeProxyOptions{
		withUserID: true,
		withBody:   true,
	})
}

func (h *Handler) DeleteCloudRuntimeNode(w http.ResponseWriter, r *http.Request) {
	h.proxyCloudRuntime(w, r, http.MethodDelete, "/api/v1/nodes", cloudRuntimeProxyOptions{
		withUserID:   true,
		withBody:     true,
		afterSuccess: h.cascadeDeleteCloudRuntimeNode,
	})
}

// cascadeDeleteCloudRuntimeNode drops the offline cloud runtime rows the node
// registered (daemon_id = node name) once Fleet has torn it down, so they don't
// linger as UI orphans. Rows with an agent still bound are skipped by the
// query. Best-effort: a cleanup failure must not fail a delete the client has
// already seen succeed. (The remote Fleet cannot do this — agent_runtime is a
// multica-server table.)
func (h *Handler) cascadeDeleteCloudRuntimeNode(ctx context.Context, workspaceID string, body []byte) {
	var ref struct {
		ID         string `json:"id"`
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(body, &ref)
	nodeName := strings.TrimSpace(ref.InstanceID)
	if nodeName == "" {
		nodeName = strings.TrimSpace(ref.ID)
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if nodeName == "" || err != nil {
		return
	}
	if _, err := h.Queries.DeleteCloudRuntimesByNode(ctx, db.DeleteCloudRuntimesByNodeParams{
		WorkspaceID: wsUUID,
		DaemonID:    pgtype.Text{String: nodeName, Valid: true},
	}); err != nil {
		slog.Warn("cloud runtime: cascade delete failed", "error", err, "node", nodeName)
	}
}

func (h *Handler) StartCloudRuntimeNode(w http.ResponseWriter, r *http.Request) {
	h.proxyCloudRuntime(w, r, http.MethodPost, "/api/v1/nodes/start", cloudRuntimeProxyOptions{
		withUserID: true,
		withBody:   true,
	})
}

func (h *Handler) StopCloudRuntimeNode(w http.ResponseWriter, r *http.Request) {
	h.proxyCloudRuntime(w, r, http.MethodPost, "/api/v1/nodes/stop", cloudRuntimeProxyOptions{
		withUserID: true,
		withBody:   true,
	})
}

func (h *Handler) RebootCloudRuntimeNode(w http.ResponseWriter, r *http.Request) {
	h.proxyCloudRuntime(w, r, http.MethodPost, "/api/v1/nodes/reboot", cloudRuntimeProxyOptions{
		withUserID: true,
		withBody:   true,
	})
}

func (h *Handler) GetCloudRuntimeNodeStatus(w http.ResponseWriter, r *http.Request) {
	h.proxyCloudRuntime(w, r, http.MethodPost, "/api/v1/nodes/status", cloudRuntimeProxyOptions{
		withUserID: true,
		withBody:   true,
	})
}

func (h *Handler) ExecCloudRuntimeNode(w http.ResponseWriter, r *http.Request) {
	h.proxyCloudRuntime(w, r, http.MethodPost, "/api/v1/nodes/exec", cloudRuntimeProxyOptions{
		withUserID: true,
		withBody:   true,
	})
}

func (h *Handler) proxyCloudRuntime(w http.ResponseWriter, r *http.Request, method, path string, opts cloudRuntimeProxyOptions) {
	workspaceID := appmiddleware.ResolveWorkspaceIDFromRequest(r, h.Queries)
	if !h.requireCloudRuntimeWorkspaceEnabled(w, r, workspaceID) {
		return
	}
	if h.CloudRuntime == nil || !h.CloudRuntime.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "cloud runtime is not configured")
		return
	}

	var userID string
	if opts.withUserID {
		var ok bool
		userID, ok = requireUserID(w, r)
		if !ok {
			return
		}
	}

	var body []byte
	if opts.withBody {
		var ok bool
		body, ok = readCloudRuntimeJSONBody(w, r)
		if !ok {
			return
		}
	}

	var query url.Values
	if opts.withQuery {
		query = r.URL.Query()
	}

	// Forward the workspace scope so the standalone Fleet (which has no
	// server DB / middleware context) can tenant its work. The SaaS Fleet
	// ignores these; the self-hosted Fleet keys namespaces on them. Slug is
	// best-effort — Fleet falls back to the UUID for the namespace name.
	headers := http.Header{}
	headers.Set("X-Workspace-ID", workspaceID)
	if wsUUID, err := util.ParseUUID(workspaceID); err == nil {
		if ws, werr := h.Queries.GetWorkspace(r.Context(), wsUUID); werr == nil {
			headers.Set("X-Workspace-Slug", ws.Slug)
		}
	}

	resp, err := h.CloudRuntime.Do(r.Context(), cloudruntime.Request{
		Method:    method,
		Path:      path,
		Query:     query,
		Body:      body,
		UserID:    userID,
		RequestID: cloudRuntimeRequestID(r),
		Headers:   headers,
	})
	if err != nil {
		writeCloudRuntimeError(w, r, err)
		return
	}
	if opts.afterSuccess != nil && resp != nil && resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		opts.afterSuccess(r.Context(), workspaceID, body)
	}
	writeCloudRuntimeResponse(w, resp)
}

func readCloudRuntimeJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCloudRuntimeRequestBodySize)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "invalid request body")
		return nil, false
	}
	if len(bytes.TrimSpace(data)) == 0 {
		writeError(w, http.StatusBadRequest, "request body is required")
		return nil, false
	}
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return nil, false
	}
	return data, true
}

func cloudRuntimeRequestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	return chimw.GetReqID(r.Context())
}

func writeCloudRuntimeResponse(w http.ResponseWriter, resp *cloudruntime.Response) {
	if requestID := resp.Header.Get("X-Request-ID"); requestID != "" {
		w.Header().Set("X-Request-ID", requestID)
	}
	body := bytes.TrimSpace(resp.Body)
	if len(body) == 0 {
		w.WriteHeader(resp.StatusCode)
		return
	}
	if json.Valid(body) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return
	}
	writeJSON(w, resp.StatusCode, map[string]string{"error": string(body)})
}

func writeCloudRuntimeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, cloudruntime.ErrDisabled):
		writeError(w, http.StatusServiceUnavailable, "cloud runtime is not configured")
	case errors.Is(err, cloudruntime.ErrInvalidBaseURL):
		writeError(w, http.StatusServiceUnavailable, "cloud runtime is misconfigured")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "cloud runtime request timed out")
	default:
		slog.Warn("cloud runtime request failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusBadGateway, "cloud runtime request failed")
	}
}
