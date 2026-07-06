package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

// Native task flow (plan C): Trigger creates the projection issue ASSIGNED to
// the workspace's p4_assessment capability agent and a native agent task
// hanging on that issue, all in one transaction. Fail-closed: no capability
// row ⇒ the trigger is rejected.

// configureP4Capability inserts the capability row directly (the config API
// has its own tests) and returns the capability agent id.
func configureP4Capability(t *testing.T, agentID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO workspace_agent_capability (workspace_id, capability, agent_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, capability) DO UPDATE SET agent_id = EXCLUDED.agent_id
	`, testWorkspaceID, service.CapabilityP4Assessment, agentID); err != nil {
		t.Fatalf("configure capability: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM workspace_agent_capability WHERE workspace_id = $1`, testWorkspaceID)
	})
}

func TestTriggerFailsClosedWithoutCapability(t *testing.T) {
	bindingID, _ := setupP4BindingFixture(t)

	w := triggerP4AssessmentRequest(t, bindingID, false)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["created"] != false || resp["reason"] != "p4_assessment_capability_not_configured" {
		t.Fatalf("expected fail-closed rejection, got %v", resp)
	}
	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`, bindingID,
	).Scan(&count); err != nil {
		t.Fatalf("count assessment rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected trigger must not enqueue an assessment row, got %d", count)
	}
}

func TestTriggerRejectsCapabilityAgentOnCloudRuntime(t *testing.T) {
	bindingID, _ := setupP4BindingFixture(t)
	// Bypass the config API validation: a drifted row (agent later moved to a
	// cloud runtime) must still be rejected at trigger time.
	cloudAgentID := createHandlerTestAgent(t, "P4 Cloud Capability Agent", nil)
	configureP4Capability(t, cloudAgentID)

	w := triggerP4AssessmentRequest(t, bindingID, false)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["created"] != false || resp["reason"] != "capability_agent_runtime_not_local" {
		t.Fatalf("expected runtime-mode rejection, got %v", resp)
	}
}

func TestTriggerCreatesAssignedIssueAndNativeTask(t *testing.T) {
	bindingID, realIssueID := setupP4BindingFixture(t)
	agentID, _ := createLocalRuntimeAgent(t, "P4 Native Capability Agent")
	configureP4Capability(t, agentID)

	w := triggerP4AssessmentRequest(t, bindingID, false)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["created"] != true {
		t.Fatalf("expected created=true, got %v", resp)
	}
	taskID, _ := resp["task_id"].(string)
	if taskID == "" {
		t.Fatalf("expected task_id in trigger response, got %v", resp)
	}

	projIssueID := loadAssessmentIssueID(t, bindingID)
	if projIssueID == "" {
		t.Fatalf("trigger must create a projection issue")
	}

	// The projection issue is assigned to the capability agent.
	var assigneeType, assigneeID *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT assignee_type, assignee_id::text FROM issue WHERE id = $1`, projIssueID,
	).Scan(&assigneeType, &assigneeID); err != nil {
		t.Fatalf("load projection issue assignee: %v", err)
	}
	if assigneeType == nil || *assigneeType != "agent" || assigneeID == nil || *assigneeID != agentID {
		t.Fatalf("projection issue assignee = %v/%v, want agent/%s", assigneeType, assigneeID, agentID)
	}

	// The native task hangs on the PROJECTION issue and carries the legacy
	// per-binding context contract.
	var taskAgentID, taskIssueID, taskStatus string
	var handoffNote *string
	var forceFresh bool
	var contextJSON []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT agent_id::text, issue_id::text, status, handoff_note, force_fresh_session, context
		FROM agent_task_queue WHERE id = $1`, taskID,
	).Scan(&taskAgentID, &taskIssueID, &taskStatus, &handoffNote, &forceFresh, &contextJSON); err != nil {
		t.Fatalf("load native task: %v", err)
	}
	if taskAgentID != agentID {
		t.Fatalf("task agent = %s, want capability agent %s", taskAgentID, agentID)
	}
	if taskIssueID != projIssueID {
		t.Fatalf("task issue = %s, want projection issue %s", taskIssueID, projIssueID)
	}
	if taskStatus != "queued" || !forceFresh {
		t.Fatalf("task shape = %s/force_fresh=%v, want queued/true", taskStatus, forceFresh)
	}
	if handoffNote == nil || *handoffNote == "" {
		t.Fatalf("native task must carry the stale-daemon steering handoff_note")
	}
	var taskCtx map[string]string
	if err := json.Unmarshal(contextJSON, &taskCtx); err != nil {
		t.Fatalf("decode task context: %v", err)
	}
	if taskCtx["type"] != service.P4AssessmentTaskType ||
		taskCtx["workspace_id"] != testWorkspaceID ||
		taskCtx["issue_id"] != realIssueID ||
		taskCtx["feishu_binding_id"] != bindingID ||
		taskCtx["mode"] != "assess_only" {
		t.Fatalf("task context = %v, want legacy per-binding contract (real issue %s, binding %s)", taskCtx, realIssueID, bindingID)
	}

	// The queue row is keyed on the task so start/complete/fail projections
	// and the result-submit authorization all resolve.
	var rowTaskID *string
	var rowStatus string
	if err := testPool.QueryRow(context.Background(),
		`SELECT assessment_task_id::text, assessment_status FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`,
		bindingID,
	).Scan(&rowTaskID, &rowStatus); err != nil {
		t.Fatalf("load assessment row: %v", err)
	}
	if rowTaskID == nil || *rowTaskID != taskID || rowStatus != "pending" {
		t.Fatalf("assessment row = task %v status %s, want task %s status pending", rowTaskID, rowStatus, taskID)
	}
}

// Single-direction status projection (plan C-1): task running → row running +
// issue in_progress; terminal task failure → row failed + issue cancelled +
// failure comment. No reverse path.
func TestNativeTaskLifecycleProjectsAssessmentStatus(t *testing.T) {
	bindingID, _ := setupP4BindingFixture(t)
	agentID, _ := createLocalRuntimeAgent(t, "P4 Lifecycle Capability Agent")
	configureP4Capability(t, agentID)

	if w := triggerP4AssessmentRequest(t, bindingID, false); w.Code != http.StatusOK {
		t.Fatalf("trigger: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	ctx := context.Background()
	var taskID string
	if err := testPool.QueryRow(ctx,
		`SELECT assessment_task_id::text FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`, bindingID,
	).Scan(&taskID); err != nil {
		t.Fatalf("load assessment task id: %v", err)
	}
	projIssueID := loadAssessmentIssueID(t, bindingID)

	// Task start (daemon StartTask path) projects running / in_progress.
	if _, err := testPool.Exec(ctx,
		`UPDATE agent_task_queue SET status = 'dispatched', dispatched_at = now() WHERE id = $1`, taskID,
	); err != nil {
		t.Fatalf("dispatch task: %v", err)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(taskID)); err != nil {
		t.Fatalf("start task: %v", err)
	}
	var rowStatus string
	var attempts int
	if err := testPool.QueryRow(ctx,
		`SELECT assessment_status, attempt_count FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`, bindingID,
	).Scan(&rowStatus, &attempts); err != nil {
		t.Fatalf("load row after start: %v", err)
	}
	if rowStatus != "running" || attempts != 1 {
		t.Fatalf("after start: row = %s attempts=%d, want running attempts=1", rowStatus, attempts)
	}
	assertIssueStatus(t, projIssueID, "in_progress")

	// Terminal task failure projects failed / cancelled + failure comment.
	if _, err := testHandler.TaskService.FailTask(ctx, parseUUID(taskID), "daemon exploded", "", "", "timeout"); err != nil {
		t.Fatalf("fail task: %v", err)
	}
	var lastError string
	if err := testPool.QueryRow(ctx,
		`SELECT assessment_status, last_error FROM agent_fix_p4_assessment WHERE feishu_binding_id = $1`, bindingID,
	).Scan(&rowStatus, &lastError); err != nil {
		t.Fatalf("load row after fail: %v", err)
	}
	if rowStatus != "failed" || lastError == "" {
		t.Fatalf("after fail: row = %s last_error=%q, want failed with a recorded reason", rowStatus, lastError)
	}
	assertIssueStatus(t, projIssueID, "cancelled")
	var comments int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM comment WHERE issue_id = $1 AND author_type = 'agent'`, projIssueID,
	).Scan(&comments); err != nil {
		t.Fatalf("count failure comments: %v", err)
	}
	if comments != 1 {
		t.Fatalf("expected 1 server failure comment on the projection issue, got %d", comments)
	}
}
