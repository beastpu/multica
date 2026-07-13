package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestDeriveAgentFixEval locks the AI-vs-human accuracy scoring. The earlier
// switch only treated likely_correct+accepted as a match and dumped every other
// combination — including the genuinely-accurate likely_needs_changes+needs_changes
// and likely_wrong+rejected — into an untranslated "mismatch" bucket, inflating
// the deviation metric the feature exists to produce. This covers the full
// 3x3 quality/outcome grid plus the pending / not_comparable downgrades.
func TestDeriveAgentFixEval(t *testing.T) {
	p4 := func(quality string) *AgentFixP4AssessmentResponse {
		return &AgentFixP4AssessmentResponse{QualityPrediction: quality}
	}
	review := func(outcome string) *AgentFixHumanReviewResponse {
		return &AgentFixHumanReviewResponse{Outcome: outcome}
	}

	cases := []struct {
		name   string
		p4     *AgentFixP4AssessmentResponse
		review *AgentFixHumanReviewResponse
		want   string
	}{
		// No review yet → pending.
		{"no review", p4("likely_correct"), nil, "pending"},
		{"empty outcome", p4("likely_correct"), review(""), "pending"},
		{"unreviewed", p4("likely_correct"), review("unreviewed"), "pending"},
		// Not comparable: not_applicable, missing assessment, or unknown quality.
		{"not applicable", p4("likely_correct"), review("not_applicable"), "not_comparable"},
		{"nil assessment", nil, review("accepted"), "not_comparable"},
		{"unknown quality", p4("unknown"), review("accepted"), "not_comparable"},
		{"empty quality", p4(""), review("accepted"), "not_comparable"},
		// Diagonal = accurate prediction.
		{"correct+accepted", p4("likely_correct"), review("accepted"), "match"},
		{"needs+needs", p4("likely_needs_changes"), review("needs_changes"), "match"},
		{"wrong+rejected", p4("likely_wrong"), review("rejected"), "match"},
		// AI too optimistic (predicted better than the verdict) → overestimated.
		{"correct+needs", p4("likely_correct"), review("needs_changes"), "overestimated"},
		{"correct+rejected", p4("likely_correct"), review("rejected"), "overestimated"},
		{"needs+rejected", p4("likely_needs_changes"), review("rejected"), "overestimated"},
		// AI too pessimistic (predicted worse than the verdict) → underestimated.
		{"needs+accepted", p4("likely_needs_changes"), review("accepted"), "underestimated"},
		{"wrong+accepted", p4("likely_wrong"), review("accepted"), "underestimated"},
		{"wrong+needs", p4("likely_wrong"), review("needs_changes"), "underestimated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveAgentFixEval(tc.p4, tc.review); got != tc.want {
				t.Errorf("deriveAgentFixEval = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestListWorkspaceAgentTaskSnapshot covers the agent presence snapshot endpoint:
// every active task (queued/dispatched/running) PLUS each agent's most recent
// OUTCOME task (completed/failed only). Cancelled tasks are excluded by design
// from the outcome half — they're a procedural signal, not an outcome, and
// must NOT mask a prior failure.
//
// The fixtures cover every branch the SQL must classify:
//   - actives are always returned, no dedup
//   - outcomes are deduped to "latest per agent" by completed_at
//   - the OLD 2-minute window must be irrelevant (a 5-minute-old failure is
//     still returned if it's the latest outcome)
//   - cancelled rows are NEVER returned, even when they are temporally newer
//     than a failure — this is what keeps the failed signal sticky after the
//     user cancels their queued retry
func TestListWorkspaceAgentTaskSnapshot(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	// Three agents so we can verify per-agent semantics independently.
	agentA := createHandlerTestAgent(t, "snapshot-agent-a", []byte(`{}`))
	agentB := createHandlerTestAgent(t, "snapshot-agent-b", []byte(`{}`))
	agentC := createHandlerTestAgent(t, "snapshot-agent-c", []byte(`{}`))

	type taskFixture struct {
		agentID     string
		status      string
		completedAt string // SQL expression; "" for NULL
		label       string
	}
	fixtures := []taskFixture{
		// Agent A — actives + a newer completed supersedes an older failed.
		{agentA, "queued", "", "A.queued"},
		{agentA, "dispatched", "", "A.dispatched"},
		{agentA, "running", "", "A.running"},
		{agentA, "failed", "now() - interval '10 minutes'", "A.old_failed"},
		{agentA, "completed", "now() - interval '30 seconds'", "A.latest_completed"},

		// Agent B — old failure with no later outcome stays visible (no
		// time window).
		{agentB, "failed", "now() - interval '5 minutes'", "B.stale_failed_kept"},

		// Agent C — failure followed by a NEWER cancelled. The cancelled
		// must be skipped by the SQL filter so the failure remains visible.
		// This is the scenario where a user fails, then cancels their
		// queued retry to debug.
		{agentC, "failed", "now() - interval '5 minutes'", "C.failure"},
		{agentC, "cancelled", "now() - interval '30 seconds'", "C.newer_cancelled_must_be_ignored"},
	}

	insertedIDs := make([]string, 0, len(fixtures))
	for _, f := range fixtures {
		var id string
		var query string
		if f.completedAt == "" {
			query = `INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority)
			         VALUES ($1, $2, $3, 0) RETURNING id`
		} else {
			query = `INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, completed_at)
			         VALUES ($1, $2, $3, 0, ` + f.completedAt + `) RETURNING id`
		}
		if err := testPool.QueryRow(ctx, query, f.agentID, testRuntimeID, f.status).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", f.label, err)
		}
		insertedIDs = append(insertedIDs, id)
	}
	t.Cleanup(func() {
		for _, id := range insertedIDs {
			testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, id)
		}
	})

	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/agent-task-snapshot", nil)
	testHandler.ListWorkspaceAgentTaskSnapshot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListWorkspaceAgentTaskSnapshot: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var tasks []AgentTaskResponse
	if err := json.NewDecoder(w.Body).Decode(&tasks); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Per-agent breakdown so leftover tasks from other tests in this package
	// don't pollute the assertions.
	type key struct{ agent, status string }
	counts := map[key]int{}
	for _, task := range tasks {
		if task.AgentID != agentA && task.AgentID != agentB && task.AgentID != agentC {
			continue
		}
		counts[key{task.AgentID, task.Status}]++
	}

	wantCounts := map[key]int{
		// Agent A: 3 actives + the latest outcome (completed). The older
		// failed must be excluded by DISTINCT ON.
		{agentA, "queued"}:     1,
		{agentA, "dispatched"}: 1,
		{agentA, "running"}:    1,
		{agentA, "completed"}:  1,
		// Agent B: just the failed outcome.
		{agentB, "failed"}: 1,
		// Agent C: the failed outcome must survive the temporally newer
		// cancellation — that's the whole point of excluding cancelled
		// from the outcome half.
		{agentC, "failed"}: 1,
	}
	for k, expected := range wantCounts {
		if got := counts[k]; got != expected {
			t.Errorf("agent=%s status=%s: expected %d, got %d", k.agent, k.status, expected, got)
		}
	}

	// The OLD failed terminal on agent A must be excluded.
	if counts[key{agentA, "failed"}] != 0 {
		t.Errorf("agent A old failed must be superseded by newer completed; got %d", counts[key{agentA, "failed"}])
	}

	// No cancelled row may ever appear in the snapshot — they're filtered at
	// SQL level so the front-end's "cancel doesn't mask failure" rule lands
	// without any front-end logic.
	for _, agentID := range []string{agentA, agentB, agentC} {
		if counts[key{agentID, "cancelled"}] != 0 {
			t.Errorf("agent %s: cancelled rows must be excluded from snapshot; got %d",
				agentID, counts[key{agentID, "cancelled"}])
		}
	}
}

// TestListWorkspaceAgentFixes covers the Operations feed. Its contract: one
// row per recent normal Agent run or external-done binding, including bindings
// without an Agent assignment. The
// fixtures exercise the branches the SQL/handler must get right:
//   - an issue with two runs collapses to ONE row, using the LATEST run
//   - the "status" column is the issue's workflow status (not the task status)
//   - the "last_comment" column is the issue's most recent comment
//   - a task with NO linked issue is excluded (INNER JOIN issue)
func TestListWorkspaceAgentFixes(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "fixes-agent", []byte(`{}`))
	// Operations is a workspace-wide dashboard, so a regular member must see
	// rows produced by a private Agent they do not own. Agent visibility only
	// governs Agent access/invocation; it must not filter aggregate operations
	// reporting.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET visibility = 'private', permission_mode = 'private'
		WHERE id = $1
	`, agentID); err != nil {
		t.Fatalf("make operations fixture agent private: %v", err)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM agent_invocation_target WHERE agent_id = $1`, agentID); err != nil {
		t.Fatalf("remove private agent invocation targets: %v", err)
	}
	viewerID := createPermissionTestMember(t, "operations-private-agent-viewer@example.test")

	// issue.number is UNIQUE (workspace_id, number); allocate MAX+1 per row
	// so we don't collide with rows other tests left in the shared fixture
	// workspace.
	mkIssue := func(title, status string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number)
			VALUES ($1, $2, $3, 'medium', $4, 'member',
				(SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1))
			RETURNING id
		`, testWorkspaceID, title, status, testUserID).Scan(&id); err != nil {
			t.Fatalf("insert issue %q: %v", title, err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, id) })
		return id
	}
	doneIssue := mkIssue("Fix the login bug", "done")
	reviewIssue := mkIssue("Refactor the parser", "in_review")
	unassignedIssue := mkIssue("External done without Agent", "done")
	if _, err := testPool.Exec(ctx, `
		UPDATE issue
		SET assignee_type = 'agent', assignee_id = $2
		WHERE id = $1
	`, reviewIssue, agentID); err != nil {
		t.Fatalf("assign review issue to agent: %v", err)
	}

	mkTask := func(query string, args ...any) string {
		var id string
		if err := testPool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatalf("insert task: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, id) })
		return id
	}

	// doneIssue: an OLDER run plus a NEWER run — the feed must keep only the
	// newer one (one row per issue).
	mkTask(`
		INSERT INTO agent_task_queue (agent_id, issue_id, runtime_id, status, priority, completed_at)
		VALUES ($1, $2, $3, 'failed', 0, now() - interval '3 hours')
		RETURNING id
	`, agentID, doneIssue, testRuntimeID)
	newerDoneTaskID := mkTask(`
		INSERT INTO agent_task_queue (agent_id, issue_id, runtime_id, status, priority, completed_at)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '1 hour')
		RETURNING id
	`, agentID, doneIssue, testRuntimeID)

	mkTask(`
		INSERT INTO agent_task_queue (agent_id, issue_id, runtime_id, status, priority, completed_at)
		VALUES ($1, $2, $3, 'completed', 0, now() - interval '30 minutes')
		RETURNING id
	`, agentID, reviewIssue, testRuntimeID)

	// reviewIssue comments — the "原因/描述" column tracks the AGENT's latest
	// comment, not the issue's. Seed an agent comment (the closing action) and
	// then a NEWER member reply: the feed must surface the agent comment, the
	// member "收到" must NOT mask it.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content, type, created_at)
		VALUES ($1, $2, 'agent', $3, 'looks good, ready for review', 'comment', now() - interval '10 minutes')
	`, testWorkspaceID, reviewIssue, agentID); err != nil {
		t.Fatalf("insert agent comment: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content, type, created_at)
		VALUES ($1, $2, 'member', $3, '收到，辛苦了', 'comment', now() - interval '1 minute')
	`, testWorkspaceID, reviewIssue, testUserID); err != nil {
		t.Fatalf("insert member comment: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, reviewIssue) })

	var integrationID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO feishu_project_integration (
			workspace_id,
			project_key,
			plugin_id,
			plugin_secret,
			status_mapping,
			work_item_types,
			created_by_id
		)
		VALUES (
			$1,
			'OperationsFixture',
			'plugin-demo',
			'secret-demo',
			'{"Done":"done"}'::jsonb,
			'[{"type_key":"issue","api_name":"issue","name":"Defect","identifier_prefix":"BUG","status_mapping":{"Done":"done"},"reverse_status_mapping":{}}]'::jsonb,
			$2
		)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&integrationID); err != nil {
		t.Fatalf("insert feishu integration: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM feishu_project_integration WHERE id = $1`, integrationID) })

	var bindingID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO feishu_project_issue_binding (
			workspace_id,
			integration_id,
			issue_id,
			project_key,
			work_item_type,
			work_item_id,
			external_identifier,
			external_url,
			external_status_label,
			external_fields
		)
		VALUES (
			$1,
			$2,
			$3,
			'OperationsFixture',
			'issue',
			'BUG-93218',
			'BUG-93218',
			'https://meego.example.test/BUG-93218',
			'Done',
			'{"version":"1.7.2"}'::jsonb
		)
		RETURNING id
	`, testWorkspaceID, integrationID, reviewIssue).Scan(&bindingID); err != nil {
		t.Fatalf("insert feishu binding: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM feishu_project_issue_binding WHERE id = $1`, bindingID) })

	var unassignedBindingID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO feishu_project_issue_binding (
			workspace_id, integration_id, issue_id, project_key, work_item_type,
			work_item_id, external_identifier, external_status_label
		)
		VALUES ($1, $2, $3, 'OperationsFixture', 'issue', 'BUG-93219', 'BUG-93219', 'Done')
		RETURNING id
	`, testWorkspaceID, integrationID, unassignedIssue).Scan(&unassignedBindingID); err != nil {
		t.Fatalf("insert unassigned feishu binding: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM feishu_project_issue_binding WHERE id = $1`, unassignedBindingID)
	})

	var p4AssessmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_fix_p4_assessment (
			workspace_id,
			issue_id,
			feishu_binding_id,
			assessment_status,
			delivery_attribution_prediction,
			quality_prediction,
			prediction_reasons,
			confidence,
			workstream,
			swarm_reviews,
			ai_shelved_cls,
			external_committed_cls,
			summary,
			warnings,
			assessed_at
		)
		VALUES (
			$1,
			$2,
			$3,
			'completed',
			'ai_delivered',
			'likely_correct',
			ARRAY['complete_usable']::text[],
			0.86,
			'rel_1.7.2/server',
			'[{"review_id":"SW-11872","changes":[282941],"commits":[283006],"swarm_branch":"main","event_type":"review.committed","sent_at":"2026-06-01T00:30:00Z"}]'::jsonb,
			ARRAY[282941]::int[],
			ARRAY[283006]::int[],
			'AI shelve was submitted as the final CL.',
			'[]'::jsonb,
			now()
		)
		RETURNING id
	`, testWorkspaceID, reviewIssue, bindingID).Scan(&p4AssessmentID); err != nil {
		t.Fatalf("insert p4 assessment: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_fix_p4_assessment WHERE id = $1`, p4AssessmentID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_fix_review (
			workspace_id,
			issue_id,
			feishu_binding_id,
			p4_assessment_id,
			outcome,
			reasons,
			note,
			reviewer_id,
			reviewed_at
		)
		VALUES (
			$1,
			$2,
			$3,
			$4,
			'accepted',
			ARRAY['complete_usable']::text[],
			'Reviewed against the same binding as the P4 assessment.',
			$5,
			now()
		)
	`, testWorkspaceID, reviewIssue, bindingID, p4AssessmentID, testUserID); err != nil {
		t.Fatalf("insert human review: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_fix_review WHERE feishu_binding_id = $1`, bindingID) })

	// A hard-failed latest run: the feed must carry the run's status and the
	// structured failure_reason so the dashboard can explain no-output rows.
	blockedIssue := mkIssue("Blocked by provider auth", "in_progress")
	mkTask(`
		INSERT INTO agent_task_queue (agent_id, issue_id, runtime_id, status, priority, completed_at, failure_reason)
		VALUES ($1, $2, $3, 'failed', 0, now() - interval '5 minutes', 'agent_error.provider_auth_or_access')
		RETURNING id
	`, agentID, blockedIssue, testRuntimeID)

	// No issue_id — must be excluded by the INNER JOIN on issue.
	issuelessTaskID := mkTask(`
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, completed_at)
		VALUES ($1, $2, 'completed', 0, now())
		RETURNING id
	`, agentID, testRuntimeID)

	w := httptest.NewRecorder()
	req := newRequest(http.MethodGet, "/api/operations/agent-fixes", nil)
	req.Header.Set("X-User-ID", viewerID)
	testHandler.ListWorkspaceAgentFixes(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ListWorkspaceAgentFixes: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var fixes []AgentFixResponse
	if err := json.NewDecoder(w.Body).Decode(&fixes); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	byIssue := map[string]AgentFixResponse{}
	rowsForDoneIssue := 0
	for _, f := range fixes {
		byIssue[f.IssueID] = f
		if f.IssueID == doneIssue {
			rowsForDoneIssue++
		}
	}

	done, ok := byIssue[doneIssue]
	if !ok {
		t.Fatalf("doneIssue fix not returned; got %d rows", len(fixes))
	}
	// One row per issue, using the LATEST run.
	if rowsForDoneIssue != 1 {
		t.Errorf("doneIssue should collapse to 1 row, got %d", rowsForDoneIssue)
	}
	if done.TaskID != newerDoneTaskID {
		t.Errorf("doneIssue row should carry the latest run %s, got %s", newerDoneTaskID, done.TaskID)
	}
	if done.AgentName != "fixes-agent" {
		t.Errorf("done.AgentName = %q, want fixes-agent", done.AgentName)
	}
	// The latest task's agent is not the issue's current assignee. This issue
	// has task history but is currently unassigned, so the two signals must stay
	// distinct for the Operations denominator.
	if done.IssueAssigneeType != "" || done.IssueAssigneeID != "" {
		t.Errorf("done current assignee = %q/%q, want unassigned", done.IssueAssigneeType, done.IssueAssigneeID)
	}
	if done.IssueTitle != "Fix the login bug" {
		t.Errorf("done.IssueTitle = %q", done.IssueTitle)
	}
	// "status" is the ISSUE workflow status, not the task status.
	if done.IssueStatus != "done" {
		t.Errorf("done.IssueStatus = %q, want done", done.IssueStatus)
	}
	if !strings.Contains(done.IssueIdentifier, "-") {
		t.Errorf("done.IssueIdentifier = %q, want PREFIX-N form", done.IssueIdentifier)
	}

	review, ok := byIssue[reviewIssue]
	if !ok {
		t.Fatalf("reviewIssue fix not returned")
	}
	if review.IssueStatus != "in_review" {
		t.Errorf("review.IssueStatus = %q, want in_review", review.IssueStatus)
	}
	if review.IssueAssigneeType != "agent" || review.IssueAssigneeID != agentID {
		t.Errorf("review current assignee = %q/%q, want agent/%s", review.IssueAssigneeType, review.IssueAssigneeID, agentID)
	}
	// The agent's comment, not the newer member "收到" — the column tracks the
	// agent's own closing action.
	if review.LastComment != "looks good, ready for review" {
		t.Errorf("review.LastComment = %q, want the agent comment (member reply must not mask it)", review.LastComment)
	}
	if review.LastCommentAuthorType != "agent" {
		t.Errorf("review.LastCommentAuthorType = %q, want agent", review.LastCommentAuthorType)
	}
	// Every agent comment on the issue counts (member replies don't), so the
	// dashboard can tell "commented a plan" from "did nothing" without relying
	// on the single last_comment snippet.
	if review.AgentCommentCount != 1 {
		t.Errorf("review.AgentCommentCount = %d, want 1", review.AgentCommentCount)
	}
	if done.AgentCommentCount != 0 {
		t.Errorf("done.AgentCommentCount = %d, want 0", done.AgentCommentCount)
	}
	// The latest run's status and structured failure reason ride along so the
	// dashboard can explain a no-output ticket (e.g. provider auth failure).
	if done.TaskStatus != "completed" {
		t.Errorf("done.TaskStatus = %q, want completed", done.TaskStatus)
	}
	blocked, ok := byIssue[blockedIssue]
	if !ok {
		t.Fatalf("blockedIssue fix not returned")
	}
	if blocked.TaskStatus != "failed" {
		t.Errorf("blocked.TaskStatus = %q, want failed", blocked.TaskStatus)
	}
	if blocked.TaskFailureReason != "agent_error.provider_auth_or_access" {
		t.Errorf("blocked.TaskFailureReason = %q, want agent_error.provider_auth_or_access", blocked.TaskFailureReason)
	}
	if review.External == nil {
		t.Fatalf("review.External = nil, want Feishu binding data")
	}
	if review.External.BindingID != bindingID {
		t.Errorf("review.External.BindingID = %q, want %q", review.External.BindingID, bindingID)
	}
	if review.External.WorkItemID != "BUG-93218" {
		t.Errorf("review.External.WorkItemID = %q, want BUG-93218", review.External.WorkItemID)
	}
	if review.External.MappedStatus != "done" || !review.External.Done {
		t.Errorf("review.External mapped status = %q done=%v, want done/true", review.External.MappedStatus, review.External.Done)
	}
	if review.P4Assessment == nil {
		t.Fatalf("review.P4Assessment = nil, want assessment joined by binding")
	}
	if review.P4Assessment.Workstream != "rel_1.7.2/server" {
		t.Errorf("review.P4Assessment.Workstream = %q", review.P4Assessment.Workstream)
	}
	if review.P4Assessment.DeliveryAttributionPrediction != "ai_delivered" {
		t.Errorf("review.P4Assessment.DeliveryAttributionPrediction = %q", review.P4Assessment.DeliveryAttributionPrediction)
	}
	if review.HumanReview == nil {
		t.Fatalf("review.HumanReview = nil, want human review joined by same binding")
	}
	if review.HumanReview.Outcome != "accepted" {
		t.Errorf("review.HumanReview.Outcome = %q, want accepted", review.HumanReview.Outcome)
	}
	if !reflect.DeepEqual(review.HumanReview.Reasons, []string{"complete_usable"}) {
		t.Errorf("review.HumanReview.Reasons = %#v", review.HumanReview.Reasons)
	}
	if review.AIJudgementEval != "match" {
		t.Errorf("review.AIJudgementEval = %q, want match", review.AIJudgementEval)
	}
	unassigned, ok := byIssue[unassignedIssue]
	if !ok {
		t.Fatalf("external-done issue without Agent not returned")
	}
	if unassigned.TaskID != "" || unassigned.AgentID != "" || unassigned.AgentName != "" {
		t.Errorf("unassigned row task/agent = %q/%q/%q, want all empty", unassigned.TaskID, unassigned.AgentID, unassigned.AgentName)
	}
	if unassigned.External == nil || !unassigned.External.Done {
		t.Errorf("unassigned.External = %#v, want external done binding", unassigned.External)
	}

	for _, f := range fixes {
		if f.TaskID == issuelessTaskID {
			t.Errorf("task with no linked issue must be excluded from the fix feed")
		}
	}
}

