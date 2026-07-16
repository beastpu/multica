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

	"github.com/multica-ai/multica/server/internal/util/secretbox"
)

func withWorkspaceIDParam(req *http.Request, workspaceID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func installCloudRuntimeEnvBox(t *testing.T) {
	t.Helper()
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

func TestWorkspaceCloudRuntimeEnv_PutGetDelete(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	installCloudRuntimeEnvBox(t)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace_cloud_runtime_env WHERE workspace_id = $1`, testWorkspaceID)
	})

	// PUT stores sealed env and echoes masked infos only.
	w := httptest.NewRecorder()
	req := withWorkspaceIDParam(newRequest(http.MethodPut, "/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", map[string]any{
		"env": map[string]string{
			"ANTHROPIC_AUTH_TOKEN": "sk-proxy-secret-abcd",
			"OPENAI_API_KEY":       "sk-openai-wxyz",
		},
	}), testWorkspaceID)
	testHandler.PutWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: status = %d body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "sk-proxy-secret-abcd") {
		t.Fatalf("PUT response leaks plaintext: %s", w.Body.String())
	}

	// The sealed row must not contain plaintext.
	var sealed []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT env_sealed FROM workspace_cloud_runtime_env WHERE workspace_id = $1`, testWorkspaceID).Scan(&sealed); err != nil {
		t.Fatalf("read sealed row: %v", err)
	}
	if strings.Contains(string(sealed), "sk-proxy-secret-abcd") {
		t.Fatal("env stored unencrypted")
	}

	// GET returns names + last4 only.
	w = httptest.NewRecorder()
	req = withWorkspaceIDParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", nil), testWorkspaceID)
	testHandler.GetWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET: status = %d body = %s", w.Code, w.Body.String())
	}
	var got struct {
		Configured bool `json:"configured"`
		Env        []struct {
			Name  string `json:"name"`
			Last4 string `json:"last4"`
		} `json:"env"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET decode: %v", err)
	}
	if !got.Configured || len(got.Env) != 2 {
		t.Fatalf("GET = %+v", got)
	}
	if got.Env[0].Name != "ANTHROPIC_AUTH_TOKEN" || got.Env[0].Last4 != "abcd" {
		t.Fatalf("GET env[0] = %+v", got.Env[0])
	}
	if strings.Contains(w.Body.String(), "sk-proxy-secret") {
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
