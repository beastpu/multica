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

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Workspace cloud runtime env — the per-workspace variables (LLM proxy keys
// etc.) kubefleet injects into every node pod of that workspace. Values are
// sealed with secretbox under MULTICA_CLOUD_RUNTIME_SECRET_KEY and stored in
// workspace_cloud_runtime_env — never in workspace.settings, which is shipped
// verbatim to daemons.
//
// The API is write-only for values: GET returns variable names plus the last
// four characters, never the plaintext. Routes are admin-gated in the router.

var envNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

const (
	maxCloudRuntimeEnvVars      = 32
	maxCloudRuntimeEnvValueSize = 4096
)

type putCloudRuntimeEnvRequest struct {
	Env map[string]string `json:"env"`
}

type cloudRuntimeEnvVarInfo struct {
	Name  string `json:"name"`
	Last4 string `json:"last4"`
}

// PutWorkspaceCloudRuntimeEnv merges the submitted variables into the
// workspace's cloud runtime env (upsert by name). Values are write-only, so
// the editor can never round-trip the existing set — a full replace would
// silently drop every variable not re-entered. Merge lets the admin add or
// update one variable at a time; DELETE clears everything.
func (h *Handler) PutWorkspaceCloudRuntimeEnv(w http.ResponseWriter, r *http.Request) {
	if h.CloudRuntimeEnvBox == nil {
		writeError(w, http.StatusServiceUnavailable, "cloud runtime env is not configured on this server (MULTICA_CLOUD_RUNTIME_SECRET_KEY)")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req putCloudRuntimeEnvRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Env) == 0 {
		writeError(w, http.StatusBadRequest, "env must contain at least one variable (use DELETE to clear)")
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
	if len(merged) > maxCloudRuntimeEnvVars {
		writeError(w, http.StatusBadRequest, "too many env variables")
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

	userID, ok := requireUserID(w, r)
	if !ok {
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
	writeJSON(w, http.StatusOK, map[string]any{
		"env": cloudRuntimeEnvInfos(req.Env),
		// Existing nodes keep the env they booted with; the secret is synced
		// on the next node create. Surfaced so the UI can hint at a restart.
		"applies_to": "new_nodes",
	})
}

// GetWorkspaceCloudRuntimeEnv returns variable names and last-4 fingerprints
// only — the plaintext never leaves the server after PUT.
func (h *Handler) GetWorkspaceCloudRuntimeEnv(w http.ResponseWriter, r *http.Request) {
	if h.CloudRuntimeEnvBox == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "env": []any{}})
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
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
	if err := h.Queries.DeleteWorkspaceCloudRuntimeEnv(r.Context(), wsUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete env")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		infos = append(infos, cloudRuntimeEnvVarInfo{Name: name, Last4: last4})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos
}
