package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

// setupP4SubmitFixture wires an assessment task + running assessment row for a
// done Feishu binding, all scoped to testWorkspaceID, and returns the binding
// id and the agent's task id. The task context matches what the submit
// endpoint's auth (requestTaskCanReadP4Evidence) checks.
func setupP4SubmitFixture(t *testing.T) (bindingID, agentID, taskID, issueID string) {
	t.Helper()
	ctx := context.Background()
	issueID = createTestIssue(t, "p4 submit fixture", "done", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	agentID = createHandlerTestAgent(t, "P4 Submit Agent", nil)

	var integrationID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO feishu_project_integration (workspace_id, project_key, plugin_id, plugin_secret, created_by_id)
		VALUES ($1, 'P4SubmitFixture', 'plug', 'sec', $2) RETURNING id
	`, testWorkspaceID, testUserID).Scan(&integrationID); err != nil {
		t.Fatalf("insert integration: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM feishu_project_integration WHERE id = $1`, integrationID) })

	if err := testPool.QueryRow(ctx, `
		INSERT INTO feishu_project_issue_binding (
			workspace_id, integration_id, issue_id, project_key, work_item_type,
			work_item_id, external_identifier, external_url, external_status_label
		) VALUES ($1, $2, $3, 'P4SubmitFixture', 'issue', 'BUG-1', 'BUG-1', 'https://x.test/BUG-1', 'Done')
		RETURNING id
	`, testWorkspaceID, integrationID, issueID).Scan(&bindingID); err != nil {
		t.Fatalf("insert binding: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM feishu_project_issue_binding WHERE id = $1`, bindingID) })

	taskID = createHandlerTestTaskForAgentOnIssue(t, agentID, issueID)
	taskContext, _ := json.Marshal(map[string]string{
		"type":              service.P4AssessmentTaskType,
		"workspace_id":      testWorkspaceID,
		"issue_id":          issueID,
		"feishu_binding_id": bindingID,
		"mode":              "assess_only",
		"prompt_version":    service.P4AssessmentPromptVersion,
	})
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_task_queue SET context = $2, task_category = 'analysis' WHERE id = $1`,
		taskID, taskContext,
	); err != nil {
		t.Fatalf("update task context: %v", err)
	}

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_fix_p4_assessment (workspace_id, issue_id, feishu_binding_id, assessment_task_id, assessment_status)
		VALUES ($1, $2, $3, $4, 'running')
	`, testWorkspaceID, issueID, bindingID, taskID); err != nil {
		t.Fatalf("insert assessment row: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`, bindingID)
	})
	return bindingID, agentID, taskID, issueID
}

func submitP4Request(t *testing.T, bindingID, agentID, taskID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost,
		fmt.Sprintf("/api/operations/agent-fixes/%s/p4-assessment/result", bindingID), body)
	req = withURLParam(req, "bindingId", bindingID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	if agentID != "" {
		req.Header.Set("X-Agent-ID", agentID)
	}
	if taskID != "" {
		req.Header.Set("X-Task-ID", taskID)
	}
	testHandler.SubmitAgentFixP4Assessment(w, req)
	return w
}

func TestSubmitAgentFixP4Assessment_HappyPath(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	bindingID, agentID, taskID, _ := setupP4SubmitFixture(t)

	w := submitP4Request(t, bindingID, agentID, taskID, map[string]any{
		"delivery_attribution_prediction": "human_delivered",
		"quality_prediction":              "likely_wrong",
		"confidence":                      0.8,
		"summary":                         "human CL delivered",
		"external_committed_cls":          []int{278969},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status, delivery, quality string
	if err := testPool.QueryRow(context.Background(),
		`SELECT assessment_status, delivery_attribution_prediction, quality_prediction
		 FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`, bindingID,
	).Scan(&status, &delivery, &quality); err != nil {
		t.Fatalf("load assessment: %v", err)
	}
	if status != "completed" || delivery != "human_delivered" || quality != "likely_wrong" {
		t.Fatalf("row = %s/%s/%s, want completed/human_delivered/likely_wrong", status, delivery, quality)
	}
}

func TestSubmitAgentFixP4Assessment_InvalidBody(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	bindingID, agentID, taskID, _ := setupP4SubmitFixture(t)
	w := submitP4Request(t, bindingID, agentID, taskID, map[string]any{
		"quality_prediction": "perfect", // invalid enum
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid enum, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSubmitAgentFixP4Assessment_RejectsNonAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	bindingID, _, _, _ := setupP4SubmitFixture(t)
	// No X-Agent-ID/X-Task-ID → actor resolves to member → 403.
	w := submitP4Request(t, bindingID, "", "", map[string]any{"quality_prediction": "likely_correct"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-agent, got %d: %s", w.Code, w.Body.String())
	}
}
