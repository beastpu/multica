package handler

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/cloudruntime"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

type cloudRuntimeEnvInfoForTest struct {
	Name  string `json:"name"`
	Last4 string `json:"last4"`
	Value string `json:"value"`
}

func withWorkspaceIDParam(req *http.Request, workspaceID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func installCloudRuntimeEnvBox(t *testing.T) {
	t.Helper()
	allowCloudRuntimeForTest(t)
	key := make([]byte, secretbox.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatalf("secretbox.New: %v", err)
	}
	prev := testHandler.CloudRuntimeEnvBox
	testHandler.CloudRuntimeEnvBox = box
	t.Cleanup(func() { testHandler.CloudRuntimeEnvBox = prev })
}

func TestWorkspaceCloudRuntimeEnv_DeniedWorkspaceCannotWrite(t *testing.T) {
	installCloudRuntimeEnvBox(t)
	denyCloudRuntimeForTest(t)
	if _, err := testPool.Exec(context.Background(),
		`DELETE FROM workspace_cloud_runtime_env WHERE workspace_id = $1`, testWorkspaceID); err != nil {
		t.Fatalf("clean env row: %v", err)
	}

	w := httptest.NewRecorder()
	req := withWorkspaceIDParam(newRequest(http.MethodPut,
		"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env",
		map[string]any{"env": map[string]string{"OPENAI_API_KEY": "sk-denied"}}), testWorkspaceID)
	testHandler.PutWorkspaceCloudRuntimeEnv(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM workspace_cloud_runtime_env WHERE workspace_id = $1`, testWorkspaceID).Scan(&count); err != nil {
		t.Fatalf("count env rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("denied PUT persisted %d env rows", count)
	}
}

func TestWorkspaceCloudRuntimeEnv_PutGetDelete(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installCloudRuntimeEnvBox(t)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace_cloud_runtime_env WHERE workspace_id = $1`, testWorkspaceID)
	})

	// PUT stores sealed env, returns plaintext only for non-sensitive config,
	// and keeps proxy tokens masked.
	w := httptest.NewRecorder()
	req := withWorkspaceIDParam(newRequest(http.MethodPut, "/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", map[string]any{
		"env": map[string]string{
			"ANTHROPIC_API_KEY":    "sk-litellm-wxyz",
			"ANTHROPIC_BASE_URL":   "https://proxy.example.com/v1",
			"CODEX_BASE_URL":       "https://proxy.example.com/v1",
			"MULTICA_CLAUDE_MODEL": "gpt-5-codex",
			"MULTICA_CODEX_MODEL":  "gpt-5-codex",
			"OPENAI_API_KEY":       "sk-litellm-wxyz",
		},
	}), testWorkspaceID)
	testHandler.PutWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: status = %d body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-litellm-wxyz") {
		t.Fatalf("PUT response leaks plaintext: %s", w.Body.String())
	}

	// The sealed row must not contain plaintext.
	var sealed []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT env_sealed FROM workspace_cloud_runtime_env WHERE workspace_id = $1`, testWorkspaceID).Scan(&sealed); err != nil {
		t.Fatalf("read sealed row: %v", err)
	}
	if strings.Contains(string(sealed), "sk-litellm-wxyz") || strings.Contains(string(sealed), "https://proxy.example.com") {
		t.Fatal("env stored unencrypted")
	}

	// GET returns plaintext only for non-sensitive connection fields.
	w = httptest.NewRecorder()
	req = withWorkspaceIDParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", nil), testWorkspaceID)
	testHandler.GetWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET: status = %d body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Configured bool                         `json:"configured"`
		Env        []cloudRuntimeEnvInfoForTest `json:"env"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET decode: %v", err)
	}
	if !got.Configured || len(got.Env) != 6 {
		t.Fatalf("GET = %+v", got)
	}
	byName := map[string]cloudRuntimeEnvInfoForTest{}
	for _, env := range got.Env {
		byName[env.Name] = env
	}
	if byName["ANTHROPIC_BASE_URL"].Value != "https://proxy.example.com" {
		t.Fatalf("GET ANTHROPIC_BASE_URL = %+v", byName["ANTHROPIC_BASE_URL"])
	}
	if byName["CODEX_BASE_URL"].Value != "https://proxy.example.com/v1" {
		t.Fatalf("GET CODEX_BASE_URL = %+v", byName["CODEX_BASE_URL"])
	}
	if byName["MULTICA_CLAUDE_MODEL"].Value != "gpt-5-codex" {
		t.Fatalf("GET MULTICA_CLAUDE_MODEL = %+v", byName["MULTICA_CLAUDE_MODEL"])
	}
	if byName["MULTICA_CODEX_MODEL"].Value != "gpt-5-codex" {
		t.Fatalf("GET MULTICA_CODEX_MODEL = %+v", byName["MULTICA_CODEX_MODEL"])
	}
	if byName["OPENAI_API_KEY"].Last4 != "wxyz" || byName["OPENAI_API_KEY"].Value != "" {
		t.Fatalf("GET OPENAI_API_KEY = %+v", byName["OPENAI_API_KEY"])
	}
	if byName["ANTHROPIC_API_KEY"].Last4 != "wxyz" || byName["ANTHROPIC_API_KEY"].Value != "" {
		t.Fatalf("GET ANTHROPIC_API_KEY = %+v", byName["ANTHROPIC_API_KEY"])
	}
	if strings.Contains(w.Body.String(), "sk-litellm") {
		t.Fatalf("GET leaks plaintext: %s", w.Body.String())
	}

	// DELETE clears.
	w = httptest.NewRecorder()
	req = withWorkspaceIDParam(newRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", nil), testWorkspaceID)
	testHandler.DeleteWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: status = %d", w.Code)
	}
	w = httptest.NewRecorder()
	req = withWorkspaceIDParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", nil), testWorkspaceID)
	testHandler.GetWorkspaceCloudRuntimeEnv(w, req)
	if !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatalf("GET after DELETE = %s", w.Body.String())
	}
}

