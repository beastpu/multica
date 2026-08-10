package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
)

func TestStartWorkflowRunCreatesIdempotentStandaloneRun(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Standalone delivery",
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{{From: "start", To: "end"}},
	}
	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow (workspace_id, name, created_by)
		VALUES ($1, 'Standalone test template', $2)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&templateID); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_version (
			workspace_id, workflow_id, version, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, $3, 'test', $4, $4, now())
		RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create template version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow SET latest_published_version_id = $1 WHERE id = $2
	`, versionID, templateID); err != nil {
		t.Fatalf("publish template: %v", err)
	}

	body := map[string]any{
		"title": "Release readiness run", "idempotency_key": "standalone-run-test",
	}
	start := func() (int, workflowInstanceDetailResponse) {
		recorder := httptest.NewRecorder()
		request := withURLParam(
			newRequest(http.MethodPost,
				"/api/workflow-templates/"+templateID+"/runs?workspace_id="+testWorkspaceID,
				body,
			),
			"id", templateID,
		)
		testHandler.StartWorkflowRun(recorder, request)
		var response workflowInstanceDetailResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode standalone run response: %v; body=%s", err, recorder.Body.String())
		}
		return recorder.Code, response
	}

	firstStatus, first := start()
	if firstStatus != http.StatusCreated {
		t.Fatalf("StartWorkflowRun status = %d, run = %#v", firstStatus, first)
	}
	if first.Instance.HostIssueID != "" {
		t.Fatalf("standalone run host_issue_id = %q, want empty string", first.Instance.HostIssueID)
	}
	if first.Instance.Title != "Release readiness run" {
		t.Fatalf("standalone run title = %q", first.Instance.Title)
	}

	secondStatus, second := start()
	if secondStatus != http.StatusOK || second.Instance.ID != first.Instance.ID {
		t.Fatalf("idempotent replay = status %d run %s, want status 200 run %s",
			secondStatus, second.Instance.ID, first.Instance.ID)
	}

	var runCount, issueCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM workflow_instance
		WHERE workspace_id = $1 AND title = 'Release readiness run'
	`, testWorkspaceID).Scan(&runCount); err != nil {
		t.Fatalf("count standalone runs: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM issue
		WHERE workspace_id = $1 AND title = 'Release readiness run'
	`, testWorkspaceID).Scan(&issueCount); err != nil {
		t.Fatalf("count implicit issues: %v", err)
	}
	if runCount != 1 || issueCount != 0 {
		t.Fatalf("standalone replay counts: runs=%d issues=%d", runCount, issueCount)
	}
}

func TestListWorkflowsIncludesRecentRuns(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	started := startDirectStandaloneRunForTest(t, "recent-runs", "Latest workflow run")

	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_instance
		SET status = 'completed', completed_at = now()
		WHERE id = $1
	`, started.Instance.ID); err != nil {
		t.Fatalf("complete latest workflow run: %v", err)
	}
	var olderRunID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_instance (
			workspace_id, workflow_id, workflow_version_id, title, status,
			host_status_mode, started_by_type, started_by_id, started_at,
			completed_at
		)
		SELECT workspace_id, workflow_id, workflow_version_id,
		       'Older workflow run', 'failed', host_status_mode,
		       started_by_type, started_by_id, now() - interval '1 day',
		       now() - interval '23 hours'
		FROM workflow_instance WHERE id = $1
		RETURNING id
	`, started.Instance.ID).Scan(&olderRunID); err != nil {
		t.Fatalf("create older workflow run: %v", err)
	}

	recorder := httptest.NewRecorder()
	testHandler.ListWorkflows(
		recorder,
		newRequest(
			http.MethodGet,
			"/api/workflow-templates?workspace_id="+testWorkspaceID,
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ListWorkflows status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Workflows []struct {
			ID         string `json:"id"`
			RunCount   int64  `json:"run_count"`
			RecentRuns []struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Status string `json:"status"`
			} `json:"recent_runs"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode workflow summaries: %v", err)
	}
	var summary *struct {
		ID         string `json:"id"`
		RunCount   int64  `json:"run_count"`
		RecentRuns []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"recent_runs"`
	}
	for index := range response.Workflows {
		if response.Workflows[index].ID == started.Instance.WorkflowID {
			summary = &response.Workflows[index]
			break
		}
	}
	if summary == nil {
		t.Fatalf("workflow %s missing from summaries", started.Instance.WorkflowID)
	}
	if summary.RunCount != 2 || len(summary.RecentRuns) != 2 {
		t.Fatalf("workflow runs count=%d recent=%#v", summary.RunCount, summary.RecentRuns)
	}
	if summary.RecentRuns[0].ID != started.Instance.ID ||
		summary.RecentRuns[0].Title != "Latest workflow run" ||
		summary.RecentRuns[0].Status != "completed" ||
		summary.RecentRuns[1].ID != olderRunID {
		t.Fatalf("recent workflow runs = %#v", summary.RecentRuns)
	}
}

func TestStandaloneRunDispatchesIssueLessAgentNode(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	var agentID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent
		WHERE workspace_id = $1 AND name = 'Handler Test Agent'
	`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("load seeded agent: %v", err)
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Direct agent delivery",
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "diagnose", Kind: "activity", Name: "Diagnose",
				Description: "Inspect the repository and report the root cause.",
				IssuePolicy: "none",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "actor", ActorType: "agent", ActorID: agentID,
				},
				Completion: workflowdomain.CompletionDefinition{
					Mode: "automatic", RequiredIssueOutcome: "none",
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "diagnose"}, {From: "diagnose", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow (workspace_id, name, created_by)
		VALUES ($1, 'Direct agent template', $2)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&templateID); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_version (
			workspace_id, workflow_id, version, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, $3, 'test', $4, $4, now())
		RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create template version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow SET latest_published_version_id = $1 WHERE id = $2
	`, versionID, templateID); err != nil {
		t.Fatalf("publish template: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(http.MethodPost,
			"/api/workflow-templates/"+templateID+"/runs?workspace_id="+testWorkspaceID,
			map[string]any{
				"title": "Direct diagnosis", "idempotency_key": "direct-agent-run-test",
				"input": map[string]any{"instructions": "Focus on the checkout regression."},
			},
		),
		"id", templateID,
	)
	testHandler.StartWorkflowRun(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("StartWorkflowRun status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var started workflowInstanceDetailResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode direct run: %v", err)
	}
	if len(started.Tasks) != 1 {
		t.Fatalf("direct node workflow tasks = %d, want 1; response=%#v", len(started.Tasks), started)
	}
	if started.Tasks[0].IssueID != nil || started.Tasks[0].Source != "execution" ||
		started.Tasks[0].MaterializationStatus != "materialized" {
		t.Fatalf("direct workflow task = %#v", started.Tasks[0])
	}

	var agentTaskID, contextType, prompt string
	if err := testPool.QueryRow(ctx, `
		SELECT id::text, context->>'type', context->>'prompt'
		FROM agent_task_queue
		WHERE workflow_node_task_id = $1
	`, started.Tasks[0].ID).Scan(&agentTaskID, &contextType, &prompt); err != nil {
		t.Fatalf("load direct agent task: %v", err)
	}
	if agentTaskID == "" || contextType != "workflow_node" {
		t.Fatalf("direct agent task id=%q context.type=%q", agentTaskID, contextType)
	}
	if prompt != "Focus on the checkout regression.\n\nInspect the repository and report the root cause." {
		t.Fatalf("direct agent prompt = %q", prompt)
	}
	nodeRecorder := httptest.NewRecorder()
	nodeRequest := withURLParam(
		newRequest(
			http.MethodGet,
			"/api/workflow-node-instances/"+started.Tasks[0].WorkflowNodeInstanceID+
				"?workspace_id="+testWorkspaceID,
			nil,
		),
		"nodeInstanceId",
		started.Tasks[0].WorkflowNodeInstanceID,
	)
	testHandler.GetWorkflowNodeInstance(nodeRecorder, nodeRequest)
	if nodeRecorder.Code != http.StatusOK {
		t.Fatalf("GetWorkflowNodeInstance status=%d body=%s", nodeRecorder.Code, nodeRecorder.Body.String())
	}
	var nodeDetail struct {
		Executions []AgentTaskResponse `json:"executions"`
	}
	if err := json.Unmarshal(nodeRecorder.Body.Bytes(), &nodeDetail); err != nil {
		t.Fatalf("decode workflow node executions: %v", err)
	}
	if len(nodeDetail.Executions) != 1 || nodeDetail.Executions[0].ID != agentTaskID ||
		nodeDetail.Executions[0].IssueID != "" {
		t.Fatalf("workflow node executions = %#v", nodeDetail.Executions)
	}
	reconcileWorkflowForTest(t, started.Instance.ID, "direct-agent-running")
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_instance
		SET last_reconciled_at = now() - interval '30 seconds'
		WHERE id = $1
	`, started.Instance.ID); err != nil {
		t.Fatalf("backdate direct run reconcile: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_node_instance
		SET updated_at = now() - interval '1 minute'
		WHERE workflow_instance_id = $1
	`, started.Instance.ID); err != nil {
		t.Fatalf("backdate direct node: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_node_task
		SET updated_at = now() - interval '1 minute'
		WHERE workflow_instance_id = $1
	`, started.Instance.ID); err != nil {
		t.Fatalf("backdate direct workflow task: %v", err)
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', completed_at = now(), result = '{"output":"done"}'::jsonb
		WHERE id = $1
	`, agentTaskID); err != nil {
		t.Fatalf("complete direct agent task: %v", err)
	}
	worked, err := NewWorkflowReconciler(testHandler).ProcessNext(ctx)
	if err != nil || !worked {
		t.Fatalf("automatic direct-agent reconcile worked=%v err=%v", worked, err)
	}
	completed := latestWorkflowNodeForTest(t, started.Instance.ID, "diagnose")
	if completed.Status != "completed" {
		t.Fatalf("direct node status = %s, reasons = %s", completed.Status, completed.WaitingReasons)
	}
}

func TestCancelStandaloneRunCancelsDirectAgentTask(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	started := startDirectStandaloneRunForTest(t, "cancel-direct-run", "Cancel direct run")

	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(http.MethodPost,
			"/api/workflow-instances/"+started.Instance.ID+"/cancel?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason": "No longer needed", "idempotency_key": "cancel-direct-run-action",
			},
		),
		"instanceId", started.Instance.ID,
	)
	testHandler.CancelWorkflowInstance(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("CancelWorkflowInstance status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM agent_task_queue
		WHERE workflow_node_task_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, started.Tasks[0].ID).Scan(&status); err != nil {
		t.Fatalf("load cancelled direct task: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("direct agent task status=%q, want cancelled", status)
	}
}

func TestRetryDirectAgentTaskCreatesNewAttempt(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	started := startDirectStandaloneRunForTest(t, "retry-direct-run", "Retry direct run")

	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'failed', completed_at = now(), error = 'agent failed'
		WHERE workflow_node_task_id = $1
	`, started.Tasks[0].ID); err != nil {
		t.Fatalf("fail direct agent task: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(http.MethodPost,
			"/api/workflow-node-tasks/"+started.Tasks[0].ID+"/retry?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason": "Retry after agent failure", "idempotency_key": "retry-direct-run-action",
			},
		),
		"taskId", started.Tasks[0].ID,
	)
	testHandler.RetryWorkflowNodeTask(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("RetryWorkflowNodeTask status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var count int
	var latestStatus string
	var latestAttempt int32
	if err := testPool.QueryRow(ctx, `
		SELECT count(*),
		       (array_agg(status ORDER BY attempt DESC, created_at DESC))[1],
		       max(attempt)
		FROM agent_task_queue
		WHERE workflow_node_task_id = $1
	`, started.Tasks[0].ID).Scan(&count, &latestStatus, &latestAttempt); err != nil {
		t.Fatalf("load retried direct tasks: %v", err)
	}
	if count != 2 || latestStatus != "queued" || latestAttempt != 2 {
		t.Fatalf("retried direct tasks count=%d latest_status=%q latest_attempt=%d", count, latestStatus, latestAttempt)
	}
}

func TestDeleteWorkflowHostCancelsRunAndPreservesHistory(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	started := startDirectStandaloneRunForTest(t, "delete-direct-host", "Delete direct host")
	hostID := createWorkflowHostForTest(t, "Direct workflow host")
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_instance
		SET host_issue_id = $1, result = '{"partial":"keep"}'::jsonb
		WHERE id = $2
	`, hostID, started.Instance.ID); err != nil {
		t.Fatalf("attach direct run host: %v", err)
	}
	var completedRunID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_instance (
			workspace_id, workflow_id, workflow_version_id, host_issue_id, title,
			status, host_status_mode, started_by_type, started_by_id, completed_at
		)
		SELECT workspace_id, workflow_id, workflow_version_id, $1, 'Completed history',
		       'completed', host_status_mode, started_by_type, started_by_id, now()
		FROM workflow_instance WHERE id = $2
		RETURNING id
	`, hostID, started.Instance.ID).Scan(&completedRunID); err != nil {
		t.Fatalf("create completed workflow history: %v", err)
	}

	var agentTaskID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent_task_queue WHERE workflow_node_task_id = $1
	`, started.Tasks[0].ID).Scan(&agentTaskID); err != nil {
		t.Fatalf("load direct task before host delete: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO workflow_artifact (
			workspace_id, workflow_instance_id, workflow_node_instance_id,
			artifact_key, attempt, kind, name, content, submitted_by_type
		) VALUES ($1, $2, $3, 'history', 1, 'document', 'History', 'Keep me', 'system')
	`, testWorkspaceID, started.Instance.ID, started.Tasks[0].WorkflowNodeInstanceID); err != nil {
		t.Fatalf("create workflow artifact history: %v", err)
	}
	var eventCountBefore int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM workflow_event WHERE workflow_instance_id = $1
	`, started.Instance.ID).Scan(&eventCountBefore); err != nil {
		t.Fatalf("count workflow events before host delete: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(http.MethodDelete,
			"/api/issues/"+hostID+"?workspace_id="+testWorkspaceID,
			nil,
		),
		"id", hostID,
	)
	testHandler.DeleteIssue(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("DeleteIssue status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	var status string
	var linked bool
	if err := testPool.QueryRow(ctx, `
		SELECT status, workflow_node_task_id IS NOT NULL
		FROM agent_task_queue WHERE id = $1
	`, agentTaskID).Scan(&status, &linked); err != nil {
		t.Fatalf("load direct task after host delete: %v", err)
	}
	if status != "cancelled" || !linked {
		t.Fatalf("direct task after host delete status=%q linked=%v", status, linked)
	}

	var runStatus string
	var hostIssueID *string
	var title string
	var partialResult string
	if err := testPool.QueryRow(ctx, `
		SELECT status, host_issue_id::text, title, result->>'partial'
		FROM workflow_instance WHERE id = $1
	`, started.Instance.ID).Scan(&runStatus, &hostIssueID, &title, &partialResult); err != nil {
		t.Fatalf("load preserved workflow run: %v", err)
	}
	if runStatus != "cancelled" || hostIssueID != nil || title != started.Instance.Title || partialResult != "keep" {
		t.Fatalf("preserved run status=%q host=%v title=%q partial_result=%q", runStatus, hostIssueID, title, partialResult)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT status, host_issue_id::text
		FROM workflow_instance WHERE id = $1
	`, completedRunID).Scan(&runStatus, &hostIssueID); err != nil {
		t.Fatalf("load completed workflow history: %v", err)
	}
	if runStatus != "completed" || hostIssueID != nil {
		t.Fatalf("completed run after host delete status=%q host=%v", runStatus, hostIssueID)
	}

	for table := range map[string]struct{}{
		"workflow_node_instance": {},
		"workflow_node_task":     {},
		"workflow_artifact":      {},
	} {
		var count int
		if err := testPool.QueryRow(ctx, fmt.Sprintf(
			"SELECT count(*) FROM %s WHERE workflow_instance_id = $1", table,
		), started.Instance.ID).Scan(&count); err != nil {
			t.Fatalf("count preserved %s history: %v", table, err)
		}
		if count == 0 {
			t.Fatalf("workflow host deletion removed %s history", table)
		}
	}

	var eventCountAfter, cancellationEvents int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE event_type = 'workflow.cancelled')
		FROM workflow_event WHERE workflow_instance_id = $1
	`, started.Instance.ID).Scan(&eventCountAfter, &cancellationEvents); err != nil {
		t.Fatalf("count workflow events after host delete: %v", err)
	}
	if eventCountAfter != eventCountBefore+1 || cancellationEvents != 1 {
		t.Fatalf(
			"preserved workflow events before=%d after=%d cancellations=%d",
			eventCountBefore, eventCountAfter, cancellationEvents,
		)
	}
}

func startDirectStandaloneRunForTest(
	t *testing.T,
	idempotencyKey string,
	title string,
) workflowInstanceDetailResponse {
	t.Helper()
	ctx := context.Background()
	var agentID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent
		WHERE workspace_id = $1 AND name = 'Handler Test Agent'
	`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("load seeded agent: %v", err)
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Direct agent test",
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "diagnose", Kind: "activity", Name: "Diagnose",
				Description: "Inspect the repository.", IssuePolicy: "none",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "actor", ActorType: "agent", ActorID: agentID,
				},
				Completion: workflowdomain.CompletionDefinition{
					Mode: "automatic", RequiredIssueOutcome: "none",
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "diagnose"}, {From: "diagnose", To: "end"},
		},
	}
	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id
	`, testWorkspaceID, title+" template", testUserID).Scan(&templateID); err != nil {
		t.Fatalf("create direct template: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_version (
			workspace_id, workflow_id, version, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, $3, 'test', $4, $4, now())
		RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create direct template version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow SET latest_published_version_id = $1 WHERE id = $2
	`, versionID, templateID); err != nil {
		t.Fatalf("publish direct template: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(http.MethodPost,
			"/api/workflow-templates/"+templateID+"/runs?workspace_id="+testWorkspaceID,
			map[string]any{"title": title, "idempotency_key": idempotencyKey},
		),
		"id", templateID,
	)
	testHandler.StartWorkflowRun(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("StartWorkflowRun status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var started workflowInstanceDetailResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode direct run: %v", err)
	}
	if len(started.Tasks) != 1 {
		t.Fatalf("direct run tasks=%d, want 1", len(started.Tasks))
	}
	return started
}

