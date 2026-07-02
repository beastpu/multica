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