func TestWorkspaceCloudRuntimeEnv_SyncsFleetSecretAfterSaveAndClear(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installCloudRuntimeEnvBox(t)
	proxy := &fakeCloudRuntimeProxy{
		enabled: true,
		resp: &cloudruntime.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       []byte(`{"status":"synced"}`),
		},
	}
	useCloudRuntimeProxy(t, proxy)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace_cloud_runtime_env WHERE workspace_id = $1`, testWorkspaceID)
	})

	w := httptest.NewRecorder()
	req := withWorkspaceIDParam(newRequest(http.MethodPut,
		"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", map[string]any{
			"env": map[string]string{
				"CODEX_BASE_URL": "https://proxy.example/v1",
				"OPENAI_API_KEY": "sk-secret",
			},
		}), testWorkspaceID)
	testHandler.PutWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: status %d body %s", w.Code, w.Body.String())
	}
	if len(proxy.calls) != 1 || proxy.calls[0].Method != http.MethodPut || proxy.calls[0].Path != "/api/v1/workspace-env" {
		t.Fatalf("sync calls after PUT = %+v", proxy.calls)
	}
	if proxy.calls[0].UserID != testUserID {
		t.Fatalf("sync user id after PUT = %q", proxy.calls[0].UserID)
	}
	if !strings.Contains(string(proxy.calls[0].Body), `"CODEX_BASE_URL":"https://proxy.example/v1"`) ||
		!strings.Contains(string(proxy.calls[0].Body), `"OPENAI_API_KEY":"sk-secret"`) {
		t.Fatalf("sync body after PUT = %s", proxy.calls[0].Body)
	}

	w = httptest.NewRecorder()
	req = withWorkspaceIDParam(newRequest(http.MethodDelete,
		"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", nil), testWorkspaceID)
	testHandler.DeleteWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: status %d body %s", w.Code, w.Body.String())
	}
	if len(proxy.calls) != 2 || !strings.Contains(string(proxy.calls[1].Body), `"env":null`) {
		t.Fatalf("sync calls after DELETE = %+v body=%s", proxy.calls, proxy.calls[len(proxy.calls)-1].Body)
	}
}

func TestWorkspaceCloudRuntimeEnv_MergesAcrossSaves(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installCloudRuntimeEnvBox(t)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace_cloud_runtime_env WHERE workspace_id = $1`, testWorkspaceID)
	})

	put := func(env map[string]string) {
		w := httptest.NewRecorder()
		req := withWorkspaceIDParam(newRequest(http.MethodPut,
			"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", map[string]any{"env": env}), testWorkspaceID)
		testHandler.PutWorkspaceCloudRuntimeEnv(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("PUT %v: status %d body %s", env, w.Code, w.Body.String())
		}
	}
	// Save vars one at a time — the second save must NOT drop the first.
	put(map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.example.com"})
	put(map[string]string{"CODEX_BASE_URL": "https://proxy.example.com"})
	put(map[string]string{"MULTICA_CODEX_MODEL": "gpt-5-codex"})
	put(map[string]string{"MULTICA_CLAUDE_MODEL": "gpt-5-codex"})
	put(map[string]string{"OPENAI_API_KEY": "sk-bbbb"})
	put(map[string]string{"ANTHROPIC_API_KEY": "sk-bbbb"})

	w := httptest.NewRecorder()
	req := withWorkspaceIDParam(newRequest(http.MethodGet,
		"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", nil), testWorkspaceID)
	testHandler.GetWorkspaceCloudRuntimeEnv(w, req)
	var got struct {
		Env []struct {
			Name string `json:"name"`
		} `json:"env"`
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	names := map[string]bool{}
	for _, e := range got.Env {
		names[e.Name] = true
	}
	for _, want := range []string{"ANTHROPIC_BASE_URL", "CODEX_BASE_URL", "MULTICA_CLAUDE_MODEL", "MULTICA_CODEX_MODEL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		if !names[want] {
			t.Fatalf("merge lost %s; got %v", want, names)
		}
	}
	// Re-saving an existing name updates its value, not duplicates it.
	put(map[string]string{"OPENAI_API_KEY": "sk-cccc"})
	w = httptest.NewRecorder()
	req = withWorkspaceIDParam(newRequest(http.MethodGet,
		"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", nil), testWorkspaceID)
	testHandler.GetWorkspaceCloudRuntimeEnv(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET after update decode: %v", err)
	}
	if len(got.Env) != 6 {
		t.Fatalf("expected 6 vars after merges, got %d", len(got.Env))
	}
}