func TestListWorkflowInstancesFiltersByHostIssuePresence(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Host presence filter",
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{{From: "start", To: "end"}},
	}
	templateID := createPublishedWorkflowForTest(
		t,
		"Host presence filter template",
		definition,
	)

	hostID := createWorkflowHostForTest(t, "Hosted run issue")
	hosted := startWorkflowForTest(t, hostID, templateID, nil, "host-presence-hosted")

	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(http.MethodPost,
			"/api/workflow-templates/"+templateID+"/runs?workspace_id="+testWorkspaceID,
			map[string]any{
				"title":           "Standalone presence run",
				"idempotency_key": "host-presence-standalone",
			},
		),
		"id", templateID,
	)
	testHandler.StartWorkflowRun(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("StartWorkflowRun status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var standalone workflowInstanceDetailResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &standalone); err != nil {
		t.Fatalf("decode standalone run: %v", err)
	}

	list := func(query string) (int, []workflowInstanceResponse, int64) {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := newRequest(http.MethodGet,
			"/api/workflow-instances?workspace_id="+testWorkspaceID+query, nil)
		testHandler.ListWorkflowInstances(recorder, request)
		var response struct {
			Instances []workflowInstanceResponse `json:"instances"`
			Total     int64                      `json:"total"`
		}
		if recorder.Code == http.StatusOK {
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode workflow instance list: %v", err)
			}
		}
		return recorder.Code, response.Instances, response.Total
	}

	status, hostedOnly, hostedTotal := list("&has_host_issue=true")
	if status != http.StatusOK || hostedTotal != 1 || len(hostedOnly) != 1 ||
		hostedOnly[0].ID != hosted.Instance.ID {
		t.Fatalf(
			"has_host_issue=true: status=%d total=%d instances=%#v",
			status, hostedTotal, hostedOnly,
		)
	}

	status, standaloneOnly, standaloneTotal := list("&has_host_issue=false")
	if status != http.StatusOK || standaloneTotal != 1 || len(standaloneOnly) != 1 ||
		standaloneOnly[0].ID != standalone.Instance.ID {
		t.Fatalf(
			"has_host_issue=false: status=%d total=%d instances=%#v",
			status, standaloneTotal, standaloneOnly,
		)
	}

	status, _, allTotal := list("")
	if status != http.StatusOK || allTotal != 2 {
		t.Fatalf("unfiltered list: status=%d total=%d", status, allTotal)
	}

	if status, _, _ := list("&has_host_issue=maybe"); status != http.StatusBadRequest {
		t.Fatalf("has_host_issue=maybe status=%d, want 400", status)
	}
}

// Starting from an issue used to be the only start path without the owner
// fallback: standalone and create-run both defaulted the starter into an
// unassigned owner role, this one dropped the run into needs_setup instead.
func TestStartIssueWorkflowDefaultsOwnerToStarter(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Owner fallback",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "owner", IssuePolicy: "none",
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "end"},
		},
	}
	templateID := createPublishedWorkflowForTest(
		t,
		"Owner fallback template",
		definition,
	)
	hostID := createWorkflowHostForTest(t, "Owner fallback issue")

	started := startWorkflowForTest(t, hostID, templateID, nil, "owner-fallback-start")
	if started.Instance.Status == "needs_setup" {
		t.Fatalf("run status = needs_setup, want the owner defaulted to the starter")
	}
	var ownerActorID string
	for _, assignment := range started.RoleAssignments {
		if assignment.RoleKey == "owner" {
			ownerActorID = assignment.ActorID
		}
	}
	if ownerActorID != testUserID {
		t.Fatalf("owner assignment = %q, want starter %q", ownerActorID, testUserID)
	}
}
