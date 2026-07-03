package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

// setupP4BatchFixture wires a pending assessment row for a done Feishu binding
// plus a worker task (a plain agent task — batch workers carry no per-binding
// context; the lease is the per-item scope). The workspace is temporarily
// allowlisted for the duration of the test.
func setupP4BatchFixture(t *testing.T) (bindingID, agentID, taskID, issueID string) {
	t.Helper()
	ctx := context.Background()
	issueID = createTestIssue(t, "p4 batch fixture", "done", "low")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	agentID = createHandlerTestAgent(t, "P4 Batch Agent", nil)

	var integrationID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO feishu_project_integration (workspace_id, project_key, plugin_id, plugin_secret, created_by_id)
		VALUES ($1, 'P4BatchFixture', 'plug', 'sec', $2) RETURNING id
	`, testWorkspaceID, testUserID).Scan(&integrationID); err != nil {
		t.Fatalf("insert integration: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM feishu_project_integration WHERE id = $1`, integrationID) })

	if err := testPool.QueryRow(ctx, `
		INSERT INTO feishu_project_issue_binding (
			workspace_id, integration_id, issue_id, project_key, work_item_type,
			work_item_id, external_identifier, external_url, external_status_label
		) VALUES ($1, $2, $3, 'P4BatchFixture', 'issue', 'BUG-9', 'BUG-9', 'https://x.test/BUG-9', 'Done')
		RETURNING id
	`, testWorkspaceID, integrationID, issueID).Scan(&bindingID); err != nil {
		t.Fatalf("insert binding: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM feishu_project_issue_binding WHERE id = $1`, bindingID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_fix_p4_assessment (workspace_id, issue_id, feishu_binding_id, assessment_status)
		VALUES ($1, $2, $3, 'pending')
	`, testWorkspaceID, issueID, bindingID); err != nil {
		t.Fatalf("insert assessment row: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`, bindingID)
	})

	taskID = createHandlerTestTaskForAgentOnIssue(t, agentID, issueID)

	previous := testHandler.P4AssessmentService.Allowlist
	testHandler.P4AssessmentService.Allowlist = service.P4AssessmentAllowlist{testWorkspaceID: true}
	t.Cleanup(func() { testHandler.P4AssessmentService.Allowlist = previous })

	return bindingID, agentID, taskID, issueID
}

func pullPendingRequest(t *testing.T, agentID, taskID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/operations/assessments/pending?limit=5", nil)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	if agentID != "" {
		req.Header.Set("X-Agent-ID", agentID)
	}
	if taskID != "" {
		req.Header.Set("X-Task-ID", taskID)
	}
	testHandler.ListPendingP4Assessments(w, req)
	return w
}

func submitByRefRequest(t *testing.T, agentID, taskID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/operations/assessments/result", body)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	if agentID != "" {
		req.Header.Set("X-Agent-ID", agentID)
	}
	if taskID != "" {
		req.Header.Set("X-Task-ID", taskID)
	}
	testHandler.SubmitP4AssessmentResultByRef(w, req)
	return w
}

