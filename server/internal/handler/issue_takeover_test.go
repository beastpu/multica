package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type takeoverTestResponse struct {
	Issue struct {
		AssigneeType *string `json:"assignee_type"`
		AssigneeID   *string `json:"assignee_id"`
	} `json:"issue"`
	Takeover *struct {
		FromAgent *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"from_agent"`
		TaskID    string `json:"task_id"`
		WorkDir   string `json:"work_dir"`
		SessionID string `json:"session_id"`
		Runtime   *struct {
			Name string `json:"name"`
		} `json:"runtime"`
	} `json:"takeover"`
}

func postTakeover(t *testing.T, issueID string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodPost, "/api/issues/"+issueID+"/takeover", nil,
	), "id", issueID)
	testHandler.TakeoverIssue(recorder, request)
	return recorder
}

// The contract: one action stops the agent, hands the issue to the caller,
// records the handover on the timeline, and returns the work scene. Each half
// existed before; the takeover's whole value is that they cannot be observed
// apart.
func TestTakeoverCancelsReassignsNotesAndHandsOverTheScene(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "Takeover Agent", nil)
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id,
			number, position, assignee_type, assignee_id
		) VALUES ($1, 'Takeover target', 'in_progress', 'none', 'member', $2, $3, 0, 'agent', $4)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t), agentID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, session_id, work_dir
		)
		VALUES ($1, $2, $3, 'running', 0, now(), 'sess-takeover-1', '/home/dev/multica-envs/repo')
		RETURNING id
	`, agentID, testRuntimeID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("create running task: %v", err)
	}

	recorder := postTakeover(t, issueID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("takeover status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response takeoverTestResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if response.Issue.AssigneeType == nil || *response.Issue.AssigneeType != "member" ||
		response.Issue.AssigneeID == nil || *response.Issue.AssigneeID != testUserID {
		t.Errorf("assignee = %v/%v, want member/%s",
			response.Issue.AssigneeType, response.Issue.AssigneeID, testUserID)
	}

	var taskStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM agent_task_queue WHERE id = $1
	`, taskID).Scan(&taskStatus); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if taskStatus != "cancelled" {
		t.Errorf("task status = %q, want cancelled", taskStatus)
	}

	// The card is the point of calling this instead of plain reassign: the
	// session id and workdir are what let a person continue instead of restart.
	if response.Takeover == nil {
		t.Fatal("takeover card missing")
	}
	if response.Takeover.SessionID != "sess-takeover-1" {
		t.Errorf("card session_id = %q, want the interrupted session", response.Takeover.SessionID)
	}
	if response.Takeover.WorkDir == "" ||
		strings.HasPrefix(response.Takeover.WorkDir, "/home/dev") {
		t.Errorf("card work_dir = %q, want a privacy-stripped path", response.Takeover.WorkDir)
	}
	if response.Takeover.FromAgent == nil || response.Takeover.FromAgent.Name != "Takeover Agent" {
		t.Errorf("card from_agent = %+v, want the interrupted agent", response.Takeover.FromAgent)
	}

	var noteCount int
	var noteContent string
	if err := testPool.QueryRow(ctx, `
		SELECT count(*), max(content) FROM comment
		WHERE issue_id = $1 AND author_type = 'system'
	`, issueID).Scan(&noteCount, &noteContent); err != nil {
		t.Fatalf("read note: %v", err)
	}
	if noteCount != 1 {
		t.Fatalf("system notes = %d, want exactly one", noteCount)
	}
	if !strings.Contains(noteContent, "Takeover Agent") {
		t.Errorf("note %q does not name the agent taken over from", noteContent)
	}

	// A double-click is two identical requests, not two handovers: the second
	// must succeed without writing a second note.
	if recorder := postTakeover(t, issueID); recorder.Code != http.StatusOK {
		t.Fatalf("repeat takeover status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM comment WHERE issue_id = $1 AND author_type = 'system'
	`, issueID).Scan(&noteCount); err != nil {
		t.Fatalf("recount notes: %v", err)
	}
	if noteCount != 1 {
		t.Errorf("system notes after repeat = %d, want still one", noteCount)
	}
}

// The card must survive the takeover: the person who pressed the button on
// their phone reads the workdir later, from the issue.
func TestGetIssueTakeoverServesTheCardAfterTheFact(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "Card Agent", nil)
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id,
			number, position, assignee_type, assignee_id
		) VALUES ($1, 'Card target', 'in_progress', 'none', 'member', $2, $3, 0, 'member', $2)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, issue_id, status, priority,
			started_at, completed_at, session_id
		)
		VALUES ($1, $2, $3, 'cancelled', 0, now(), now(), 'sess-card-1')
	`, agentID, testRuntimeID, issueID); err != nil {
		t.Fatalf("create cancelled task: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodGet, "/api/issues/"+issueID+"/takeover", nil,
	), "id", issueID)
	testHandler.GetIssueTakeover(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get takeover status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response takeoverTestResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Takeover == nil || response.Takeover.SessionID != "sess-card-1" {
		t.Errorf("card = %+v, want the cancelled task's session", response.Takeover)
	}
}

// An issue no agent ever worked has nothing to hand over — the panel needs a
// clean 404 to hide itself, distinct from a failure.
func TestGetIssueTakeoverIsNotFoundWithoutAgentWork(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Never touched by an agent', 'todo', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodGet, "/api/issues/"+issueID+"/takeover", nil,
	), "id", issueID)
	testHandler.GetIssueTakeover(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", recorder.Code, recorder.Body.String())
	}
}
