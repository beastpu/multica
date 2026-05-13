package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// withWorkspaceIDContext is a minimal middleware-context shim for
// CancelTaskByUser tests. The handler only reads workspace_id (no member
// role checks), so we inject an empty Member alongside the workspace id —
// withChatTestWorkspaceCtx is heavier-weight than necessary here and
// requires the calling user to be a real member, which two of these tests
// deliberately violate.
func withWorkspaceIDContext(req *http.Request, workspaceID string) *http.Request {
	return req.WithContext(middleware.SetMemberContext(req.Context(), workspaceID, db.Member{}))
}

// createQuickCreateTask seeds a quick-create-shaped agent_task_queue row
// (all FKs NULL, context JSONB carrying the quick-create payload) and
// returns its UUID. Cleanup is registered automatically.
func createQuickCreateTask(t *testing.T, agentID, requesterID, workspaceID string) string {
	t.Helper()
	payload, err := json.Marshal(service.QuickCreateContext{
		Type:        service.QuickCreateContextType,
		Prompt:      "test prompt",
		RequesterID: requesterID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		t.Fatalf("marshal quick-create context: %v", err)
	}
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, context)
		VALUES ($1, $2, 'queued', 100, $3)
		RETURNING id
	`, agentID, handlerTestRuntimeID(t), payload).Scan(&taskID); err != nil {
		t.Fatalf("create quick-create task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
	})
	return taskID
}

func readTaskStatus(t *testing.T, taskID string) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM agent_task_queue WHERE id = $1`, taskID,
	).Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	return status
}

// TestCancelTaskByUser_QuickCreate_Requester_Succeeds is the regression
// case: before the fix, every quick-create task fell through to the
// catch-all `else` branch and returned 404 even for the original
// requester, so a wedged quick-create could never be cancelled from the
// UI. With the fix the requester can cancel their own task and the row
// transitions to 'cancelled'.
func TestCancelTaskByUser_QuickCreate_Requester_Succeeds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelQuickCreateOK", []byte("[]"))
	taskID := createQuickCreateTask(t, agentID, testUserID, testWorkspaceID)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/tasks/"+taskID+"/cancel", nil)
	req = withURLParam(req, "taskId", taskID)
	req = withWorkspaceIDContext(req, testWorkspaceID)

	testHandler.CancelTaskByUser(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := readTaskStatus(t, taskID); got != "cancelled" {
		t.Fatalf("task status: expected 'cancelled', got %q", got)
	}
}

// TestCancelTaskByUser_QuickCreate_NotRequester_Returns403 mirrors the
// chat-task creator check: another workspace member cannot cancel
// someone else's quick-create. We use a hardcoded UUID for the "other"
// requester rather than inserting a real user — the handler only string-
// compares against the JSONB requester_id, no FK touches the user table.
func TestCancelTaskByUser_QuickCreate_NotRequester_Returns403(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelQuickCreate403", []byte("[]"))
	const otherRequesterID = "00000000-0000-0000-0000-000000000001"
	taskID := createQuickCreateTask(t, agentID, otherRequesterID, testWorkspaceID)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/tasks/"+taskID+"/cancel", nil)
	req = withURLParam(req, "taskId", taskID)
	req = withWorkspaceIDContext(req, testWorkspaceID)

	testHandler.CancelTaskByUser(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if got := readTaskStatus(t, taskID); got != "queued" {
		t.Fatalf("task must not be mutated when caller is not the requester; got status %q", got)
	}
}

// TestCancelTaskByUser_QuickCreate_CrossWorkspace_Returns404 guards
// against horizontal IDOR: even when the caller IS the original
// requester, if they're operating from a different workspace context
// they get 404 (matches the chat / issue branches' workspace-binding
// check). 404 over 403 because the task should appear non-existent to
// foreign-workspace probes.
func TestCancelTaskByUser_QuickCreate_CrossWorkspace_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	otherWorkspaceID := createOtherTestWorkspace(t)
	agentID := createHandlerTestAgent(t, "CancelQuickCreateXWS", []byte("[]"))
	// Task lives in otherWorkspace; caller's middleware context is
	// testWorkspaceID — handler should reject without leaking existence.
	taskID := createQuickCreateTask(t, agentID, testUserID, otherWorkspaceID)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/tasks/"+taskID+"/cancel", nil)
	req = withURLParam(req, "taskId", taskID)
	req = withWorkspaceIDContext(req, testWorkspaceID)

	testHandler.CancelTaskByUser(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if got := readTaskStatus(t, taskID); got != "queued" {
		t.Fatalf("task must not be mutated on cross-workspace cancel; got status %q", got)
	}
}

// TestCancelTaskByUser_OrphanTask_Returns404 pins the regression-safety
// of the catch-all branch: a task with no FKs and no quick-create
// context (e.g. an autopilot run-only task today, or an unknown future
// variant) still returns 404 rather than silently entering the
// quick-create code path.
func TestCancelTaskByUser_OrphanTask_Returns404(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "CancelOrphanTask", []byte("[]"))
	var taskID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority)
		VALUES ($1, $2, 'queued', 0)
		RETURNING id
	`, agentID, handlerTestRuntimeID(t)).Scan(&taskID); err != nil {
		t.Fatalf("create orphan task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
	})

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/tasks/"+taskID+"/cancel", nil)
	req = withURLParam(req, "taskId", taskID)
	req = withWorkspaceIDContext(req, testWorkspaceID)

	testHandler.CancelTaskByUser(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for task with no link and no quick-create context, got %d: %s", w.Code, w.Body.String())
	}
}
