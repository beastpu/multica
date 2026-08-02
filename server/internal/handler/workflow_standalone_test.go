package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
)

func TestStartWorkflowTemplateRunCreatesIdempotentStandaloneRun(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Standalone delivery",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{{From: "start", To: "end"}},
	}
	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template (workspace_id, name, status, created_by)
		VALUES ($1, 'Standalone test template', 'published', $2)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&templateID); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template_version (
			workspace_id, template_id, version, status, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, 'published', $3, 'test', $4, $4, now())
		RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create template version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_template SET latest_published_version_id = $1 WHERE id = $2
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
		testHandler.StartWorkflowTemplateRun(recorder, request)
		var response workflowInstanceDetailResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode standalone run response: %v; body=%s", err, recorder.Body.String())
		}
		return recorder.Code, response
	}

	firstStatus, first := start()
	if firstStatus != http.StatusCreated {
		t.Fatalf("StartWorkflowTemplateRun status = %d, run = %#v", firstStatus, first)
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
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
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
		INSERT INTO workflow_template (workspace_id, name, status, created_by)
		VALUES ($1, 'Direct agent template', 'published', $2)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&templateID); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template_version (
			workspace_id, template_id, version, status, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, 'published', $3, 'test', $4, $4, now())
		RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create template version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_template SET latest_published_version_id = $1 WHERE id = $2
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
	testHandler.StartWorkflowTemplateRun(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("StartWorkflowTemplateRun status = %d, body = %s", recorder.Code, recorder.Body.String())
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

func TestDeleteWorkflowHostCancelsAndDetachesDirectAgentTask(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	started := startDirectStandaloneRunForTest(t, "delete-direct-host", "Delete direct host")
	hostID := createWorkflowHostForTest(t, "Direct workflow host")
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_instance SET host_issue_id = $1 WHERE id = $2
	`, hostID, started.Instance.ID); err != nil {
		t.Fatalf("attach direct run host: %v", err)
	}

	var agentTaskID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent_task_queue WHERE workflow_node_task_id = $1
	`, started.Tasks[0].ID).Scan(&agentTaskID); err != nil {
		t.Fatalf("load direct task before host delete: %v", err)
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
	if status != "cancelled" || linked {
		t.Fatalf("direct task after host delete status=%q linked=%v", status, linked)
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
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
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
		INSERT INTO workflow_template (workspace_id, name, status, created_by)
		VALUES ($1, $2, 'published', $3)
		RETURNING id
	`, testWorkspaceID, title+" template", testUserID).Scan(&templateID); err != nil {
		t.Fatalf("create direct template: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template_version (
			workspace_id, template_id, version, status, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, 'published', $3, 'test', $4, $4, now())
		RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create direct template version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_template SET latest_published_version_id = $1 WHERE id = $2
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
	testHandler.StartWorkflowTemplateRun(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("StartWorkflowTemplateRun status=%d body=%s", recorder.Code, recorder.Body.String())
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