func TestBatchAssessment_PullLeasesAndSecondPullEmpty(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	bindingID, agentID, taskID, _ := setupP4BatchFixture(t)

	w := pullPendingRequest(t, agentID, taskID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []struct {
			Ref            string         `json:"ref"`
			Issue          map[string]any `json:"issue"`
			LeaseExpiresAt string         `json:"lease_expires_at"`
			Evidence       map[string]any `json:"evidence"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d: %s", len(resp.Items), w.Body.String())
	}
	item := resp.Items[0]
	if item.Ref != bindingID {
		t.Fatalf("ref = %q, want binding id %q", item.Ref, bindingID)
	}
	if item.LeaseExpiresAt == "" || item.Evidence == nil || item.Issue == nil {
		t.Fatalf("item missing lease/evidence/issue: %s", w.Body.String())
	}

	// The row is now leased to the worker task.
	var status, leasedTask string
	if err := testPool.QueryRow(context.Background(),
		`SELECT assessment_status, assessment_task_id::text FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`,
		bindingID,
	).Scan(&status, &leasedTask); err != nil {
		t.Fatalf("load assessment: %v", err)
	}
	if status != "running" || leasedTask != taskID {
		t.Fatalf("row = %s leased to %s, want running leased to %s", status, leasedTask, taskID)
	}

	// A second pull finds nothing claimable while the lease is active.
	w2 := pullPendingRequest(t, agentID, taskID)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 on second pull, got %d: %s", w2.Code, w2.Body.String())
	}
	var resp2 struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if len(resp2.Items) != 0 {
		t.Fatalf("expected 0 items on second pull, got %d", len(resp2.Items))
	}
}

func TestBatchAssessment_SubmitByRefCompletesRow(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	bindingID, agentID, taskID, _ := setupP4BatchFixture(t)
	if w := pullPendingRequest(t, agentID, taskID); w.Code != http.StatusOK {
		t.Fatalf("pull failed: %d %s", w.Code, w.Body.String())
	}

	w := submitByRefRequest(t, agentID, taskID, map[string]any{
		"ref":                             bindingID,
		"delivery_attribution_prediction": "ai_delivered",
		"quality_prediction":              "likely_correct",
		"confidence":                      0.9,
		"ai_shelved_cls":                  []int{123456},
		"summary":                         "AI shelve was submitted",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var status, delivery, quality string
	var leasedUntil *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT assessment_status, delivery_attribution_prediction, quality_prediction, leased_until::text
		 FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`, bindingID,
	).Scan(&status, &delivery, &quality, &leasedUntil); err != nil {
		t.Fatalf("load assessment: %v", err)
	}
	if status != "completed" || delivery != "ai_delivered" || quality != "likely_correct" {
		t.Fatalf("row = %s/%s/%s, want completed/ai_delivered/likely_correct", status, delivery, quality)
	}
	if leasedUntil != nil {
		t.Fatalf("leased_until should be cleared on completion, got %v", *leasedUntil)
	}
}

func TestBatchAssessment_SubmitUnleasedRefConflicts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	bindingID, agentID, taskID, _ := setupP4BatchFixture(t)
	// No pull: the row is pending, not leased to this task.
	w := submitByRefRequest(t, agentID, taskID, map[string]any{
		"ref":                bindingID,
		"quality_prediction": "likely_correct",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for unleased ref, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBatchAssessment_SubmitInvalidEnumRejected(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	bindingID, agentID, taskID, _ := setupP4BatchFixture(t)
	if w := pullPendingRequest(t, agentID, taskID); w.Code != http.StatusOK {
		t.Fatalf("pull failed: %d %s", w.Code, w.Body.String())
	}
	w := submitByRefRequest(t, agentID, taskID, map[string]any{
		"ref":                bindingID,
		"quality_prediction": "perfect",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid enum, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBatchAssessment_FailsClosedWithoutAllowlist(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_, agentID, taskID, _ := setupP4BatchFixture(t)
	testHandler.P4AssessmentService.Allowlist = nil

	if w := pullPendingRequest(t, agentID, taskID); w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without allowlist, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBatchAssessment_RejectsNonAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	setupP4BatchFixture(t)
	if w := pullPendingRequest(t, "", ""); w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-agent, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBatchAssessment_ExpiredLeaseIsReclaimable(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	bindingID, agentID, taskID, issueID := setupP4BatchFixture(t)
	if w := pullPendingRequest(t, agentID, taskID); w.Code != http.StatusOK {
		t.Fatalf("pull failed: %d %s", w.Code, w.Body.String())
	}
	// Simulate a dead worker: expire the lease.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent_fix_p4_assessment SET leased_until = now() - interval '1 minute' WHERE feishu_binding_id = $1`,
		bindingID,
	); err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	otherTaskID := createHandlerTestTaskForAgentOnIssue(t, agentID, issueID)
	w := pullPendingRequest(t, agentID, otherTaskID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []struct {
			Ref string `json:"ref"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Ref != bindingID {
		t.Fatalf("expected the expired lease to be reclaimed, got %s", w.Body.String())
	}

	// The original worker's submit now loses to the new lease → 409.
	wOld := submitByRefRequest(t, agentID, taskID, map[string]any{
		"ref":                bindingID,
		"quality_prediction": "likely_correct",
	})
	if wOld.Code != http.StatusConflict {
		t.Fatalf("expected 409 for reclaimed lease, got %d: %s", wOld.Code, wOld.Body.String())
	}
}
