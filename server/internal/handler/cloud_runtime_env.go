package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/cloudruntime"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Workspace cloud runtime env — the per-workspace variables (LLM proxy keys
// etc.) kubefleet injects into every node pod of that workspace. Values are
// sealed with secretbox under MULTICA_CLOUD_RUNTIME_SECRET_KEY and stored in
// workspace_cloud_runtime_env — never in workspace.settings, which is shipped
// verbatim to daemons.
//
// The API is write-only for sensitive values: GET returns plaintext only for
// explicitly allowlisted non-sensitive config such as base URLs and model ids.
// Routes are admin-gated in the router.

var envNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

const (
	maxCloudRuntimeEnvVars      = 32
	maxCloudRuntimeEnvValueSize = 4096
)

type putCloudRuntimeEnvRequest struct {
	Env       map[string]string `json:"env"`
	RemoveEnv []string          `json:"remove_env"`
}

type cloudRuntimeEnvVarInfo struct {
	Name  string  `json:"name"`
	Last4 string  `json:"last4"`
	Value *string `json:"value,omitempty"`
}

// PutWorkspaceCloudRuntimeEnv merges the submitted variables into the
// workspace's cloud runtime env (upsert by name) and can remove selected
// names. Sensitive values are write-only, so the editor can never round-trip
// the existing set — a full replace would silently drop every secret not
// re-entered. Merge lets the admin update visible config without losing keys.
func (h *Handler) PutWorkspaceCloudRuntimeEnv(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	if !h.requireCloudRuntimeWorkspaceEnabled(w, r, uuidToString(wsUUID)) {
		return
	}
	if h.CloudRuntimeEnvBox == nil {
		writeError(w, http.StatusServiceUnavailable, "cloud runtime env is not configured on this server (MULTICA_CLOUD_RUNTIME_SECRET_KEY)")
		return
	}
	var req putCloudRuntimeEnvRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Env) == 0 && len(req.RemoveEnv) == 0 {
		writeError(w, http.StatusBadRequest, "env or remove_env must contain at least one variable (use DELETE to clear)")
		return
	}
	if len(req.Env) > maxCloudRuntimeEnvVars {
		writeError(w, http.StatusBadRequest, "too many env variables")
		return
	}
	for name, value := range req.Env {
		if !envNamePattern.MatchString(name) {
			writeError(w, http.StatusBadRequest, "invalid env variable name: "+name)
			return
		}
		if strings.TrimSpace(value) == "" || len(value) > maxCloudRuntimeEnvValueSize {
			writeError(w, http.StatusBadRequest, "invalid value for env variable: "+name)
			return
		}
	}
	for _, name := range req.RemoveEnv {
		if !envNamePattern.MatchString(name) {
			writeError(w, http.StatusBadRequest, "invalid env variable name: "+name)
			return
		}
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	// Merge into the existing set: load + decrypt what's there, then upsert
	// the submitted vars over it. A first-time config has no prior row.
	merged := map[string]string{}
	if row, err := h.Queries.GetWorkspaceCloudRuntimeEnv(r.Context(), wsUUID); err == nil {
		if existing, oerr := openCloudRuntimeEnv(h, row.EnvSealed); oerr == nil {
			merged = existing
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load existing env")
		return
	}
	for name, value := range req.Env {
		merged[name] = value
	}
	for _, name := range req.RemoveEnv {
		delete(merged, name)
	}
	pruneDeprecatedCloudRuntimeEnv(merged)
	if len(merged) > maxCloudRuntimeEnvVars {
		writeError(w, http.StatusBadRequest, "too many env variables")
		return
	}
	if len(merged) == 0 {
		if err := h.Queries.DeleteWorkspaceCloudRuntimeEnv(r.Context(), wsUUID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete env")
			return
		}
		if !h.syncWorkspaceCloudRuntimeEnv(w, r, userID, merged) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "env": []any{}})
		return
	}

	plaintext, err := json.Marshal(merged)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode env")
		return
	}
	sealed, err := h.CloudRuntimeEnvBox.Seal(plaintext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seal env")
		return
	}

	// updated_by is best-effort attribution; a non-UUID actor id (never the
	// case for admin-gated routes) simply stores NULL.
	updatedBy, _ := util.ParseUUID(userID)
	if err := h.Queries.UpsertWorkspaceCloudRuntimeEnv(r.Context(), db.UpsertWorkspaceCloudRuntimeEnvParams{
		WorkspaceID: wsUUID,
		EnvSealed:   sealed,
		UpdatedBy:   updatedBy,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save env")
		return
	}
	if !h.syncWorkspaceCloudRuntimeEnv(w, r, userID, merged) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": true,
		"env":        cloudRuntimeEnvInfos(merged),
		// New nodes read this directly on create; existing nodes need a pod
		// restart because Kubernetes envFrom is resolved at container start.
		"applies_to": "new_nodes_and_restarted_nodes",
	})
}