func TestWorkspaceCloudRuntimeEnv_PrunesDeprecatedModelEnvNames(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installCloudRuntimeEnvBox(t)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace_cloud_runtime_env WHERE workspace_id = $1`, testWorkspaceID)
	})

	put := func(env map[string]string) {
		w := httptest.NewRecorder()
		req := withWorkspaceIDParam(newRequest(http.MethodPut,
			"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", map[string]any{"env": env}), testWorkspaceID)
		testHandler.PutWorkspaceCloudRuntimeEnv(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("PUT %v: status %d body %s", env, w.Code, w.Body.String())
		}
	}

	put(map[string]string{
		"CODEX_MODEL":          "old-codex-model",
		"ANTHROPIC_MODEL":      "old-claude-model",
		"ANTHROPIC_AUTH_TOKEN": "old-claude-token",
	})
	put(map[string]string{
		"MULTICA_CODEX_MODEL":  "gpt-5.5",
		"MULTICA_CLAUDE_MODEL": "gpt-5.5",
	})

	row, err := testHandler.Queries.GetWorkspaceCloudRuntimeEnv(context.Background(), parseUUID(testWorkspaceID))
	if err != nil {
		t.Fatalf("load env row: %v", err)
	}
	env, err := openCloudRuntimeEnv(testHandler, row.EnvSealed)
	if err != nil {
		t.Fatalf("open env: %v", err)
	}
	for _, deprecated := range []string{"CODEX_MODEL", "ANTHROPIC_MODEL", "ANTHROPIC_AUTH_TOKEN"} {
		if _, ok := env[deprecated]; ok {
			t.Fatalf("deprecated %s should be pruned, got %v", deprecated, env)
		}
	}
	for _, canonical := range []string{"MULTICA_CODEX_MODEL", "MULTICA_CLAUDE_MODEL"} {
		if env[canonical] != "gpt-5.5" {
			t.Fatalf("canonical %s = %q, want gpt-5.5", canonical, env[canonical])
		}
	}
}

func TestWorkspaceCloudRuntimeEnv_RemovesSelectedVariables(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installCloudRuntimeEnvBox(t)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace_cloud_runtime_env WHERE workspace_id = $1`, testWorkspaceID)
	})

	w := httptest.NewRecorder()
	req := withWorkspaceIDParam(newRequest(http.MethodPut,
		"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", map[string]any{
			"env": map[string]string{
				"ANTHROPIC_API_KEY":  "sk-secret",
				"ANTHROPIC_BASE_URL": "https://proxy.example.com",
				"CODEX_BASE_URL":     "https://proxy.example.com",
				"OPENAI_API_KEY":     "sk-secret",
			},
		}), testWorkspaceID)
	testHandler.PutWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("seed PUT: status %d body %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = withWorkspaceIDParam(newRequest(http.MethodPut,
		"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", map[string]any{
			"remove_env": []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
		}), testWorkspaceID)
	testHandler.PutWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("remove PUT: status %d body %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "OPENAI_API_KEY") || strings.Contains(w.Body.String(), "ANTHROPIC_API_KEY") || strings.Contains(w.Body.String(), "ANTHROPIC_AUTH_TOKEN") {
		t.Fatalf("removed key still returned: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "CODEX_BASE_URL") {
		t.Fatalf("non-sensitive config was removed too: %s", w.Body.String())
	}
}

