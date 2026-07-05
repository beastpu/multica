package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

// createLocalRuntimeAgent seeds a daemon-served local runtime and an agent on
// it — the only agent shape the capability config accepts (assessment needs
// inner-network access, so cloud runtimes are rejected).
func createLocalRuntimeAgent(t *testing.T, name string) (agentID, runtimeID string) {
	t.Helper()
	ctx := context.Background()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, NULL, $2, 'local', 'claude_code', 'online', 'capability test runtime', '{}'::jsonb, $3, now())
		RETURNING id
	`, testWorkspaceID, name+" Runtime", testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("create local runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'local', '{}'::jsonb, $3, 'workspace', 1, $4)
		RETURNING id
	`, testWorkspaceID, name, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create local agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})
	return agentID, runtimeID
}

// withCapabilityURLParams sets both chi URL params in ONE route context —
// chaining withURLParam would replace the context and drop the first param.
func withCapabilityURLParams(req *http.Request, workspaceID, capability string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	rctx.URLParams.Add("capability", capability)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func capabilityRequest(t *testing.T, method string, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(method, "/api/workspaces/"+testWorkspaceID+"/capabilities/"+service.CapabilityP4Assessment, body)
	req = withCapabilityURLParams(req, testWorkspaceID, service.CapabilityP4Assessment)
	switch method {
	case http.MethodGet:
		testHandler.GetWorkspaceCapability(w, req)
	case http.MethodPut:
		testHandler.PutWorkspaceCapability(w, req)
	case http.MethodDelete:
		testHandler.DeleteWorkspaceCapability(w, req)
	default:
		t.Fatalf("unsupported method %s", method)
	}
	return w
}

func cleanupCapabilityRow(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM workspace_agent_capability WHERE workspace_id = $1`, testWorkspaceID)
	})
}

func TestWorkspaceCapabilityGetReturns404WhenUnconfigured(t *testing.T) {
	cleanupCapabilityRow(t)
	if w := capabilityRequest(t, http.MethodGet, nil); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unconfigured capability, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkspaceCapabilityRejectsUnknownCapability(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/capabilities/nonsense", nil)
	req = withCapabilityURLParams(req, testWorkspaceID, "nonsense")
	testHandler.GetWorkspaceCapability(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown capability key, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkspaceCapabilityPutRejectsCloudRuntimeAgent(t *testing.T) {
	cleanupCapabilityRow(t)
	// The shared fixture agent runs on a cloud runtime — the exact shape the
	// config must reject (assessment needs a daemon-served local runtime).
	cloudAgentID := createHandlerTestAgent(t, "Capability Cloud Agent", nil)
	w := capabilityRequest(t, http.MethodPut, map[string]any{"agent_id": cloudAgentID})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for cloud-runtime agent, got %d: %s", w.Code, w.Body.String())
	}
	if g := capabilityRequest(t, http.MethodGet, nil); g.Code != http.StatusNotFound {
		t.Fatalf("rejected PUT must not persist a row, GET = %d", g.Code)
	}
}

func TestWorkspaceCapabilityPutRejectsForeignWorkspaceAgent(t *testing.T) {
	cleanupCapabilityRow(t)
	ctx := context.Background()
	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Capability foreign ws', 'capability-foreign-ws', '', 'CFW') RETURNING id
	`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("insert foreign workspace: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID) })

	var foreignRuntimeID, foreignAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at)
		VALUES ($1, NULL, 'Foreign Runtime', 'local', 'claude_code', 'online', '', '{}'::jsonb, $2, now()) RETURNING id
	`, otherWorkspaceID, testUserID).Scan(&foreignRuntimeID); err != nil {
		t.Fatalf("insert foreign runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id)
		VALUES ($1, 'Foreign Agent', '', 'local', '{}'::jsonb, $2, 'workspace', 1, $3) RETURNING id
	`, otherWorkspaceID, foreignRuntimeID, testUserID).Scan(&foreignAgentID); err != nil {
		t.Fatalf("insert foreign agent: %v", err)
	}

	w := capabilityRequest(t, http.MethodPut, map[string]any{"agent_id": foreignAgentID})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for foreign-workspace agent, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkspaceCapabilityPutGetDeleteRoundTrip(t *testing.T) {
	cleanupCapabilityRow(t)
	agentID, _ := createLocalRuntimeAgent(t, "Capability Roundtrip Agent")

	w := capabilityRequest(t, http.MethodPut, map[string]any{
		"agent_id":             agentID,
		"max_concurrent_tasks": 3,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp workspaceCapabilityResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if resp.AgentID != agentID || resp.Capability != service.CapabilityP4Assessment || resp.MaxConcurrentTasks != 3 {
		t.Fatalf("PUT response = %+v, want agent %s / p4_assessment / max 3", resp, agentID)
	}

	g := capabilityRequest(t, http.MethodGet, nil)
	if g.Code != http.StatusOK {
		t.Fatalf("GET after PUT: expected 200, got %d: %s", g.Code, g.Body.String())
	}

	// Upsert: repointing to another agent overwrites the same row.
	otherAgentID, _ := createLocalRuntimeAgent(t, "Capability Roundtrip Agent B")
	w = capabilityRequest(t, http.MethodPut, map[string]any{"agent_id": otherAgentID})
	if w.Code != http.StatusOK {
		t.Fatalf("second PUT: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode second PUT response: %v", err)
	}
	if resp.AgentID != otherAgentID || resp.MaxConcurrentTasks != 1 {
		t.Fatalf("upsert response = %+v, want agent %s / max 1 (default)", resp, otherAgentID)
	}

	if d := capabilityRequest(t, http.MethodDelete, nil); d.Code != http.StatusNoContent {
		t.Fatalf("DELETE: expected 204, got %d: %s", d.Code, d.Body.String())
	}
	if g := capabilityRequest(t, http.MethodGet, nil); g.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE: expected 404, got %d: %s", g.Code, g.Body.String())
	}
}