func TestListWorkspaceAgentFixesRejectsNonMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	var outsiderID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Operations outsider', 'operations-outsider@example.test')
		RETURNING id
	`).Scan(&outsiderID); err != nil {
		t.Fatalf("create operations outsider: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, outsiderID) })

	req := newRequest(http.MethodGet, "/api/operations/agent-fixes", nil)
	req.Header.Set("X-User-ID", outsiderID)
	w := httptest.NewRecorder()
	testHandler.ListWorkspaceAgentFixes(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("non-member status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// TestListWorkspaceAgentFixes_Search covers the ?search= filter on the
// Operations feed: only issues whose AGENT comment contains the term (case-
// insensitive) come back, an issue whose agent comment lacks the term is
// dropped, and a no-comment issue never matches. The returned snippet is
// centered on the match so the keyword is visible.
func TestListWorkspaceAgentFixes_Search(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "search-agent", []byte(`{}`))

	mkIssue := func(title string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number)
			VALUES ($1, $2, 'in_progress', 'medium', $3, 'member',
				(SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1))
			RETURNING id
		`, testWorkspaceID, title, testUserID).Scan(&id); err != nil {
			t.Fatalf("insert issue %q: %v", title, err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, id) })
		return id
	}
	mkRun := func(issueID string) {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_task_queue (agent_id, issue_id, runtime_id, status, priority, completed_at)
			VALUES ($1, $2, $3, 'completed', 0, now() - interval '20 minutes')
			RETURNING id
		`, agentID, issueID, testRuntimeID).Scan(&id); err != nil {
			t.Fatalf("insert run: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, id) })
	}
	mkAgentComment := func(issueID, content string) {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO comment (workspace_id, issue_id, author_type, author_id, content, type)
			VALUES ($1, $2, 'agent', $3, $4, 'comment')
		`, testWorkspaceID, issueID, agentID, content); err != nil {
			t.Fatalf("insert agent comment: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, issueID) })
	}

	// A long comment so the matched keyword sits past the leading snippet — the
	// match-centered snippet must still surface it. The keyword is mixed-case in
	// the content to prove the search is case-insensitive.
	matchIssue := mkIssue("Refactor the parser")
	mkRun(matchIssue)
	longBody := strings.Repeat("分析了一遍代码并做了大量改动，", 12) + "最终通过 Code Review 后合并。"
	mkAgentComment(matchIssue, longBody)

	// Agent commented, but the term isn't there — must be dropped by search.
	noMatchIssue := mkIssue("Tune the cache")
	mkRun(noMatchIssue)
	mkAgentComment(noMatchIssue, "just started looking into it")

	// No agent comment at all — must never match a search.
	silentIssue := mkIssue("Investigate flake")
	mkRun(silentIssue)

	get := func(query string) map[string]AgentFixResponse {
		w := httptest.NewRecorder()
		testHandler.ListWorkspaceAgentFixes(w, newRequest(http.MethodGet, "/api/operations/agent-fixes"+query, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("ListWorkspaceAgentFixes%s: expected 200, got %d: %s", query, w.Code, w.Body.String())
		}
		var fixes []AgentFixResponse
		if err := json.NewDecoder(w.Body).Decode(&fixes); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		byIssue := map[string]AgentFixResponse{}
		for _, f := range fixes {
			byIssue[f.IssueID] = f
		}
		return byIssue
	}

	// Case-insensitive match: search "review" finds the "Code Review" comment.
	got := get("?search=review")
	match, ok := got[matchIssue]
	if !ok {
		t.Fatalf("search=review should return the matching issue")
	}
	if !strings.Contains(match.LastComment, "Review") {
		t.Errorf("snippet should be centered on the match and contain %q; got %q", "Review", match.LastComment)
	}
	if _, ok := got[noMatchIssue]; ok {
		t.Errorf("issue whose agent comment lacks the term must be dropped by search")
	}
	if _, ok := got[silentIssue]; ok {
		t.Errorf("issue with no agent comment must never match a search")
	}

	// No-results term returns none of our seeded issues.
	none := get("?search=zzz-no-such-term")
	for _, id := range []string{matchIssue, noMatchIssue, silentIssue} {
		if _, ok := none[id]; ok {
			t.Errorf("non-matching search must return no rows; issue %s leaked", id)
		}
	}

	// No search arg: the agent-commented issues come back (silent one too — it
	// has a run), proving the filter is truly optional.
	all := get("")
	if _, ok := all[matchIssue]; !ok {
		t.Errorf("no-search query should include the matching issue")
	}
	if _, ok := all[noMatchIssue]; !ok {
		t.Errorf("no-search query should include the non-matching-comment issue")
	}
}

// TestCommentSnippetAround is a pure unit test (no DB) for the match-centering
// excerpt used by the "原因/描述" column when a search term is active.
func TestCommentSnippetAround(t *testing.T) {
	t.Run("empty keyword falls back to leading snippet", func(t *testing.T) {
		long := strings.Repeat("a", 300)
		got := commentSnippetAround(long, "")
		want := strings.Repeat("a", commentSnippetMaxRunes) + "…"
		if got != want {
			t.Errorf("empty keyword should give the leading snippet; got len %d", len([]rune(got)))
		}
	})

	t.Run("keyword far into a long body is centered with ellipses", func(t *testing.T) {
		body := strings.Repeat("x", 100) + "REVIEW" + strings.Repeat("y", 100)
		got := commentSnippetAround(body, "review")
		if !strings.Contains(got, "REVIEW") {
			t.Errorf("centered snippet must contain the match; got %q", got)
		}
		if !strings.HasPrefix(got, "…") {
			t.Errorf("elided lead should be marked with an ellipsis; got %q", got)
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("elided tail should be marked with an ellipsis; got %q", got)
		}
	})

	t.Run("keyword near the start has no leading ellipsis", func(t *testing.T) {
		body := "REVIEW done, " + strings.Repeat("z", 200)
		got := commentSnippetAround(body, "review")
		if strings.HasPrefix(got, "…") {
			t.Errorf("a match at the start should not get a leading ellipsis; got %q", got)
		}
		if !strings.Contains(got, "REVIEW") {
			t.Errorf("snippet must contain the match; got %q", got)
		}
	})

	t.Run("CJK content is cut on rune boundaries around the match", func(t *testing.T) {
		body := strings.Repeat("前面无关内容", 12) + "关键动作：审阅完成已合并" + strings.Repeat("后面内容", 12)
		got := commentSnippetAround(body, "审阅")
		if !strings.Contains(got, "审阅") {
			t.Errorf("CJK snippet must contain the match; got %q", got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("snippet must not cut a multi-byte rune; got invalid UTF-8 %q", got)
		}
	})

	t.Run("no match defensively falls back to leading snippet", func(t *testing.T) {
		got := commentSnippetAround("hello world", "absent")
		if got != "hello world" {
			t.Errorf("no-match should return the leading snippet; got %q", got)
		}
	})
}

func TestCreateAgent_RejectsDuplicateName(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Clean up any agents created by this test.
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE workspace_id = $1 AND name = $2`,
			testWorkspaceID, "duplicate-name-test-agent",
		)
	})

	body := map[string]any{
		"name":                 "duplicate-name-test-agent",
		"description":          "first description",
		"runtime_id":           testRuntimeID,
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}

	// First call — creates the agent.
	w1 := httptest.NewRecorder()
	testHandler.CreateAgent(w1, newRequest(http.MethodPost, "/api/agents", body))
	if w1.Code != http.StatusCreated {
		t.Fatalf("first CreateAgent: expected 201, got %d: %s", w1.Code, w1.Body.String())
	}
	var resp1 map[string]any
	if err := json.NewDecoder(w1.Body).Decode(&resp1); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	agentID1, _ := resp1["id"].(string)
	if agentID1 == "" {
		t.Fatalf("first CreateAgent: no id in response: %v", resp1)
	}

	// Second call — same name must be rejected with 409 Conflict.
	// The unique constraint prevents silent duplicates; the UI shows a clear error.
	body["description"] = "updated description"
	w2 := httptest.NewRecorder()
	testHandler.CreateAgent(w2, newRequest(http.MethodPost, "/api/agents", body))
	if w2.Code != http.StatusConflict {
		t.Fatalf("second CreateAgent with duplicate name: expected 409, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestWorkspaceAlwaysRedactSecrets(t *testing.T) {
	tests := []struct {
		name     string
		settings []byte
		want     bool
	}{
		{"nil settings", nil, false},
		{"empty settings", []byte(`{}`), false},
		{"false", []byte(`{"always_redact_env": false}`), false},
		{"true", []byte(`{"always_redact_env": true}`), true},
		{"invalid json", []byte(`not json`), false},
		{"other fields only", []byte(`{"theme": "dark"}`), false},
		{"true among other fields", []byte(`{"theme": "dark", "always_redact_env": true}`), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workspaceAlwaysRedactSecrets(tt.settings); got != tt.want {
				t.Errorf("workspaceAlwaysRedactSecrets(%q) = %v, want %v", tt.settings, got, tt.want)
			}
		})
	}
}

// rawJSONResponse decodes the raw map so we can assert the literal
// JSON shape — `custom_env` MUST be absent from the wire output, not
// merely empty, otherwise a future caller decoding into a wider struct
// could still see masked or partial values.
func rawJSONResponse(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return out
}

// TestGetAgent_ResponseHasNoCustomEnv guards the core invariant from
// MUL-2600: the generic agent resource response NEVER carries the
// custom_env field, even for the agent's owner. Only the dedicated
// env endpoint exposes secret values.
func TestGetAgent_ResponseHasNoCustomEnv(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "noenv-get-agent", nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET custom_env = '{"SECRET_KEY": "super-secret"}' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to set custom_env: %v", err)
	}

	req := newRequest("GET", "/agents/"+agentID, nil)
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.GetAgent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	raw := rawJSONResponse(t, w.Body.Bytes())
	if _, ok := raw["custom_env"]; ok {
		t.Errorf("custom_env field must not appear in agent response, got %v", raw["custom_env"])
	}
	if _, ok := raw["custom_env_redacted"]; ok {
		t.Errorf("custom_env_redacted field must not appear in agent response (use has_custom_env)")
	}
	if got, _ := raw["has_custom_env"].(bool); !got {
		t.Errorf("has_custom_env expected true, got %v", raw["has_custom_env"])
	}
	if got, _ := raw["custom_env_key_count"].(float64); got != 1 {
		t.Errorf("custom_env_key_count expected 1, got %v", raw["custom_env_key_count"])
	}

	// Sanity-check the typed shape too — the struct must not have
	// rehydrated the masked map.
	var typed AgentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &typed); err != nil {
		t.Fatalf("typed decode failed: %v", err)
	}
	if typed.HasCustomEnv != true {
		t.Errorf("typed.HasCustomEnv expected true")
	}
	if typed.CustomEnvKeyCount != 1 {
		t.Errorf("typed.CustomEnvKeyCount expected 1, got %d", typed.CustomEnvKeyCount)
	}
}

// TestListAgents_ResponseHasNoCustomEnv mirrors the GetAgent guard for
// the list endpoint. Same invariant: no custom_env field on the wire,
// only coarse metadata.
func TestListAgents_ResponseHasNoCustomEnv(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentName := "noenv-list-agent"
	agentID := createHandlerTestAgent(t, agentName, nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET custom_env = '{"SECRET_KEY": "super-secret", "OTHER": "y"}' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to set custom_env: %v", err)
	}

	req := newRequest("GET", "/agents", nil)
	w := httptest.NewRecorder()
	testHandler.ListAgents(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var rawAgents []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rawAgents); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	var found map[string]any
	for _, a := range rawAgents {
		if name, _ := a["name"].(string); name == agentName {
			found = a
			break
		}
	}
	if found == nil {
		t.Fatal("agent not found in list response")
	}
	if _, ok := found["custom_env"]; ok {
		t.Errorf("custom_env must not appear in list response")
	}
	if got, _ := found["custom_env_key_count"].(float64); got != 2 {
		t.Errorf("custom_env_key_count expected 2, got %v", found["custom_env_key_count"])
	}
	if got, _ := found["has_custom_env"].(bool); !got {
		t.Errorf("has_custom_env expected true")
	}
}

// TestGetAgentEnv_OwnerSucceedsAndAudits exercises the happy path: an
// agent owner reveals env, and the response carries the plaintext map.
// The activity_log row is checked at the end so the audit trail is
// proven to land in the same transaction window.
func TestGetAgentEnv_OwnerSucceedsAndAudits(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "env-reveal-owner-agent", nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET custom_env = '{"KEY_ONE": "v1", "KEY_TWO": "v2"}' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to set custom_env: %v", err)
	}

	req := newRequest("GET", "/api/agents/"+agentID+"/env", nil)
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.GetAgentEnv(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GetAgentEnv: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentEnvResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AgentID != agentID {
		t.Errorf("agent_id mismatch: got %q", resp.AgentID)
	}
	expected := map[string]string{"KEY_ONE": "v1", "KEY_TWO": "v2"}
	if !reflect.DeepEqual(resp.CustomEnv, expected) {
		t.Errorf("CustomEnv mismatch: got %v, want %v", resp.CustomEnv, expected)
	}

	// Audit row must exist; keys but not values must be recorded.
	var revealedKeysJSON string
	if err := testPool.QueryRow(ctx, `
		SELECT details::text FROM activity_log
		WHERE workspace_id = $1 AND action = 'agent_env_revealed'
		  AND details->>'agent_id' = $2
		ORDER BY created_at DESC LIMIT 1
	`, testWorkspaceID, agentID).Scan(&revealedKeysJSON); err != nil {
		t.Fatalf("no agent_env_revealed activity row found: %v", err)
	}
	if !strings.Contains(revealedKeysJSON, `"KEY_ONE"`) || !strings.Contains(revealedKeysJSON, `"KEY_TWO"`) {
		t.Errorf("expected revealed_keys to contain KEY_ONE and KEY_TWO, got: %s", revealedKeysJSON)
	}
	if strings.Contains(revealedKeysJSON, `"v1"`) || strings.Contains(revealedKeysJSON, `"v2"`) {
		t.Errorf("activity details must NOT contain env values, got: %s", revealedKeysJSON)
	}
}

// TestAgentEnv_AgentActorRejected proves the security-critical actor
// guard: even when the underlying user is a workspace owner, a request
// arriving from inside a running agent task is denied 403. This is
// the lateral-movement fix — an agent running with its owner's token
// cannot reveal a sibling agent's secrets.
func TestAgentEnv_AgentActorRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	targetID := createHandlerTestAgent(t, "env-target-agent", nil)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET custom_env = '{"K":"v"}' WHERE id = $1`, targetID); err != nil {
		t.Fatalf("failed to set custom_env: %v", err)
	}

	// Spin up a separate agent + task that authorises the X-Agent-ID /
	// X-Task-ID header pair resolveActor checks. The owning member of
	// the host agent is the same testUserID (workspace owner), which is
	// the exact lateral-movement shape we want to block.
	hostAgentID := createHandlerTestAgent(t, "env-host-agent", nil)
	hostTaskID := createHandlerTestTaskForAgent(t, hostAgentID)

	cases := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		body any
	}{
		{"reveal", testHandler.GetAgentEnv, nil},
		{"update", testHandler.UpdateAgentEnv, map[string]any{"custom_env": map[string]string{"K": "v2"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method := http.MethodGet
			if tc.body != nil {
				method = http.MethodPut
			}
			req := newRequest(method, "/api/agents/"+targetID+"/env", tc.body)
			req = withURLParam(req, "id", targetID)
			req.Header.Set("X-Agent-ID", hostAgentID)
			req.Header.Set("X-Task-ID", hostTaskID)
			w := httptest.NewRecorder()
			tc.fn(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 from agent actor, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestAgentEnv_TaskTokenActorSource locks in the post-MUL-2600 attack
// model: an agent process that strips its identifying headers
// (X-Agent-ID / X-Task-ID) but is still authenticated by an `mat_`
// task token MUST be recognized as actor=agent and rejected on the
// env endpoint. The auth middleware sets X-Actor-Source=task_token
// from the token row; resolveActor honors that header before the
// header-pair fallback. Without this guard the lateral-movement fix
// would only block "honest" CLIs that voluntarily set both headers.
func TestAgentEnv_TaskTokenActorSource(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	targetID := createHandlerTestAgent(t, "env-tt-target-agent", nil)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET custom_env = '{"K":"v"}' WHERE id = $1`, targetID); err != nil {
		t.Fatalf("failed to set custom_env: %v", err)
	}

	req := newRequest(http.MethodGet, "/api/agents/"+targetID+"/env", nil)
	req = withURLParam(req, "id", targetID)
	// Simulate the auth middleware's post-mat_-resolution state: the
	// only header touching actor identity is X-Actor-Source. The agent
	// process stripped X-Agent-ID and X-Task-ID, hoping to fall back
	// to the member auth path — the server-set X-Actor-Source must
	// short-circuit that escape.
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Del("X-Agent-ID")
	req.Header.Del("X-Task-ID")
	w := httptest.NewRecorder()
	testHandler.GetAgentEnv(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when X-Actor-Source=task_token, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateAgentEnv_PreservesSentinelValues verifies the **** guard.
// A naive write would clobber real secrets with the masked
// placeholder; we want any key whose value comes in as **** to keep
// its stored value.
func TestUpdateAgentEnv_PreservesSentinelValues(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "env-sentinel-agent", nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET custom_env = '{"KEEP_ME":"real-secret","ALSO":"another-secret"}' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to seed custom_env: %v", err)
	}

	// Client sends one key with a real new value, one with **** (should
	// be preserved), and one new key that isn't in the existing map but
	// arrives as **** (must be dropped, never written as literal).
	body := map[string]any{
		"custom_env": map[string]string{
			"KEEP_ME":   "****",
			"ALSO":      "rotated",
			"PHANTOM":   "****",
			"BRAND_NEW": "fresh",
		},
	}
	req := newRequest(http.MethodPut, "/api/agents/"+agentID+"/env", body)
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.UpdateAgentEnv(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgentEnv: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Refetch from DB so we don't rely on the response body alone.
	var stored string
	if err := testPool.QueryRow(ctx, `SELECT custom_env::text FROM agent WHERE id = $1`, agentID).Scan(&stored); err != nil {
		t.Fatalf("failed to read back custom_env: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(stored), &got); err != nil {
		t.Fatalf("failed to decode stored custom_env: %v", err)
	}
	want := map[string]string{
		"KEEP_ME":   "real-secret", // **** must preserve the existing value
		"ALSO":      "rotated",     // explicit overwrite
		"BRAND_NEW": "fresh",       // new addition
		// PHANTOM is intentionally absent — **** for a non-existent key
		// is dropped, never persisted as literal `****`.
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stored custom_env mismatch:\n got:  %v\n want: %v", got, want)
	}

	// Audit row should reflect the diff. We decode the jsonb back into a
	// typed map and compare semantically — postgres serializes jsonb with
	// canonicalised whitespace (`"added_keys": ["BRAND_NEW"]`), so a raw
	// substring match on the dense form silently fails on real database
	// output.
	var details string
	if err := testPool.QueryRow(ctx, `
		SELECT details::text FROM activity_log
		WHERE workspace_id = $1 AND action = 'agent_env_updated' AND details->>'agent_id' = $2
		ORDER BY created_at DESC LIMIT 1
	`, testWorkspaceID, agentID).Scan(&details); err != nil {
		t.Fatalf("expected agent_env_updated activity row: %v", err)
	}
	var auditFields struct {
		AddedKeys     []string `json:"added_keys"`
		ChangedKeys   []string `json:"changed_keys"`
		PreservedKeys []string `json:"preserved_keys"`
	}
	if err := json.Unmarshal([]byte(details), &auditFields); err != nil {
		t.Fatalf("failed to decode audit details: %v (raw=%s)", err, details)
	}
	if !reflect.DeepEqual(auditFields.AddedKeys, []string{"BRAND_NEW"}) {
		t.Errorf("added_keys: got %v, want [BRAND_NEW]; raw=%s", auditFields.AddedKeys, details)
	}
	if !reflect.DeepEqual(auditFields.ChangedKeys, []string{"ALSO"}) {
		t.Errorf("changed_keys: got %v, want [ALSO]; raw=%s", auditFields.ChangedKeys, details)
	}
	if !reflect.DeepEqual(auditFields.PreservedKeys, []string{"KEEP_ME"}) {
		t.Errorf("preserved_keys: got %v, want [KEEP_ME]; raw=%s", auditFields.PreservedKeys, details)
	}
	// Audit must never contain values.
	for _, leak := range []string{"real-secret", "another-secret", "rotated", "fresh"} {
		if strings.Contains(details, leak) {
			t.Errorf("audit details leaked value %q: %s", leak, details)
		}
	}
}

func TestUpdateAgent_RejectsCustomEnvInBody(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "update-no-env-agent", nil)
	if _, err := testPool.Exec(ctx, `UPDATE agent SET custom_env = '{"PRE":"existing"}' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to seed custom_env: %v", err)
	}

	// Sending custom_env via the generic PUT /api/agents/{id} must fail
	// loudly with a 400 — see the comment on the rejection in agent.go.
	// Silently dropping the field used to make scripted clients believe
	// they had rotated a secret when nothing actually happened.
	body := map[string]any{
		"description": "still updating description",
		"custom_env":  map[string]string{"INJECTED": "should-not-stick"},
	}
	req := newRequest(http.MethodPut, "/api/agents/"+agentID, body)
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateAgent: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "custom_env") || !strings.Contains(w.Body.String(), "/env") {
		t.Errorf("error body should mention custom_env and the env endpoint; got %s", w.Body.String())
	}

	// The stored env must be untouched by the rejected request.
	var stored string
	if err := testPool.QueryRow(ctx, `SELECT custom_env::text FROM agent WHERE id = $1`, agentID).Scan(&stored); err != nil {
		t.Fatalf("failed to read custom_env: %v", err)
	}
	if !strings.Contains(stored, `"PRE": "existing"`) && !strings.Contains(stored, `"PRE":"existing"`) {
		t.Errorf("UpdateAgent must NOT touch custom_env; got %q", stored)
	}
	if strings.Contains(stored, "INJECTED") {
		t.Errorf("UpdateAgent should have rejected custom_env in body; got %q", stored)
	}
}

// TestMergeAgentEnv_PureFunction exercises the diff/sentinel logic
// without the DB round-trip — keeps the contract front-and-centre in
// case someone refactors the handler later.
func TestMergeAgentEnv_PureFunction(t *testing.T) {
	cases := []struct {
		name     string
		existing map[string]string
		request  map[string]string
		want     map[string]string
		audit    envAudit
	}{
		{
			name:     "preserve sentinel",
			existing: map[string]string{"A": "real"},
			request:  map[string]string{"A": "****"},
			want:     map[string]string{"A": "real"},
			audit:    envAudit{preserved: []string{"A"}},
		},
		{
			name:     "drop sentinel for missing key",
			existing: map[string]string{},
			request:  map[string]string{"A": "****"},
			want:     map[string]string{},
			audit:    envAudit{},
		},
		{
			name:     "add new key",
			existing: map[string]string{},
			request:  map[string]string{"B": "v"},
			want:     map[string]string{"B": "v"},
			audit:    envAudit{added: []string{"B"}},
		},
		{
			name:     "change existing value",
			existing: map[string]string{"B": "old"},
			request:  map[string]string{"B": "new"},
			want:     map[string]string{"B": "new"},
			audit:    envAudit{changed: []string{"B"}},
		},
		{
			name:     "remove key absent from request",
			existing: map[string]string{"B": "v"},
			request:  map[string]string{},
			want:     map[string]string{},
			audit:    envAudit{removed: []string{"B"}},
		},
		{
			name:     "noop when value unchanged",
			existing: map[string]string{"B": "same"},
			request:  map[string]string{"B": "same"},
			want:     map[string]string{"B": "same"},
			audit:    envAudit{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, audit := mergeAgentEnv(tc.existing, tc.request)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("merged map: got %v, want %v", got, tc.want)
			}
			if !reflect.DeepEqual(audit, tc.audit) {
				t.Errorf("audit: got %+v, want %+v", audit, tc.audit)
			}
		})
	}
}

// Compile-time guard: AgentResponse must NOT carry the legacy env
// fields. Reintroducing them is a security regression — this test
// fails to compile rather than fails at runtime so reviewers see the
// breakage in the diff. Kept as a runtime test because the package
// boundary makes a struct-tag introspection cheap and obvious.
func TestAgentResponseShape_HasNoLegacyEnvFields(t *testing.T) {
	typ := reflect.TypeOf(AgentResponse{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		switch tag {
		case "custom_env", "custom_env_redacted", "custom_env_redacted_reason":
			t.Errorf("AgentResponse must not carry %q field (MUL-2600)", tag)
		}
	}
}

// TestUpdateAgent_RedactsMcpConfigForAgentActor closes the second leg
// of MUL-2600 review #2: an agent process with a task token (or with
// the X-Actor-Source server marker) must not be able to scrape another
// agent's mcp_config via an unrelated mutation response. Even when the
// host PAT would otherwise satisfy canManageAgent, the response body
// must come back with mcp_config redacted.
func TestUpdateAgent_RedactsMcpConfigForAgentActor(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// The target agent has a populated mcp_config that historically would
	// be leaked back via the UpdateAgent / ArchiveAgent / RestoreAgent
	// HTTP response.
	target := createHandlerTestAgent(t, "mut-mcp-target", []byte(`{"server":"secret-config"}`))

	// A second agent acts as the "calling" agent process whose task
	// token authenticated the request. It is registered in the same
	// workspace so resolveActor recognises X-Agent-ID as valid.
	caller := createHandlerTestAgent(t, "mut-mcp-caller", nil)
	taskID := insertHandlerTestTask(t, caller)

	desc := "trivial mutation that should NOT leak target mcp_config"
	req := newRequest(http.MethodPut, "/api/agents/"+target, map[string]any{
		"description": desc,
	})
	req = withURLParam(req, "id", target)
	// Simulate a task-token-authenticated agent request. The auth
	// middleware would normally set these; we mimic both the modern
	// path (X-Actor-Source) and the legacy header pair so the test is
	// resilient to either resolveActor branch.
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", caller)
	req.Header.Set("X-Task-ID", taskID)
	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// The response contract keeps `mcp_config` always-present so clients
	// can distinguish "no config" vs "redacted" via the companion flag.
	// `json.RawMessage` of a JSON null decodes to the literal bytes
	// `null`, not Go nil — so check for "no secret-bearing content"
	// rather than `!= nil`.
	if len(resp.McpConfig) > 0 && !bytes.Equal(bytes.TrimSpace(resp.McpConfig), []byte("null")) {
		t.Errorf("UpdateAgent response leaked mcp_config to agent actor: %s", string(resp.McpConfig))
	}
	if !resp.McpConfigRedacted {
		t.Errorf("UpdateAgent response should set mcp_config_redacted=true for agent actor")
	}
}

// TestUpdateAgent_KeepsMcpConfigForMemberActor is the matching positive
// test — a normal member request (owner/admin) still receives the full
// mcp_config in the mutation response, so the redaction does not
// accidentally regress the legitimate Web admin flow.
func TestUpdateAgent_KeepsMcpConfigForMemberActor(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	target := createHandlerTestAgent(t, "mut-mcp-member", []byte(`{"server":"member-visible"}`))

	req := newRequest(http.MethodPut, "/api/agents/"+target, map[string]any{
		"description": "owner-visible mutation",
	})
	req = withURLParam(req, "id", target)
	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.McpConfig == nil {
		t.Errorf("UpdateAgent response should keep mcp_config for member actor; got nil")
	}
	if resp.McpConfigRedacted {
		t.Errorf("UpdateAgent response should NOT mark mcp_config redacted for member actor")
	}
}

// TestUpdateAgent_PreservesSkillsInResponse is the regression for #3459:
// updating only description/instructions used to return "skills": []
// because the handler skipped the skill reload that GetAgent does. The
// DB row was always preserved; the response just lied about it, which
// scared users into manually re-running `agent skills set` and risked
// scripted clients writing the empty set back. We assert (a) the
// response carries the bound skills, (b) the DB row is unchanged, and
// (c) GetAgent reports the same shape so the two endpoints don't drift.
func TestUpdateAgent_PreservesSkillsInResponse(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "update-preserves-skills-agent", nil)
	skillA := insertHandlerTestSkill(t, "update-preserve-a", "alpha body")
	skillB := insertHandlerTestSkill(t, "update-preserve-b", "beta body")
	for _, sid := range []string{skillA, skillB} {
		if _, err := testPool.Exec(ctx,
			`INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`,
			agentID, sid,
		); err != nil {
			t.Fatalf("attach skill %s: %v", sid, err)
		}
	}

	req := newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"description": "metadata-only update",
	})
	req = withURLParam(req, "id", agentID)
	w := httptest.NewRecorder()
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp AgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	gotIDs := map[string]bool{}
	for _, s := range resp.Skills {
		gotIDs[s.ID] = true
	}
	for _, want := range []string{skillA, skillB} {
		if !gotIDs[want] {
			t.Errorf("UpdateAgent response missing skill %s; got %+v", want, resp.Skills)
		}
	}

	// Defence in depth: the junction table must be untouched too. Without
	// this check a future regression that DOES wipe agent_skill rows but
	// reloads them into the response would silently pass.
	var rowCount int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_skill WHERE agent_id = $1`,
		agentID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count agent_skill: %v", err)
	}
	if rowCount != 2 {
		t.Errorf("agent_skill row count: expected 2, got %d", rowCount)
	}

	// GetAgent must agree with UpdateAgent on the skill list — otherwise
	// CLI users will see one shape from the mutation and a different one
	// on the very next read.
	getReq := newRequest(http.MethodGet, "/api/agents/"+agentID, nil)
	getReq = withURLParam(getReq, "id", agentID)
	getW := httptest.NewRecorder()
	testHandler.GetAgent(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GetAgent: expected 200, got %d: %s", getW.Code, getW.Body.String())
	}
	var getResp AgentResponse
	if err := json.NewDecoder(getW.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode GetAgent: %v", err)
	}
	if len(getResp.Skills) != len(resp.Skills) {
		t.Errorf("GetAgent skill count %d != UpdateAgent skill count %d",
			len(getResp.Skills), len(resp.Skills))
	}
}

// TestArchiveRestoreAgent_PreservesSkillsInResponse is the sister
// regression for #3459: ArchiveAgent / RestoreAgent share the same
// agentToResponse path as UpdateAgent and previously also returned
// "skills": [] regardless of what was in the junction table. The
// archive/restore broadcasts are the only place where mobile clients
// learn about state flips, so an empty skills array there would propagate
// to every connected client until the next refetch.
func TestArchiveRestoreAgent_PreservesSkillsInResponse(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "archive-preserves-skills-agent", nil)
	skillID := insertHandlerTestSkill(t, "archive-preserve", "body")
	if _, err := testPool.Exec(ctx,
		`INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`,
		agentID, skillID,
	); err != nil {
		t.Fatalf("attach skill: %v", err)
	}

	archiveReq := newRequest(http.MethodPost, "/api/agents/"+agentID+"/archive", nil)
	archiveReq = withURLParam(archiveReq, "id", agentID)
	archiveW := httptest.NewRecorder()
	testHandler.ArchiveAgent(archiveW, archiveReq)
	if archiveW.Code != http.StatusOK {
		t.Fatalf("ArchiveAgent: expected 200, got %d: %s", archiveW.Code, archiveW.Body.String())
	}
	var archived AgentResponse
	if err := json.NewDecoder(archiveW.Body).Decode(&archived); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	if len(archived.Skills) != 1 || archived.Skills[0].ID != skillID {
		t.Errorf("ArchiveAgent: expected 1 skill %s, got %+v", skillID, archived.Skills)
	}

	restoreReq := newRequest(http.MethodPost, "/api/agents/"+agentID+"/restore", nil)
	restoreReq = withURLParam(restoreReq, "id", agentID)
	restoreW := httptest.NewRecorder()
	testHandler.RestoreAgent(restoreW, restoreReq)
	if restoreW.Code != http.StatusOK {
		t.Fatalf("RestoreAgent: expected 200, got %d: %s", restoreW.Code, restoreW.Body.String())
	}
	var restored AgentResponse
	if err := json.NewDecoder(restoreW.Body).Decode(&restored); err != nil {
		t.Fatalf("decode restore: %v", err)
	}
	if len(restored.Skills) != 1 || restored.Skills[0].ID != skillID {
		t.Errorf("RestoreAgent: expected 1 skill %s, got %+v", skillID, restored.Skills)
	}
}

// insertHandlerTestTask creates an in_progress task for the given
// agent so resolveActor's GetAgentTask lookup succeeds without
// dragging the full TaskService into the test.
func insertHandlerTestTask(t *testing.T, agentID string) string {
	t.Helper()
	ctx := context.Background()
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority)
		VALUES ($1, $2, 'running', 0)
		RETURNING id
	`, agentID, handlerTestRuntimeID(t)).Scan(&taskID); err != nil {
		t.Fatalf("insert test task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
	})
	return taskID
}

// Defence-in-depth: spot-check that the package compiles a small
// fmt.Sprintf so accidental imports stay tidy.
var _ = fmt.Sprintf
