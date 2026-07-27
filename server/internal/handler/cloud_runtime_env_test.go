package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/multica-ai/multica/server/internal/cloudruntime"
)

func withWorkspaceIDParam(req *http.Request, workspaceID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// Workspace env is stored and validated by the standalone Fleet service; the
// server handlers are thin proxies. These tests pin the proxying: right
// method/path/scope, the workspace role gate, and the disabled case.

func TestWorkspaceCloudRuntimeEnv_ProxiesToFleet(t *testing.T) {
	proxy := &fakeCloudRuntimeProxy{
		enabled: true,
		resp: &cloudruntime.Response{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"configured":true,"env":[]}`),
		},
	}
	useCloudRuntimeProxy(t, proxy)

	cases := []struct {
		name       string
		method     string
		body       any
		callMethod string
	}{
		{"put", http.MethodPut, map[string]any{"env": map[string]string{"ANTHROPIC_API_KEY": "sk-1"}}, http.MethodPut},
		{"get", http.MethodGet, nil, http.MethodGet},
		{"delete", http.MethodDelete, nil, http.MethodDelete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proxy.called = false
			req := withWorkspaceIDParam(newRequest(tc.method,
				"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", tc.body), testWorkspaceID)
			w := httptest.NewRecorder()
			switch tc.method {
			case http.MethodPut:
				testHandler.PutWorkspaceCloudRuntimeEnv(w, req)
			case http.MethodGet:
				testHandler.GetWorkspaceCloudRuntimeEnv(w, req)
			case http.MethodDelete:
				testHandler.DeleteWorkspaceCloudRuntimeEnv(w, req)
			}
			if !proxy.called {
				t.Fatal("did not proxy to Fleet")
			}
			if proxy.req.Method != tc.callMethod || proxy.req.Path != "/api/v1/workspace-env" {
				t.Fatalf("proxied %s %s", proxy.req.Method, proxy.req.Path)
			}
			if got := proxy.req.Headers.Get("X-Workspace-ID"); got != testWorkspaceID {
				t.Fatalf("X-Workspace-ID = %q, want %s", got, testWorkspaceID)
			}
			if proxy.req.UserID != testUserID {
				t.Fatalf("user id = %q", proxy.req.UserID)
			}
		})
	}
}

func TestWorkspaceCloudRuntimeEnv_DeniedWorkspaceDoesNotReachFleet(t *testing.T) {
	proxy := &fakeCloudRuntimeProxy{enabled: true, resp: &cloudruntime.Response{StatusCode: http.StatusOK}}
	useCloudRuntimeProxy(t, proxy)
	denyCloudRuntimeForTest(t)

	req := withWorkspaceIDParam(newRequest(http.MethodPut,
		"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env",
		map[string]any{"env": map[string]string{"ANTHROPIC_API_KEY": "sk-denied"}}), testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.PutWorkspaceCloudRuntimeEnv(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if proxy.called {
		t.Fatal("denied workspace must not reach Fleet")
	}
}

func TestWorkspaceCloudRuntimeEnv_DisabledReturns503(t *testing.T) {
	useCloudRuntimeProxy(t, &fakeCloudRuntimeProxy{enabled: false})
	req := withWorkspaceIDParam(newRequest(http.MethodGet,
		"/api/workspaces/"+testWorkspaceID+"/cloud-runtime-env", nil), testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.GetWorkspaceCloudRuntimeEnv(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}