func TestWorkspaceCloudRuntimeEnv_Validation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installCloudRuntimeEnvBox(t)

	for name, body := range map[string]map[string]any{
		"lowercase name":  {"env": map[string]string{"bad-name": "v"}},
		"empty value":     {"env": map[string]string{"GOOD_NAME": "  "}},
		"empty env":       {"env": map[string]string{}},
		"huge value":      {"env": map[string]string{"GOOD_NAME": strings.Repeat("x", maxCloudRuntimeEnvValueSize+1)}},
		"injection chars": {"env": map[string]string{"BAD NAME": "v"}},
	} {
		w := httptest.NewRecorder()
		req := withWorkspaceIDParam(newRequest(http.MethodPut, "/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", body), testWorkspaceID)
		testHandler.PutWorkspaceCloudRuntimeEnv(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", name, w.Code)
		}
	}
}

func TestWorkspaceCloudRuntimeEnv_NotConfigured(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	allowCloudRuntimeForTest(t)
	prev := testHandler.CloudRuntimeEnvBox
	testHandler.CloudRuntimeEnvBox = nil
	t.Cleanup(func() { testHandler.CloudRuntimeEnvBox = prev })

	w := httptest.NewRecorder()
	req := withWorkspaceIDParam(newRequest(http.MethodPut, "/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", map[string]any{
		"env": map[string]string{"A_KEY": "v"},
	}), testWorkspaceID)
	testHandler.PutWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("PUT without box: status = %d, want 503", w.Code)
	}

	w = httptest.NewRecorder()
	req = withWorkspaceIDParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", nil), testWorkspaceID)
	testHandler.GetWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatalf("GET without box = %d %s", w.Code, w.Body.String())
	}
}