// GetWorkspaceCloudRuntimeEnv returns variable names and last-4 fingerprints.
// Plaintext is included only for non-sensitive allowlisted config values.
func (h *Handler) GetWorkspaceCloudRuntimeEnv(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	if !h.requireCloudRuntimeWorkspaceEnabled(w, r, uuidToString(wsUUID)) {
		return
	}
	if h.CloudRuntimeEnvBox == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "env": []any{}})
		return
	}
	row, err := h.Queries.GetWorkspaceCloudRuntimeEnv(r.Context(), wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, map[string]any{"configured": false, "env": []any{}})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load env")
		return
	}
	env, err := openCloudRuntimeEnv(h, row.EnvSealed)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open env")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": true,
		"env":        cloudRuntimeEnvInfos(env),
		"updated_at": row.UpdatedAt.Time,
	})
}

func (h *Handler) DeleteWorkspaceCloudRuntimeEnv(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	if !h.requireCloudRuntimeWorkspaceEnabled(w, r, uuidToString(wsUUID)) {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if err := h.Queries.DeleteWorkspaceCloudRuntimeEnv(r.Context(), wsUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete env")
		return
	}
	if !h.syncWorkspaceCloudRuntimeEnv(w, r, userID, nil) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) syncWorkspaceCloudRuntimeEnv(w http.ResponseWriter, r *http.Request, userID string, env map[string]string) bool {
	if h.CloudRuntime == nil || !h.CloudRuntime.Enabled() {
		return true
	}
	body, err := json.Marshal(map[string]any{"env": env})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode env sync request")
		return false
	}
	resp, err := h.CloudRuntime.Do(r.Context(), cloudruntime.Request{
		Method:    http.MethodPut,
		Path:      "/api/v1/workspace-env",
		Body:      body,
		UserID:    userID,
		RequestID: cloudRuntimeRequestID(r),
		Op:        "config",
	})
	if err != nil {
		writeCloudRuntimeError(w, r, err)
		return false
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		writeCloudRuntimeResponse(w, resp)
		return false
	}
	return true
}

func openCloudRuntimeEnv(h *Handler, sealed []byte) (map[string]string, error) {
	plaintext, err := h.CloudRuntimeEnvBox.Open(sealed)
	if err != nil {
		return nil, err
	}
	var env map[string]string
	if err := json.Unmarshal(plaintext, &env); err != nil {
		return nil, err
	}
	return env, nil
}

func cloudRuntimeEnvInfos(env map[string]string) []cloudRuntimeEnvVarInfo {
	infos := make([]cloudRuntimeEnvVarInfo, 0, len(env))
	for name, value := range env {
		last4 := value
		if len(last4) > 4 {
			last4 = last4[len(last4)-4:]
		}
		info := cloudRuntimeEnvVarInfo{Name: name, Last4: last4}
		if isCloudRuntimeEnvPlaintextAllowed(name) {
			v := value
			info.Value = &v
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos
}

func isCloudRuntimeEnvPlaintextAllowed(name string) bool {
	switch name {
	case "ANTHROPIC_BASE_URL", "CODEX_BASE_URL", "MULTICA_CODEX_MODEL", "MULTICA_CLAUDE_MODEL":
		return true
	default:
		return false
	}
}

func pruneDeprecatedCloudRuntimeEnv(env map[string]string) {
	delete(env, "CODEX_MODEL")
	delete(env, "ANTHROPIC_MODEL")
}
