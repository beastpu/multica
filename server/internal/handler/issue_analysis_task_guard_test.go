package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createAnalysisTaskForIssue(t *testing.T, agentID, issueID string) string {
	t.Helper()
	taskID := createHandlerTestTaskForAgentOnIssue(t, agentID, issueID)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_task_queue SET task_category = 'analysis' WHERE id = $1`,
		taskID,
	); err != nil {
		t.Fatalf("mark task as analysis: %v", err)
	}
	return taskID
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

func TestAnalysisTaskCannotUpdateIssueStatus(t *testing.T) {
	issueID := createTestIssue(t, "analysis status guard", "done", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	agentID := createHandlerTestAgent(t, "Analysis Status Guard Agent", nil)
	taskID := createAnalysisTaskForIssue(t, agentID, issueID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{"status": "in_progress"})
	req = withURLParam(req, "id", issueID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)

	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for analysis task status update, got %d: %s", w.Code, w.Body.String())
	}
	assertIssueStatus(t, issueID, "done")
}

func TestAnalysisTaskCannotBatchUpdateIssueStatus(t *testing.T) {
	issueID := createTestIssue(t, "analysis batch status guard", "done", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	agentID := createHandlerTestAgent(t, "Analysis Batch Status Guard Agent", nil)
	taskID := createAnalysisTaskForIssue(t, agentID, issueID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/batch-update", map[string]any{
		"issue_ids": []string{issueID},
		"updates":   map[string]any{"status": "in_progress"},
	})
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)

	testHandler.BatchUpdateIssues(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for analysis task batch status update, got %d: %s", w.Code, w.Body.String())
	}
	assertIssueStatus(t, issueID, "done")
}

func TestAnalysisTaskCannotComment(t *testing.T) {
	issueID := createTestIssue(t, "analysis comment guard", "done", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	agentID := createHandlerTestAgent(t, "Analysis Comment Guard Agent", nil)
	taskID := createAnalysisTaskForIssue(t, agentID, issueID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/issues/"+issueID+"/comments", map[string]any{
		"content": "assessment result: likely_correct",
	})
	req = withURLParam(req, "id", issueID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)

	testHandler.CreateComment(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for analysis task comment, got %d: %s", w.Code, w.Body.String())
	}
}

// The narration carve-out is scoped to the task's OWN issue: an analysis task
// holding one projection issue must not be able to comment on another run's
// projection issue (plan C-1 guard tightening).
func TestAnalysisTaskCannotCommentOnOtherAgentWorkIssue(t *testing.T) {
	ownIssueID := createTestIssue(t, "own projection issue", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, ownIssueID) })
	markIssueAsAgentWork(t, ownIssueID)
	otherIssueID := createTestIssue(t, "someone else's projection issue", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, otherIssueID) })
	markIssueAsAgentWork(t, otherIssueID)

	agentID := createHandlerTestAgent(t, "Cross Projection Guard Agent", nil)
	taskID := createAnalysisTaskForIssue(t, agentID, ownIssueID)

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

// Transitional guard anchor (plan C-1): a task hanging on an agent_work issue
// is a derived-work actor even when task_category is not 'analysis' (the
// column is retired in C-2). Writes are rejected; own-issue comments pass.
func TestAgentWorkIssueTaskIsGuardedWithoutAnalysisCategory(t *testing.T) {
	issueID := createTestIssue(t, "metadata-anchored guard", "todo", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	markIssueAsAgentWork(t, issueID)
	agentID := createHandlerTestAgent(t, "Metadata Anchor Guard Agent", nil)
	// Plain fix-category task — only the issue's agent_work marker anchors it.
	taskID := createHandlerTestTaskForAgentOnIssue(t, agentID, issueID)

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

func TestAnalysisTaskCannotUpdateNonStatusField(t *testing.T) {
	issueID := createTestIssue(t, "analysis title guard", "done", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	agentID := createHandlerTestAgent(t, "Analysis Title Guard Agent", nil)
	taskID := createAnalysisTaskForIssue(t, agentID, issueID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{"title": "rewritten by analysis"})
	req = withURLParam(req, "id", issueID)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)

	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for analysis task non-status update, got %d: %s", w.Code, w.Body.String())
	}
}
