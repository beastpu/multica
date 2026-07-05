package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// createDerivedWorkTask wires the derived-work actor shape the guard anchors
// on: a plain agent task hanging on an issue that carries the server-reserved
// metadata.agent_work marker. Returns the task id and its own (projection)
// issue id.
func createDerivedWorkTask(t *testing.T, agentID string) (taskID, ownIssueID string) {
	t.Helper()
	ownIssueID = createTestIssue(t, "derived work projection issue", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, ownIssueID) })
	markIssueAsAgentWork(t, ownIssueID)
	taskID = createHandlerTestTaskForAgentOnIssue(t, agentID, ownIssueID)
	return taskID, ownIssueID
}

func assertIssueStatus(t *testing.T, issueID, want string) {
	t.Helper()
	var got string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM issue WHERE id = $1`,
		issueID,
	).Scan(&got); err != nil {
		t.Fatalf("load issue status: %v", err)
	}
	if got != want {
		t.Fatalf("issue status = %q, want %q", got, want)
	}
}

func TestDerivedWorkTaskCannotUpdateRealIssueStatus(t *testing.T) {
	realIssueID := createTestIssue(t, "derived work status guard", "done", "low")
	t.Cleanup(func() { deleteTestIssue(t, realIssueID) })
	agentID := createHandlerTestAgent(t, "Derived Work Status Guard Agent", nil)
	taskID, _ := createDerivedWorkTask(t, agentID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/issues/"+realIssueID, map[string]any{"status": "in_progress"})
	req = withURLParam(req, "id", realIssueID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)

	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for derived-work task status update, got %d: %s", w.Code, w.Body.String())
	}
	assertIssueStatus(t, realIssueID, "done")
}

func TestDerivedWorkTaskCannotBatchUpdateIssues(t *testing.T) {
	realIssueID := createTestIssue(t, "derived work batch status guard", "done", "low")
	t.Cleanup(func() { deleteTestIssue(t, realIssueID) })
	agentID := createHandlerTestAgent(t, "Derived Work Batch Guard Agent", nil)
	taskID, _ := createDerivedWorkTask(t, agentID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{realIssueID},
		"updates":   map[string]any{"status": "in_progress"},
	})
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)

	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for derived-work task batch status update, got %d: %s", w.Code, w.Body.String())
	}
	assertIssueStatus(t, realIssueID, "done")
}

func TestDerivedWorkTaskCannotCommentOnRealIssue(t *testing.T) {
	realIssueID := createTestIssue(t, "derived work comment guard", "done", "low")
	t.Cleanup(func() { deleteTestIssue(t, realIssueID) })
	agentID := createHandlerTestAgent(t, "Derived Work Comment Guard Agent", nil)
	taskID, _ := createDerivedWorkTask(t, agentID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/"+realIssueID+"/comments", map[string]any{
		"content": "assessment result: likely_correct",
	})
	req = withURLParam(req, "id", realIssueID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)

	testHandler.CreateComment(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for derived-work task comment on a real issue, got %d: %s", w.Code, w.Body.String())
	}
}

// The narration carve-out is scoped to the task's OWN issue: a derived-work
// task holding one projection issue must not be able to comment on another
// run's projection issue.
func TestDerivedWorkTaskCannotCommentOnOtherAgentWorkIssue(t *testing.T) {
	otherIssueID := createTestIssue(t, "someone else's projection issue", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, otherIssueID) })
	markIssueAsAgentWork(t, otherIssueID)

	agentID := createHandlerTestAgent(t, "Cross Projection Guard Agent", nil)
	taskID, _ := createDerivedWorkTask(t, agentID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/"+otherIssueID+"/comments", map[string]any{
		"content": "narration on the wrong issue",
	})
	req = withURLParam(req, "id", otherIssueID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)

	testHandler.CreateComment(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for comment on another agent_work issue, got %d: %s", w.Code, w.Body.String())
	}
}

// The issue's agent_work marker is the ONLY guard anchor: the same task is
// rejected on status writes to its own issue (status stays server-projected)
// but passes the own-issue narration carve-out.
func TestAgentWorkIssueMetadataAnchorsGuard(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Metadata Anchor Guard Agent", nil)
	taskID, issueID := createDerivedWorkTask(t, agentID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{"status": "done"})
	req = withURLParam(req, "id", issueID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403: agent_work metadata alone must anchor the guard, got %d: %s", w.Code, w.Body.String())
	}
	assertIssueStatus(t, issueID, "todo")

	w = httptest.NewRecorder()
	req = newRequest(http.MethodPost, "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "narration through the metadata anchor",
	})
	req = withURLParam(req, "id", issueID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	testHandler.CreateComment(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for own-issue narration via metadata anchor, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDerivedWorkTaskCannotUpdateNonStatusField(t *testing.T) {
	realIssueID := createTestIssue(t, "derived work title guard", "done", "low")
	t.Cleanup(func() { deleteTestIssue(t, realIssueID) })
	agentID := createHandlerTestAgent(t, "Derived Work Title Guard Agent", nil)
	taskID, _ := createDerivedWorkTask(t, agentID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/issues/"+realIssueID, map[string]any{"title": "rewritten by derived work"})
	req = withURLParam(req, "id", realIssueID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)

	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for derived-work task non-status update, got %d: %s", w.Code, w.Body.String())
	}
}
