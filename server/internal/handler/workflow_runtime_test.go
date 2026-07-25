package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

func TestWorkflowRuntimeReworkAndAcceptance(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	ctx := context.Background()
	cleanupWorkflowRuntimeTest(t)

	var hostID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Workflow runtime host', 'todo', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create host issue: %v", err)
	}

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Runtime delivery",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true, AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", ActivityMode: "work", Name: "Implementation", OwnerRole: "owner",
				IssuePolicy: "fixed_and_dynamic", TimeoutMinutes: 1,
				Executor: workflowdomain.ExecutorDefinition{Strategies: []workflowdomain.ExecutorStrategy{
					{Kind: "fixed_role", Role: "owner"}, {Kind: "manual"},
				}},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "implementation", Title: "Implement {{host.title}}", AssigneeRole: "owner", Required: true,
				}},
				SubmissionSchema: &workflowdomain.SubmissionSchema{Fields: []workflowdomain.SubmissionField{{
					Key: "result", Name: "Result", Type: "text", Required: true,
				}}},
				Verdict: &workflowdomain.VerdictDefinition{Evaluator: "member", RequiredResult: "pass"},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done", SubmissionRequired: true,
					VerdictRequired: "pass", Confirmation: "owner_any",
				},
			},
			{Key: "acceptance", Kind: "activity", ActivityMode: "acceptance", Name: "Acceptance", OwnerRole: "owner"},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"}, {From: "work", To: "acceptance"}, {From: "acceptance", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{
			Policy: "member", ApproverRole: "owner", NodeKey: "acceptance", ReworkTargets: []string{"work"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template (
			workspace_id, name, status, created_by
		) VALUES ($1, 'Runtime test template', 'published', $2)
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
		t.Fatalf("set published version: %v", err)
	}

	startRecorder := httptest.NewRecorder()
	startRequest := withURLParam(newRequest(http.MethodPost, "/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID, map[string]any{
		"template_id": templateID,
		"role_assignments": []map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
		}},
		"idempotency_key": "runtime-test-start",
	}), "id", hostID)
	testHandler.StartIssueWorkflow(startRecorder, startRequest)
	if startRecorder.Code != http.StatusCreated {
		t.Fatalf("StartIssueWorkflow status = %d, body = %s", startRecorder.Code, startRecorder.Body.String())
	}
	var started workflowInstanceDetailResponse
	if err := json.Unmarshal(startRecorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if started.Instance.Status != "running" || len(started.Tasks) != 1 || started.Tasks[0].IssueID == nil {
		t.Fatalf("unexpected start response: %#v", started)
	}

	instanceID := started.Instance.ID
	firstWorkNode := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
	firstIssueID := *started.Tasks[0].IssueID
	dynamicTask := createDynamicWorkflowIssue(
		t, firstWorkNode.ID, "Dynamic investigation", "runtime-test-dynamic-1",
	)
	if dynamicTask.Source != "dynamic" || dynamicTask.IssueID == nil {
		t.Fatalf("dynamic task = %#v", dynamicTask)
	}
	fanOutSubmissionRecorder := httptest.NewRecorder()
	fanOutSubmissionRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+firstWorkNode.ID+
				"/submissions?workspace_id="+testWorkspaceID,
			map[string]any{
				"payload": map[string]any{"result": "propose investigation"},
				"summary": "Propose a stable fan-out task",
				"proposed_tasks": []map[string]any{{
					"key": "runtime_fanout_investigation", "title": "Investigate {{host.title}}",
					"assignee_role": "owner", "required": false,
				}},
				"idempotency_key": "runtime-test-fanout-submission",
			},
		),
		"nodeInstanceId",
		firstWorkNode.ID,
	)
	testHandler.CreateWorkflowNodeSubmission(
		fanOutSubmissionRecorder,
		fanOutSubmissionRequest,
	)
	if fanOutSubmissionRecorder.Code != http.StatusCreated {
		t.Fatalf(
			"CreateWorkflowNodeSubmission fan-out status = %d, body = %s",
			fanOutSubmissionRecorder.Code,
			fanOutSubmissionRecorder.Body.String(),
		)
	}
	var fanOutSubmissionResponse struct {
		Submission workflowSubmissionResponse `json:"submission"`
	}
	if err := json.Unmarshal(
		fanOutSubmissionRecorder.Body.Bytes(),
		&fanOutSubmissionResponse,
	); err != nil {
		t.Fatalf("decode fan-out submission: %v", err)
	}
	if len(fanOutSubmissionResponse.Submission.ProposedTasks) != 1 {
		t.Fatalf(
			"fan-out proposed tasks = %#v",
			fanOutSubmissionResponse.Submission.ProposedTasks,
		)
	}
	confirmFanOutBody := map[string]any{
		"idempotency_key": "runtime-test-confirm-fanout",
	}
	confirmFanOut := func() (int, []workflowTaskResponse, bool, string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := withURLParams(
			newRequest(
				http.MethodPost,
				"/api/workflow-node-instances/"+firstWorkNode.ID+
					"/submissions/"+fanOutSubmissionResponse.Submission.ID+
					"/confirm-tasks?workspace_id="+testWorkspaceID,
				confirmFanOutBody,
			),
			"nodeInstanceId",
			firstWorkNode.ID,
			"submissionId",
			fanOutSubmissionResponse.Submission.ID,
		)
		testHandler.ConfirmWorkflowSubmissionTasks(recorder, request)
		var response struct {
			Tasks    []workflowTaskResponse `json:"tasks"`
			Replayed bool                   `json:"replayed"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode confirmed fan-out tasks: %v", err)
		}
		return recorder.Code, response.Tasks, response.Replayed, recorder.Body.String()
	}
	confirmCode, fanOutTasks, replayed, confirmBody := confirmFanOut()
	if confirmCode != http.StatusCreated || replayed || len(fanOutTasks) != 1 ||
		fanOutTasks[0].Source != "dynamic" || fanOutTasks[0].IssueID == nil {
		t.Fatalf(
			"confirmed fan-out status=%d replayed=%v tasks=%#v body=%s",
			confirmCode,
			replayed,
			fanOutTasks,
			confirmBody,
		)
	}
	replayCode, replayTasks, replayed, replayBody := confirmFanOut()
	if replayCode != http.StatusOK || !replayed || len(replayTasks) != 1 ||
		replayTasks[0].ID != fanOutTasks[0].ID {
		t.Fatalf(
			"replayed fan-out status=%d replayed=%v tasks=%#v body=%s",
			replayCode,
			replayed,
			replayTasks,
			replayBody,
		)
	}
	var workflowViewerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Workflow viewer', 'workflow-viewer-permission-test@multica.ai')
		RETURNING id
	`).Scan(&workflowViewerID); err != nil {
		t.Fatalf("create workflow permission viewer: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, workflowViewerID); err != nil {
		t.Fatalf("create workflow permission member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `
			DELETE FROM member WHERE workspace_id = $1 AND user_id = $2
		`, testWorkspaceID, workflowViewerID)
		_, _ = testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, workflowViewerID)
	})
	forbiddenFanOutRecorder := httptest.NewRecorder()
	forbiddenFanOutRequest := withURLParams(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+firstWorkNode.ID+
				"/submissions/"+fanOutSubmissionResponse.Submission.ID+
				"/confirm-tasks?workspace_id="+testWorkspaceID,
			map[string]any{"idempotency_key": "runtime-test-forbidden-fanout"},
		),
		"nodeInstanceId",
		firstWorkNode.ID,
		"submissionId",
		fanOutSubmissionResponse.Submission.ID,
	)
	forbiddenFanOutRequest.Header.Set("X-User-ID", workflowViewerID)
	testHandler.ConfirmWorkflowSubmissionTasks(
		forbiddenFanOutRecorder,
		forbiddenFanOutRequest,
	)
	if forbiddenFanOutRecorder.Code != http.StatusForbidden {
		t.Fatalf(
			"non-owner fan-out status = %d, want %d, body = %s",
			forbiddenFanOutRecorder.Code,
			http.StatusForbidden,
			forbiddenFanOutRecorder.Body.String(),
		)
	}
	foreignWorkspaceID := createOtherTestWorkspace(t)
	crossWorkspaceRecorder := httptest.NewRecorder()
	crossWorkspaceRequest := withURLParam(
		newRequest(
			http.MethodGet,
			"/api/workflow-instances/"+instanceID+
				"?workspace_id="+foreignWorkspaceID,
			nil,
		),
		"instanceId",
		instanceID,
	)
	crossWorkspaceRequest.Header.Set("X-Workspace-ID", foreignWorkspaceID)
	testHandler.GetWorkflowInstance(crossWorkspaceRecorder, crossWorkspaceRequest)
	if crossWorkspaceRecorder.Code != http.StatusNotFound {
		t.Fatalf(
			"cross-workspace workflow read status = %d, want %d, body = %s",
			crossWorkspaceRecorder.Code,
			http.StatusNotFound,
			crossWorkspaceRecorder.Body.String(),
		)
	}
	resolveWorkflowExecutor(t, firstWorkNode.ID, dynamicTask.ID, "runtime-test-resolve-1")
	detached := changeWorkflowTask(
		t, dynamicTask.ID, "detach", "runtime-test-detach-1",
	)
	if detached.MaterializationStatus != "cancelled" || detached.Required || detached.IssueID != nil {
		t.Fatalf("detached task = %#v", detached)
	}
	var detachedOriginType *string
	if err := testPool.QueryRow(ctx, `
		SELECT origin_type FROM issue WHERE id = $1
	`, *dynamicTask.IssueID).Scan(&detachedOriginType); err != nil {
		t.Fatalf("load detached issue origin: %v", err)
	}
	if detachedOriginType != nil {
		t.Fatalf("detached issue origin_type = %q, want NULL", *detachedOriginType)
	}

	retryTask := createDynamicWorkflowIssue(
		t, firstWorkNode.ID, "Retry investigation", "runtime-test-dynamic-retry",
	)
	if retryTask.IssueID == nil {
		t.Fatalf("retry fixture task = %#v", retryTask)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, *retryTask.IssueID); err != nil {
		t.Fatalf("delete retry fixture issue: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_node_task
		SET issue_id = NULL, materialization_status = 'materializing',
		    attempt_count = 1, claimed_at = now() - interval '3 minutes',
		    last_error = ''
		WHERE id = $1
	`, retryTask.ID); err != nil {
		t.Fatalf("create stale worker fixture: %v", err)
	}
	if err := NewWorkflowSweeper(testHandler).SweepOnce(ctx); err != nil {
		t.Fatalf("sweep stale materialization: %v", err)
	}
	staleResetTask, err := testHandler.Queries.GetWorkflowNodeTaskInWorkspace(
		ctx,
		db.GetWorkflowNodeTaskInWorkspaceParams{
			ID: parseUUID(retryTask.ID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || staleResetTask.MaterializationStatus != "pending_materialization" {
		t.Fatalf("stale materialization reset = %#v, err = %v", staleResetTask, err)
	}
	staleNode := latestWorkflowNodeForTest(t, instanceID, "work")
	if !jsonContainsWaitingReason(staleNode.WaitingReasons, "stale_materialization") {
		t.Fatalf("stale materialization reason missing: %s", staleNode.WaitingReasons)
	}
	previousFlags := testHandler.FeatureFlags
	pausedProvider := featureflag.NewStaticProvider()
	pausedProvider.Set(
		featureflags.WorkflowsActivityEngine,
		featureflag.Rule{Default: true},
	)
	pausedProvider.Set(
		featureflags.OpsPauseWorkflowProgression,
		featureflag.Rule{Default: true},
	)
	testHandler.FeatureFlags = featureflag.NewService(pausedProvider)
	pausedWorked, pausedErr := NewWorkflowMaterializer(testHandler).
		ProcessNext(ctx)
	testHandler.FeatureFlags = previousFlags
	if pausedErr != nil || pausedWorked {
		t.Fatalf(
			"paused materializer worked=%v err=%v, want false and nil",
			pausedWorked,
			pausedErr,
		)
	}
	pausedTask, err := testHandler.Queries.GetWorkflowNodeTaskInWorkspace(
		ctx,
		db.GetWorkflowNodeTaskInWorkspaceParams{
			ID:          parseUUID(retryTask.ID),
			WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil ||
		pausedTask.MaterializationStatus != "pending_materialization" ||
		pausedTask.AttemptCount != staleResetTask.AttemptCount {
		t.Fatalf(
			"paused materialization mutated retry budget: before=%#v after=%#v err=%v",
			staleResetTask,
			pausedTask,
			err,
		)
	}
	if _, err := testPool.Exec(
		ctx,
		`UPDATE workflow_instance SET last_reconciled_at = NULL WHERE id = $1`,
		instanceID,
	); err != nil {
		t.Fatalf("mark reconcile pending before pause: %v", err)
	}
	testHandler.FeatureFlags = featureflag.NewService(pausedProvider)
	reconcileWorked, reconcileErr := NewWorkflowReconciler(testHandler).
		ProcessNext(ctx)
	testHandler.FeatureFlags = previousFlags
	if reconcileErr != nil || !reconcileWorked {
		t.Fatalf(
			"paused reconciler worked=%v err=%v, want true and nil",
			reconcileWorked,
			reconcileErr,
		)
	}
	var reconcileStillPending bool
	var reconcileDeferred bool
	if err := testPool.QueryRow(
		ctx,
		`SELECT last_reconciled_at IS NULL, reconcile_after > now()
		 FROM workflow_instance WHERE id = $1`,
		instanceID,
	).Scan(&reconcileStillPending, &reconcileDeferred); err != nil ||
		!reconcileStillPending || !reconcileDeferred {
		t.Fatalf(
			"paused reconcile was not deferred: pending=%v deferred=%v err=%v",
			reconcileStillPending,
			reconcileDeferred,
			err,
		)
	}
	fairnessHostID := createWorkflowHostForTest(t, "Workflow fairness host")
	fairnessWorkflow := startWorkflowForTest(
		t,
		fairnessHostID,
		templateID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
		}},
		"runtime-test-fairness-start",
	)
	if _, err := testPool.Exec(
		ctx,
		`UPDATE workflow_instance
		 SET last_reconciled_at = NULL, reconcile_after = NULL
		 WHERE id = $1`,
		fairnessWorkflow.Instance.ID,
	); err != nil {
		t.Fatalf("mark fairness workflow pending: %v", err)
	}
	fairnessWorked, fairnessErr := NewWorkflowReconciler(testHandler).
		ProcessNext(ctx)
	if fairnessErr != nil || !fairnessWorked {
		t.Fatalf(
			"eligible workflow behind deferred workflow worked=%v err=%v",
			fairnessWorked,
			fairnessErr,
		)
	}
	var fairnessReconciled bool
	if err := testPool.QueryRow(
		ctx,
		`SELECT last_reconciled_at IS NOT NULL
		 FROM workflow_instance WHERE id = $1`,
		fairnessWorkflow.Instance.ID,
	).Scan(&fairnessReconciled); err != nil || !fairnessReconciled {
		t.Fatalf(
			"eligible workflow behind deferred workflow was not reconciled: reconciled=%v err=%v",
			fairnessReconciled,
			err,
		)
	}
	workerResults := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := NewWorkflowMaterializer(testHandler).ProcessNext(ctx)
			workerResults <- err
		}()
	}
	for range 2 {
		if err := <-workerResults; err != nil {
			t.Fatalf("concurrent workflow materializer: %v", err)
		}
	}
	var workerIssueCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM issue
		WHERE workspace_id = $1 AND origin_type = 'workflow' AND origin_id = $2
	`, testWorkspaceID, retryTask.ID).Scan(&workerIssueCount); err != nil {
		t.Fatalf("count worker-created issues: %v", err)
	}
	if workerIssueCount != 1 {
		t.Fatalf("concurrent materializers created %d issues, want 1", workerIssueCount)
	}
	workerTask, err := testHandler.Queries.GetWorkflowNodeTaskInWorkspace(
		ctx,
		db.GetWorkflowNodeTaskInWorkspaceParams{
			ID: parseUUID(retryTask.ID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || !workerTask.IssueID.Valid {
		t.Fatalf("worker task = %#v, err = %v", workerTask, err)
	}
	originalWorkerIssueID := workerTask.IssueID
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_node_task
		SET issue_id = NULL,
		    materialization_status = 'materializing',
		    claimed_at = now() - interval '3 minutes'
		WHERE id = $1
	`, retryTask.ID); err != nil {
		t.Fatalf("simulate materialization binding crash: %v", err)
	}
	if err := NewWorkflowSweeper(testHandler).SweepOnce(ctx); err != nil {
		t.Fatalf("sweep crashed materialization binding: %v", err)
	}
	worked, err := NewWorkflowMaterializer(testHandler).ProcessNext(ctx)
	if err != nil || !worked {
		t.Fatalf(
			"repair crashed materialization binding worked=%v err=%v",
			worked,
			err,
		)
	}
	repairedBinding, err := testHandler.Queries.GetWorkflowNodeTaskInWorkspace(
		ctx,
		db.GetWorkflowNodeTaskInWorkspaceParams{
			ID:          parseUUID(retryTask.ID),
			WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil ||
		repairedBinding.MaterializationStatus != "materialized" ||
		repairedBinding.IssueID != originalWorkerIssueID {
		t.Fatalf(
			"repaired materialization binding = %#v, err = %v",
			repairedBinding,
			err,
		)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM issue
		WHERE workspace_id = $1 AND origin_type = 'workflow' AND origin_id = $2
	`, testWorkspaceID, retryTask.ID).Scan(&workerIssueCount); err != nil {
		t.Fatalf("count repaired materialization issues: %v", err)
	}
	if workerIssueCount != 1 {
		t.Fatalf(
			"materialization binding repair created %d issues, want 1",
			workerIssueCount,
		)
	}
	if _, err := testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, workerTask.IssueID); err != nil {
		t.Fatalf("delete worker-created issue: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_node_task
		SET issue_id = NULL, materialization_status = 'failed', last_error = 'injected'
		WHERE id = $1
	`, retryTask.ID); err != nil {
		t.Fatalf("mark retry fixture failed: %v", err)
	}
	retried := changeWorkflowTask(
		t, retryTask.ID, "retry", "runtime-test-retry-1",
	)
	if retried.MaterializationStatus != "materialized" || retried.IssueID == nil {
		t.Fatalf("retried task = %#v", retried)
	}

	if _, err := testPool.Exec(ctx, `
		DELETE FROM workflow_node_task WHERE id = $1
	`, started.Tasks[0].ID); err != nil {
		t.Fatalf("delete fixed task for sweeper repair: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_node_instance
		SET activated_at = now() - interval '2 minutes'
		WHERE id = $1
	`, firstWorkNode.ID); err != nil {
		t.Fatalf("age workflow node for timeout: %v", err)
	}
	if err := NewWorkflowSweeper(testHandler).SweepOnce(ctx); err != nil {
		t.Fatalf("workflow sweeper: %v", err)
	}
	sweptNode := latestWorkflowNodeForTest(t, instanceID, "work")
	if !jsonContainsWaitingReason(sweptNode.WaitingReasons, "missing_node_task") ||
		!jsonContainsWaitingReason(sweptNode.WaitingReasons, "node_timeout") {
		t.Fatalf("sweeper waiting reasons = %s", sweptNode.WaitingReasons)
	}
	var interventionInboxCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM inbox_item
		WHERE workspace_id = $1
		  AND issue_id = $2
		  AND recipient_type = 'member'
		  AND recipient_id = $3
		  AND type = 'workflow_action_required'
		  AND details->>'reason' = 'intervention'
	`, testWorkspaceID, hostID, testUserID).Scan(&interventionInboxCount); err != nil {
		t.Fatalf("count workflow intervention inbox: %v", err)
	}
	if interventionInboxCount < 1 {
		t.Fatalf(
			"workflow intervention inbox count = %d, want at least 1",
			interventionInboxCount,
		)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_instance
		SET last_reconciled_at = NULL, reconcile_after = NULL
		WHERE id = $1
	`, instanceID); err != nil {
		t.Fatalf("make workflow eligible for reconcile worker: %v", err)
	}
	worked, err = NewWorkflowReconciler(testHandler).ProcessNext(ctx)
	if err != nil || !worked {
		t.Fatalf("workflow reconciler repair worked=%v err=%v", worked, err)
	}
	worked, err = NewWorkflowMaterializer(testHandler).ProcessNext(ctx)
	if err != nil || !worked {
		t.Fatalf("workflow materializer repair worked=%v err=%v", worked, err)
	}
	repairedTasks, err := testHandler.Queries.ListWorkflowNodeTasks(
		ctx,
		db.ListWorkflowNodeTasksParams{
			WorkflowNodeInstanceID: parseUUID(firstWorkNode.ID),
			WorkspaceID:            parseUUID(testWorkspaceID),
		},
	)
	if err != nil {
		t.Fatalf("list repaired workflow tasks: %v", err)
	}
	foundRepairedFixedTask := false
	for _, task := range repairedTasks {
		if task.TaskKey == "implementation" && task.IssueID.Valid {
			firstIssueID = uuidToString(task.IssueID)
			foundRepairedFixedTask = true
		}
	}
	if !foundRepairedFixedTask {
		t.Fatalf("reconciler did not restore fixed task: %#v", repairedTasks)
	}

	reconcileRecorder := httptest.NewRecorder()
	reconcileRequest := withURLParam(newRequest(http.MethodPost, "/api/workflow-instances/"+instanceID+"/reconcile?workspace_id="+testWorkspaceID, map[string]any{
		"idempotency_key": "runtime-test-before-done",
	}), "instanceId", instanceID)
	testHandler.ReconcileWorkflowInstance(reconcileRecorder, reconcileRequest)
	if reconcileRecorder.Code != http.StatusOK {
		t.Fatalf("reconcile before done status = %d, body = %s", reconcileRecorder.Code, reconcileRecorder.Body.String())
	}
	nodeAfterEarlyReconcile, err := testHandler.Queries.GetWorkflowNodeInstanceInWorkspace(ctx, db.GetWorkflowNodeInstanceInWorkspaceParams{
		ID: parseUUID(firstWorkNode.ID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil || nodeAfterEarlyReconcile.Status != "waiting" {
		t.Fatalf("work node after early reconcile = %#v, err = %v", nodeAfterEarlyReconcile, err)
	}

	batchCompleteRecorder := httptest.NewRecorder()
	testHandler.BatchUpdateIssues(
		batchCompleteRecorder,
		newRequest(
			http.MethodPost,
			"/api/issues/batch-update?workspace_id="+testWorkspaceID,
			map[string]any{
				"issue_ids": []string{
					firstIssueID,
					*fanOutTasks[0].IssueID,
				},
				"updates": map[string]any{"status": "done"},
			},
		),
	)
	if batchCompleteRecorder.Code != http.StatusOK {
		t.Fatalf(
			"batch complete workflow issues status=%d body=%s",
			batchCompleteRecorder.Code,
			batchCompleteRecorder.Body.String(),
		)
	}
	var hostCommentCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id = $1`, hostID).Scan(&hostCommentCount); err != nil {
		t.Fatalf("count host comments: %v", err)
	}
	if hostCommentCount != 0 {
		t.Fatalf("workflow issue completion created %d legacy parent comments, want 0", hostCommentCount)
	}
	postSubmission(t, firstWorkNode.ID, "runtime-test-submission-1", "first result")
	nodeBeforeVerdict := latestWorkflowNodeForTest(t, instanceID, "work")
	if nodeBeforeVerdict.Status != "waiting" ||
		!jsonContainsWaitingReason(nodeBeforeVerdict.WaitingReasons, "member_verdict_required") {
		t.Fatalf("work node before verdict = %#v", nodeBeforeVerdict)
	}
	postWorkflowVerdict(t, firstWorkNode.ID, "runtime-test-verdict-1")
	nodeBeforeConfirmation := latestWorkflowNodeForTest(t, instanceID, "work")
	if nodeBeforeConfirmation.Status != "waiting" ||
		!jsonContainsWaitingReason(nodeBeforeConfirmation.WaitingReasons, "confirmation_required") {
		t.Fatalf("work node before confirmation = %#v", nodeBeforeConfirmation)
	}
	confirmWorkflowNode(t, firstWorkNode.ID, "runtime-test-confirm-1")

	acceptanceNode := latestWorkflowNodeForTest(t, instanceID, "acceptance")
	if acceptanceNode.Attempt != 1 || acceptanceNode.Status != "waiting" {
		t.Fatalf("first acceptance node = %#v", acceptanceNode)
	}
	transitionWorkflowNode(t, firstWorkNode.ID, "rollback", "runtime-test-rollback")

	secondWorkNode := latestWorkflowNodeForTest(t, instanceID, "work")
	if secondWorkNode.Attempt != 2 || secondWorkNode.Status != "active" {
		t.Fatalf("rework node = %#v", secondWorkNode)
	}
	secondTasks, err := testHandler.Queries.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: secondWorkNode.ID, WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil || len(secondTasks) != 1 || !secondTasks[0].IssueID.Valid {
		t.Fatalf("second attempt tasks = %#v, err = %v", secondTasks, err)
	}
	completeWorkflowIssue(t, uuidToString(secondTasks[0].IssueID))
	if err := testPool.QueryRow(
		ctx,
		`SELECT count(*) FROM comment WHERE issue_id = $1`,
		hostID,
	).Scan(&hostCommentCount); err != nil {
		t.Fatalf("count host comments after single completion: %v", err)
	}
	if hostCommentCount != 0 {
		t.Fatalf(
			"single workflow issue completion created %d legacy parent comments, want 0",
			hostCommentCount,
		)
	}
	postSubmission(t, uuidToString(secondWorkNode.ID), "runtime-test-submission-2", "reworked result")
	postWorkflowVerdict(t, uuidToString(secondWorkNode.ID), "runtime-test-verdict-2")
	confirmWorkflowNode(t, uuidToString(secondWorkNode.ID), "runtime-test-confirm-2")

	secondAcceptanceNode := latestWorkflowNodeForTest(t, instanceID, "acceptance")
	if secondAcceptanceNode.Attempt != 2 || secondAcceptanceNode.Status != "waiting" {
		t.Fatalf("second acceptance node = %#v", secondAcceptanceNode)
	}
	decideAcceptance(t, instanceID, map[string]any{
		"status": "changes_requested", "reason": "Needs another pass",
		"rework_target_node_key": "work", "idempotency_key": "runtime-test-rework",
	})

	thirdWorkNode := latestWorkflowNodeForTest(t, instanceID, "work")
	if thirdWorkNode.Attempt != 3 || thirdWorkNode.Status != "active" {
		t.Fatalf("third work node = %#v", thirdWorkNode)
	}
	transitionWorkflowNode(t, uuidToString(thirdWorkNode.ID), "skip", "runtime-test-skip")

	thirdAcceptanceNode := latestWorkflowNodeForTest(t, instanceID, "acceptance")
	if thirdAcceptanceNode.Attempt != 3 || thirdAcceptanceNode.Status != "waiting" {
		t.Fatalf("third acceptance node = %#v", thirdAcceptanceNode)
	}
	decideAcceptance(t, instanceID, map[string]any{
		"status": "approved", "reason": "Accepted", "idempotency_key": "runtime-test-approved",
	})
	completed, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(ctx, db.GetWorkflowInstanceInWorkspaceParams{
		ID: parseUUID(instanceID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil || completed.Status != "completed" {
		t.Fatalf("completed workflow = %#v, err = %v", completed, err)
	}
	var independentHostStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, hostID).Scan(&independentHostStatus); err != nil {
		t.Fatalf("load independent workflow host: %v", err)
	}
	if independentHostStatus != "todo" {
		t.Fatalf("independent workflow host status = %q, want todo", independentHostStatus)
	}
}

func TestCreateWorkflowAtomicAndIdempotent(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	ctx := context.Background()
	cleanupWorkflowRuntimeTest(t)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Atomic workflow",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", ActivityMode: "work",
				Name: "Work", OwnerRole: "owner",
				Executor: workflowdomain.ExecutorDefinition{
					Strategies: []workflowdomain.ExecutorStrategy{
						{Kind: "fixed_role", Role: "owner"}, {Kind: "manual"},
					},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "work_item", Title: "Do {{host.title}}",
					AssigneeRole: "owner", Required: true,
				}},
			},
			{
				Key: "acceptance", Kind: "activity", ActivityMode: "acceptance",
				Name: "Acceptance", OwnerRole: "owner",
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "acceptance"},
			{From: "acceptance", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{
			Policy: "member", ApproverRole: "owner",
			NodeKey: "acceptance", ReworkTargets: []string{"work"},
		},
	}
	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template (workspace_id, name, status, created_by)
		VALUES ($1, 'Atomic test template', 'published', $2)
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
		t.Fatalf("create version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_template SET latest_published_version_id = $1 WHERE id = $2
	`, versionID, templateID); err != nil {
		t.Fatalf("publish template: %v", err)
	}

	body := map[string]any{
		"title": "Atomic workflow host", "template_id": templateID,
		"idempotency_key": "atomic-create-test",
	}
	firstRecorder := httptest.NewRecorder()
	testHandler.CreateWorkflow(
		firstRecorder,
		newRequest(http.MethodPost, "/api/workflow-instances?workspace_id="+testWorkspaceID, body),
	)
	if firstRecorder.Code != http.StatusCreated {
		t.Fatalf("CreateWorkflow status = %d, body = %s", firstRecorder.Code, firstRecorder.Body.String())
	}
	var first workflowInstanceDetailResponse
	if err := json.Unmarshal(firstRecorder.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if first.Instance.Status != "running" || len(first.Tasks) != 1 ||
		first.Tasks[0].IssueID == nil {
		t.Fatalf("unexpected create response: %#v", first)
	}

	secondRecorder := httptest.NewRecorder()
	testHandler.CreateWorkflow(
		secondRecorder,
		newRequest(http.MethodPost, "/api/workflow-instances?workspace_id="+testWorkspaceID, body),
	)
	if secondRecorder.Code != http.StatusOK {
		t.Fatalf("CreateWorkflow replay status = %d, body = %s", secondRecorder.Code, secondRecorder.Body.String())
	}
	var second workflowInstanceDetailResponse
	if err := json.Unmarshal(secondRecorder.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if second.Instance.ID != first.Instance.ID ||
		second.Instance.HostIssueID != first.Instance.HostIssueID {
		t.Fatalf("idempotent replay changed identity: first=%#v second=%#v", first.Instance, second.Instance)
	}
	var hostCount, instanceCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM issue
		WHERE workspace_id = $1 AND title = 'Atomic workflow host'
	`, testWorkspaceID).Scan(&hostCount); err != nil {
		t.Fatalf("count hosts: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM workflow_instance
		WHERE workspace_id = $1
	`, testWorkspaceID).Scan(&instanceCount); err != nil {
		t.Fatalf("count instances: %v", err)
	}
	if hostCount != 1 || instanceCount != 1 {
		t.Fatalf("replay created duplicates: hosts=%d instances=%d", hostCount, instanceCount)
	}
}

func TestWorkflowConcurrentStartCreatesOneActiveInstance(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Concurrent start",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", ActivityMode: "work",
				Name: "Work", OwnerRole: "owner", IssuePolicy: "none",
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("concurrent start definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t,
		"Concurrent start template",
		definition,
	)
	hostID := createWorkflowHostForTest(t, "Concurrent start host")

	type startResult struct {
		status int
		body   string
	}
	results := make(chan startResult, 2)
	for index := range 2 {
		go func(index int) {
			recorder := httptest.NewRecorder()
			request := withURLParam(
				newRequest(
					http.MethodPost,
					"/api/issues/"+hostID+
						"/workflow?workspace_id="+testWorkspaceID,
					map[string]any{
						"template_id": templateID,
						"role_assignments": []map[string]any{{
							"role_key":   "owner",
							"actor_type": "member",
							"actor_id":   testUserID,
						}},
						"idempotency_key": "concurrent-start-" +
							strconv.Itoa(index),
					},
				),
				"id",
				hostID,
			)
			testHandler.StartIssueWorkflow(recorder, request)
			results <- startResult{
				status: recorder.Code,
				body:   recorder.Body.String(),
			}
		}(index)
	}
	statusCounts := map[int]int{}
	for range 2 {
		result := <-results
		statusCounts[result.status]++
		if result.status != http.StatusCreated &&
			result.status != http.StatusConflict {
			t.Fatalf(
				"concurrent start status=%d body=%s",
				result.status,
				result.body,
			)
		}
	}
	if statusCounts[http.StatusCreated] != 1 ||
		statusCounts[http.StatusConflict] != 1 {
		t.Fatalf("concurrent start statuses = %#v", statusCounts)
	}
	var instanceCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM workflow_instance
		WHERE workspace_id = $1 AND host_issue_id = $2
		  AND status IN ('needs_setup', 'running', 'paused')
	`, testWorkspaceID, hostID).Scan(&instanceCount); err != nil {
		t.Fatalf("count concurrent workflow starts: %v", err)
	}
	if instanceCount != 1 {
		t.Fatalf(
			"concurrent starts created %d active instances, want 1",
			instanceCount,
		)
	}
}

func TestWorkflowPauseResumeAndCancelPreserveExistingIssue(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	ctx := context.Background()
	cleanupWorkflowRuntimeTest(t)

	var hostID, instanceID, activeNodeID, pendingNodeID, taskID, issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id,
			number, position
		) VALUES (
			$1, 'Workflow lifecycle host', 'todo', 'none', 'member', $2, $3, 0
		)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create lifecycle host: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_instance (
			workspace_id, template_id, template_version_id, host_issue_id,
			status, host_status_mode, started_by_type, started_by_id
		) VALUES (
			$1, gen_random_uuid(), gen_random_uuid(), $2,
			'running', 'independent', 'member', $3
		)
		RETURNING id
	`, testWorkspaceID, hostID, testUserID).Scan(&instanceID); err != nil {
		t.Fatalf("create lifecycle instance: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_node_instance (
			workspace_id, workflow_instance_id, node_key, node_kind, attempt,
			name_snapshot, display_order, status, activated_at
		) VALUES (
			$1, $2, 'active_work', 'activity', 1,
			'Active work', 1, 'active', now()
		)
		RETURNING id
	`, testWorkspaceID, instanceID).Scan(&activeNodeID); err != nil {
		t.Fatalf("create active lifecycle node: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_node_instance (
			workspace_id, workflow_instance_id, node_key, node_kind, attempt,
			name_snapshot, display_order, status
		) VALUES (
			$1, $2, 'future_work', 'activity', 1,
			'Future work', 2, 'pending'
		)
		RETURNING id
	`, testWorkspaceID, instanceID).Scan(&pendingNodeID); err != nil {
		t.Fatalf("create pending lifecycle node: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_node_task (
			workspace_id, workflow_instance_id, workflow_node_instance_id,
			task_key, source, required, materialization_status, created_by_type
		) VALUES (
			$1, $2, $3, 'work', 'template', true, 'materialized', 'system'
		)
		RETURNING id
	`, testWorkspaceID, instanceID, activeNodeID).Scan(&taskID); err != nil {
		t.Fatalf("create lifecycle task: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id,
			number, position, parent_issue_id, origin_type, origin_id
		) VALUES (
			$1, 'Workflow lifecycle child', 'todo', 'none', 'member', $2,
			$3, 0, $4, 'workflow', $5
		)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t), hostID, taskID).Scan(&issueID); err != nil {
		t.Fatalf("create lifecycle issue: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_node_task SET issue_id = $1 WHERE id = $2
	`, issueID, taskID); err != nil {
		t.Fatalf("bind lifecycle issue: %v", err)
	}

	transitionInstance := func(action, key string) workflowInstanceDetailResponse {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := withURLParam(
			newRequest(
				http.MethodPost,
				"/api/workflow-instances/"+instanceID+"/"+action+
					"?workspace_id="+testWorkspaceID,
				map[string]any{"idempotency_key": key},
			),
			"instanceId",
			instanceID,
		)
		if action == "pause" {
			testHandler.PauseWorkflowInstance(recorder, request)
		} else {
			testHandler.ResumeWorkflowInstance(recorder, request)
		}
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s workflow status = %d, body = %s", action, recorder.Code, recorder.Body.String())
		}
		var response workflowInstanceDetailResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode %s response: %v", action, err)
		}
		return response
	}
	if paused := transitionInstance("pause", "lifecycle-pause"); paused.Instance.Status != "paused" {
		t.Fatalf("paused workflow status = %q", paused.Instance.Status)
	}
	var activeStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM workflow_node_instance WHERE id = $1
	`, activeNodeID).Scan(&activeStatus); err != nil || activeStatus != "active" {
		t.Fatalf("pause changed active node status=%q err=%v", activeStatus, err)
	}
	if resumed := transitionInstance("resume", "lifecycle-resume"); resumed.Instance.Status != "running" {
		t.Fatalf("resumed workflow status = %q", resumed.Instance.Status)
	}

	cancelRecorder := httptest.NewRecorder()
	cancelRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-instances/"+instanceID+"/cancel?workspace_id="+
				testWorkspaceID,
			map[string]any{
				"reason": "No longer needed", "idempotency_key": "lifecycle-cancel",
			},
		),
		"instanceId",
		instanceID,
	)
	testHandler.CancelWorkflowInstance(cancelRecorder, cancelRequest)
	if cancelRecorder.Code != http.StatusOK {
		t.Fatalf(
			"cancel workflow status = %d, body = %s",
			cancelRecorder.Code,
			cancelRecorder.Body.String(),
		)
	}
	var cancelled workflowInstanceDetailResponse
	if err := json.Unmarshal(cancelRecorder.Body.Bytes(), &cancelled); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if cancelled.Instance.Status != "cancelled" {
		t.Fatalf("cancelled workflow status = %q", cancelled.Instance.Status)
	}
	for _, nodeID := range []string{activeNodeID, pendingNodeID} {
		var status string
		if err := testPool.QueryRow(ctx, `
			SELECT status FROM workflow_node_instance WHERE id = $1
		`, nodeID).Scan(&status); err != nil || status != "cancelled" {
			t.Fatalf("cancelled node %s status=%q err=%v", nodeID, status, err)
		}
	}
	var issueStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM issue WHERE id = $1
	`, issueID).Scan(&issueStatus); err != nil || issueStatus != "todo" {
		t.Fatalf("cancel changed existing issue status=%q err=%v", issueStatus, err)
	}
}

func TestWorkflowIssueRelationshipGuardsAndHostCleanup(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	ctx := context.Background()
	cleanupWorkflowRuntimeTest(t)

	var hostID, instanceID, nodeID, requiredTaskID, requiredIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Workflow guard host', 'todo', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create workflow guard host: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_instance (
			workspace_id, template_id, template_version_id, host_issue_id,
			status, host_status_mode, started_by_type, started_by_id
		) VALUES ($1, gen_random_uuid(), gen_random_uuid(), $2, 'running', 'independent', 'member', $3)
		RETURNING id
	`, testWorkspaceID, hostID, testUserID).Scan(&instanceID); err != nil {
		t.Fatalf("create workflow guard instance: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_node_instance (
			workspace_id, workflow_instance_id, node_key, node_kind, attempt,
			name_snapshot, display_order, status, activated_at
		) VALUES ($1, $2, 'work', 'activity', 1, 'Work', 1, 'active', now())
		RETURNING id
	`, testWorkspaceID, instanceID).Scan(&nodeID); err != nil {
		t.Fatalf("create workflow guard node: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_node_task (
			workspace_id, workflow_instance_id, workflow_node_instance_id,
			task_key, source, required, materialization_status, created_by_type
		) VALUES ($1, $2, $3, 'required', 'template', true, 'materialized', 'system')
		RETURNING id
	`, testWorkspaceID, instanceID, nodeID).Scan(&requiredTaskID); err != nil {
		t.Fatalf("create required workflow task: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id,
			number, position, parent_issue_id, origin_type, origin_id, stage
		) VALUES ($1, 'Workflow required child', 'todo', 'none', 'member', $2,
		          $3, 0, $4, 'workflow', $5, 1)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t), hostID, requiredTaskID).Scan(&requiredIssueID); err != nil {
		t.Fatalf("create required workflow issue: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_node_task SET issue_id = $1 WHERE id = $2
	`, requiredIssueID, requiredTaskID); err != nil {
		t.Fatalf("bind required workflow issue: %v", err)
	}

	deleteRequired := httptest.NewRecorder()
	testHandler.DeleteIssue(
		deleteRequired,
		withURLParam(
			newRequest(http.MethodDelete, "/api/issues/"+requiredIssueID+"?workspace_id="+testWorkspaceID, nil),
			"id", requiredIssueID,
		),
	)
	if deleteRequired.Code != http.StatusConflict {
		t.Fatalf("DeleteIssue active required workflow issue status = %d, body = %s", deleteRequired.Code, deleteRequired.Body.String())
	}

	reparentRequired := httptest.NewRecorder()
	testHandler.UpdateIssue(
		reparentRequired,
		withURLParam(
			newRequest(http.MethodPut, "/api/issues/"+requiredIssueID+"?workspace_id="+testWorkspaceID, map[string]any{
				"parent_issue_id": nil,
			}),
			"id", requiredIssueID,
		),
	)
	if reparentRequired.Code != http.StatusConflict {
		t.Fatalf("UpdateIssue active workflow parent status = %d, body = %s", reparentRequired.Code, reparentRequired.Body.String())
	}

	batchDeleteRequired := httptest.NewRecorder()
	testHandler.BatchDeleteIssues(
		batchDeleteRequired,
		newRequest(http.MethodPost, "/api/issues/batch-delete?workspace_id="+testWorkspaceID, map[string]any{
			"issue_ids": []string{requiredIssueID},
		}),
	)
	if batchDeleteRequired.Code != http.StatusConflict {
		t.Fatalf("BatchDeleteIssues active required workflow issue status = %d, body = %s", batchDeleteRequired.Code, batchDeleteRequired.Body.String())
	}

	batchReparentRequired := httptest.NewRecorder()
	testHandler.BatchUpdateIssues(
		batchReparentRequired,
		newRequest(http.MethodPost, "/api/issues/batch-update?workspace_id="+testWorkspaceID, map[string]any{
			"issue_ids": []string{requiredIssueID},
			"updates":   map[string]any{"parent_issue_id": nil},
		}),
	)
	if batchReparentRequired.Code != http.StatusConflict {
		t.Fatalf("BatchUpdateIssues active workflow parent status = %d, body = %s", batchReparentRequired.Code, batchReparentRequired.Body.String())
	}

	detachRequired := changeWorkflowTask(t, requiredTaskID, "detach", "guard-test-detach")
	if detachRequired.IssueID != nil || detachRequired.Required {
		t.Fatalf("detached required workflow task = %#v", detachRequired)
	}
	reparentDetached := httptest.NewRecorder()
	testHandler.UpdateIssue(
		reparentDetached,
		withURLParam(
			newRequest(http.MethodPut, "/api/issues/"+requiredIssueID+"?workspace_id="+testWorkspaceID, map[string]any{
				"parent_issue_id": nil,
			}),
			"id", requiredIssueID,
		),
	)
	if reparentDetached.Code != http.StatusOK {
		t.Fatalf("UpdateIssue detached workflow parent status = %d, body = %s", reparentDetached.Code, reparentDetached.Body.String())
	}

	var optionalTaskID, optionalIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_node_task (
			workspace_id, workflow_instance_id, workflow_node_instance_id,
			task_key, source, required, materialization_status, created_by_type
		) VALUES ($1, $2, $3, 'optional', 'dynamic', false, 'materialized', 'member')
		RETURNING id
	`, testWorkspaceID, instanceID, nodeID).Scan(&optionalTaskID); err != nil {
		t.Fatalf("create optional workflow task: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id,
			number, position, parent_issue_id, origin_type, origin_id, stage
		) VALUES ($1, 'Workflow optional child', 'todo', 'none', 'member', $2,
		          $3, 0, $4, 'workflow', $5, 1)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t), hostID, optionalTaskID).Scan(&optionalIssueID); err != nil {
		t.Fatalf("create optional workflow issue: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_node_task SET issue_id = $1 WHERE id = $2
	`, optionalIssueID, optionalTaskID); err != nil {
		t.Fatalf("bind optional workflow issue: %v", err)
	}

	deleteHost := httptest.NewRecorder()
	testHandler.DeleteIssue(
		deleteHost,
		withURLParam(
			newRequest(http.MethodDelete, "/api/issues/"+hostID+"?workspace_id="+testWorkspaceID, nil),
			"id", hostID,
		),
	)
	if deleteHost.Code != http.StatusNoContent {
		t.Fatalf("DeleteIssue workflow host status = %d, body = %s", deleteHost.Code, deleteHost.Body.String())
	}

	var hostCount, instanceCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE id = $1`, hostID).Scan(&hostCount); err != nil {
		t.Fatalf("count deleted workflow host: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM workflow_instance WHERE id = $1`, instanceID).Scan(&instanceCount); err != nil {
		t.Fatalf("count deleted workflow runtime: %v", err)
	}
	if hostCount != 0 || instanceCount != 0 {
		t.Fatalf("workflow host cleanup left rows: host=%d instance=%d", hostCount, instanceCount)
	}

	var optionalOriginType, optionalParentID *string
	var optionalStage *int32
	if err := testPool.QueryRow(ctx, `
		SELECT origin_type, parent_issue_id::text, stage
		FROM issue WHERE id = $1
	`, optionalIssueID).Scan(&optionalOriginType, &optionalParentID, &optionalStage); err != nil {
		t.Fatalf("load preserved optional workflow issue: %v", err)
	}
	if optionalOriginType != nil || optionalParentID != nil || optionalStage != nil {
		t.Fatalf(
			"preserved workflow issue still bound: origin=%v parent=%v stage=%v",
			optionalOriginType, optionalParentID, optionalStage,
		)
	}
}

func TestWorkflowDAGParallelJoinAndGateway(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	ctx := context.Background()
	cleanupWorkflowRuntimeTest(t)

	var hostID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Workflow DAG host', 'todo', 'high', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create DAG host issue: %v", err)
	}
	ownerExecutor := workflowdomain.ExecutorDefinition{Strategies: []workflowdomain.ExecutorStrategy{
		{Kind: "fixed_role", Role: "owner"}, {Kind: "manual"},
	}}
	requiredIssue := func(key, title string) []workflowdomain.IssueTemplate {
		return []workflowdomain.IssueTemplate{{
			Key: key, Title: title, AssigneeRole: "owner", Required: true,
		}}
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "DAG delivery",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true, AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "analysis", Kind: "activity", Name: "Analysis", OwnerRole: "owner",
				Executor: ownerExecutor, IssuePolicy: "fixed",
				IssueTemplates: requiredIssue("analysis_issue", "Analyze {{host.title}}"),
				SubmissionSchema: &workflowdomain.SubmissionSchema{Fields: []workflowdomain.SubmissionField{{
					Key: "needs_review", Name: "Needs review", Type: "boolean", Required: true,
				}}},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done", SubmissionRequired: true,
				},
			},
			{
				Key: "implementation", Kind: "activity", Name: "Implementation", OwnerRole: "owner",
				Executor: ownerExecutor, IssuePolicy: "fixed",
				IssueTemplates: requiredIssue("implementation_issue", "Implement {{host.title}}"),
				Completion:     workflowdomain.CompletionDefinition{RequiredIssueOutcome: "done"},
			},
			{Key: "join", Kind: "parallel_join", JoinMode: "all", Name: "Join"},
			{Key: "route", Kind: "gateway", Name: "Review route"},
			{
				Key: "review", Kind: "activity", Name: "Review", OwnerRole: "owner",
				Executor: ownerExecutor, IssuePolicy: "fixed",
				IssueTemplates: requiredIssue("review_issue", "Review {{host.title}}"),
				Completion:     workflowdomain.CompletionDefinition{RequiredIssueOutcome: "done"},
			},
			{
				Key: "direct", Kind: "activity", Name: "Direct", OwnerRole: "owner",
				Executor: ownerExecutor, IssuePolicy: "fixed",
				IssueTemplates: requiredIssue("direct_issue", "Direct {{host.title}}"),
				Completion:     workflowdomain.CompletionDefinition{RequiredIssueOutcome: "done"},
			},
			{Key: "review_end", Kind: "end", Name: "Review end"},
			{Key: "direct_end", Kind: "end", Name: "Direct end"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "analysis"},
			{From: "start", To: "implementation"},
			{From: "analysis", To: "join"},
			{From: "implementation", To: "join"},
			{From: "join", To: "route"},
			{
				From: "route", To: "review",
				Condition: json.RawMessage(`{
					"source":"node_submission",
					"node":"analysis",
					"key":"needs_review",
					"op":"eq",
					"value":true
				}`),
			},
			{From: "route", To: "direct", Default: true},
			{From: "review", To: "review_end"},
			{From: "direct", To: "direct_end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("DAG definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(t, "DAG test template", definition)

	startRecorder := httptest.NewRecorder()
	startRequest := withURLParam(newRequest(http.MethodPost, "/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID, map[string]any{
		"template_id":      templateID,
		"host_status_mode": "managed",
		"role_assignments": []map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
		}},
		"idempotency_key": "dag-test-start",
	}), "id", hostID)
	testHandler.StartIssueWorkflow(startRecorder, startRequest)
	if startRecorder.Code != http.StatusCreated {
		t.Fatalf("StartIssueWorkflow DAG status = %d, body = %s", startRecorder.Code, startRecorder.Body.String())
	}
	var started workflowInstanceDetailResponse
	if err := json.Unmarshal(startRecorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode DAG start response: %v", err)
	}
	if len(started.Tasks) != 2 {
		t.Fatalf("DAG start tasks = %#v, want two parallel tasks", started.Tasks)
	}
	var managedHostStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, hostID).Scan(&managedHostStatus); err != nil {
		t.Fatalf("load managed workflow host after start: %v", err)
	}
	if managedHostStatus != "in_progress" {
		t.Fatalf("managed workflow host status after start = %q, want in_progress", managedHostStatus)
	}
	instanceID := started.Instance.ID
	analysisNode := findWorkflowNodeResponse(t, started.Nodes, "analysis", 1)
	implementationNode := findWorkflowNodeResponse(t, started.Nodes, "implementation", 1)
	if analysisNode.Status != "active" || implementationNode.Status != "active" {
		t.Fatalf("parallel activities were not both active: analysis=%#v implementation=%#v", analysisNode, implementationNode)
	}
	analysisIssueID := workflowTaskIssueForNode(t, started.Tasks, analysisNode.ID)
	implementationIssueID := workflowTaskIssueForNode(t, started.Tasks, implementationNode.ID)

	postWorkflowSubmissionPayload(t, analysisNode.ID, "dag-analysis-submission", map[string]any{
		"needs_review": true,
	})
	completeWorkflowIssue(t, analysisIssueID)
	reconcileWorkflowForTest(t, instanceID, "dag-after-analysis")
	analysisAfter := latestWorkflowNodeForTest(t, instanceID, "analysis")
	joinBefore := latestWorkflowNodeForTest(t, instanceID, "join")
	if analysisAfter.Status != "completed" || joinBefore.Status != "pending" {
		t.Fatalf("all join advanced early: analysis=%#v join=%#v", analysisAfter, joinBefore)
	}

	completeWorkflowIssue(t, implementationIssueID)
	reconcileWorkflowForTest(t, instanceID, "dag-after-implementation")
	joinAfter := latestWorkflowNodeForTest(t, instanceID, "join")
	routeAfter := latestWorkflowNodeForTest(t, instanceID, "route")
	reviewAfter := latestWorkflowNodeForTest(t, instanceID, "review")
	directAfter := latestWorkflowNodeForTest(t, instanceID, "direct")
	if joinAfter.Status != "completed" || routeAfter.Status != "completed" ||
		(reviewAfter.Status != "active" && reviewAfter.Status != "waiting") ||
		directAfter.Status != "skipped" {
		t.Fatalf(
			"DAG route state: join=%#v route=%#v review=%#v direct=%#v",
			joinAfter, routeAfter, reviewAfter, directAfter,
		)
	}
	reviewTasks, err := testHandler.Queries.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: reviewAfter.ID, WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil || len(reviewTasks) != 1 || !reviewTasks[0].IssueID.Valid {
		t.Fatalf("review tasks = %#v, err = %v", reviewTasks, err)
	}
	completeWorkflowIssue(t, uuidToString(reviewTasks[0].IssueID))
	reconcileWorkflowForTest(t, instanceID, "dag-after-review")
	completed, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(ctx, db.GetWorkflowInstanceInWorkspaceParams{
		ID: parseUUID(instanceID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil || completed.Status != "completed" {
		t.Fatalf("DAG workflow completion = %#v, err = %v", completed, err)
	}
	if err := testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, hostID).Scan(&managedHostStatus); err != nil {
		t.Fatalf("load managed workflow host after completion: %v", err)
	}
	if managedHostStatus != "done" {
		t.Fatalf("managed workflow host status after completion = %q, want done", managedHostStatus)
	}
	if _, err := testPool.Exec(
		ctx,
		`UPDATE issue SET status = 'in_progress', updated_at = now() WHERE id = $1`,
		hostID,
	); err != nil {
		t.Fatalf("drift managed workflow host status: %v", err)
	}
	if _, err := testPool.Exec(
		ctx,
		`UPDATE workflow_instance
		 SET last_reconciled_at = now() - interval '1 minute'
		 WHERE id = $1`,
		instanceID,
	); err != nil {
		t.Fatalf("make managed workflow repair due: %v", err)
	}
	repaired, repairErr := NewWorkflowReconciler(testHandler).ProcessNext(ctx)
	if repairErr != nil || !repaired {
		t.Fatalf(
			"repair completed workflow host worked=%v err=%v",
			repaired,
			repairErr,
		)
	}
	if err := testPool.QueryRow(
		ctx,
		`SELECT status FROM issue WHERE id = $1`,
		hostID,
	).Scan(&managedHostStatus); err != nil || managedHostStatus != "done" {
		t.Fatalf(
			"repaired managed workflow host status = %q, want done, err=%v",
			managedHostStatus,
			err,
		)
	}
	reviewEnd := latestWorkflowNodeForTest(t, instanceID, "review_end")
	directEnd := latestWorkflowNodeForTest(t, instanceID, "direct_end")
	if reviewEnd.Status != "completed" || directEnd.Status != "skipped" {
		t.Fatalf("DAG end routing: review=%#v direct=%#v", reviewEnd, directEnd)
	}
}

func TestWorkflowDAGAnyJoinDoesNotCancelOtherBranch(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	ctx := context.Background()
	cleanupWorkflowRuntimeTest(t)

	var hostID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Workflow any join host', 'todo', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create any-join host issue: %v", err)
	}
	activity := func(key, title string) workflowdomain.NodeDefinition {
		return workflowdomain.NodeDefinition{
			Key: key, Kind: "activity", Name: title, OwnerRole: "owner",
			IssuePolicy: "fixed",
			Executor: workflowdomain.ExecutorDefinition{Strategies: []workflowdomain.ExecutorStrategy{
				{Kind: "fixed_role", Role: "owner"}, {Kind: "manual"},
			}},
			IssueTemplates: []workflowdomain.IssueTemplate{{
				Key: key + "_issue", Title: title + " {{host.title}}",
				AssigneeRole: "owner", Required: true,
			}},
			Completion: workflowdomain.CompletionDefinition{RequiredIssueOutcome: "done"},
		}
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Any join delivery",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true, AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{Key: "split", Kind: "parallel_split", Name: "Split"},
			activity("fast", "Fast"),
			activity("slow", "Slow"),
			{Key: "join", Kind: "parallel_join", JoinMode: "any", Name: "Any join"},
			activity("followup", "Follow-up"),
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "split"},
			{From: "split", To: "fast"},
			{From: "split", To: "slow"},
			{From: "fast", To: "join"},
			{From: "slow", To: "join"},
			{From: "join", To: "followup"},
			{From: "followup", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	templateID := createPublishedWorkflowTemplateForTest(t, "Any join test template", definition)
	startRecorder := httptest.NewRecorder()
	startRequest := withURLParam(newRequest(http.MethodPost, "/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID, map[string]any{
		"template_id": templateID,
		"role_assignments": []map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
		}},
		"idempotency_key": "any-join-start",
	}), "id", hostID)
	testHandler.StartIssueWorkflow(startRecorder, startRequest)
	if startRecorder.Code != http.StatusCreated {
		t.Fatalf("StartIssueWorkflow any-join status = %d, body = %s", startRecorder.Code, startRecorder.Body.String())
	}
	var started workflowInstanceDetailResponse
	if err := json.Unmarshal(startRecorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode any-join workflow: %v", err)
	}
	instanceID := started.Instance.ID
	fastNode := findWorkflowNodeResponse(t, started.Nodes, "fast", 1)
	slowNode := findWorkflowNodeResponse(t, started.Nodes, "slow", 1)
	fastIssueID := workflowTaskIssueForNode(t, started.Tasks, fastNode.ID)
	slowIssueID := workflowTaskIssueForNode(t, started.Tasks, slowNode.ID)

	completeWorkflowIssue(t, fastIssueID)
	reconcileWorkflowForTest(t, instanceID, "any-join-fast")
	join := latestWorkflowNodeForTest(t, instanceID, "join")
	followup := latestWorkflowNodeForTest(t, instanceID, "followup")
	slow := latestWorkflowNodeForTest(t, instanceID, "slow")
	if join.Status != "completed" ||
		(followup.Status != "active" && followup.Status != "waiting") ||
		(slow.Status != "active" && slow.Status != "waiting") {
		t.Fatalf("any join did not preserve slow branch: join=%#v followup=%#v slow=%#v", join, followup, slow)
	}
	followupTasks, err := testHandler.Queries.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: followup.ID, WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil || len(followupTasks) != 1 || !followupTasks[0].IssueID.Valid {
		t.Fatalf("any-join follow-up tasks = %#v, err = %v", followupTasks, err)
	}
	completeWorkflowIssue(t, uuidToString(followupTasks[0].IssueID))
	reconcileWorkflowForTest(t, instanceID, "any-join-followup")
	running, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(ctx, db.GetWorkflowInstanceInWorkspaceParams{
		ID: parseUUID(instanceID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil || running.Status != "running" {
		t.Fatalf("any-join workflow completed while slow branch remained open: %#v, err=%v", running, err)
	}
	completeWorkflowIssue(t, slowIssueID)
	reconcileWorkflowForTest(t, instanceID, "any-join-slow")
	completed, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(ctx, db.GetWorkflowInstanceInWorkspaceParams{
		ID: parseUUID(instanceID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil || completed.Status != "completed" {
		t.Fatalf("any-join workflow did not complete after slow branch: %#v, err=%v", completed, err)
	}
}

func TestWorkflowManualExecutorPausesAndResumesSetup(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Manual executor setup",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Manual work",
				OwnerRole: "owner", IssuePolicy: "fixed",
				Executor: workflowdomain.ExecutorDefinition{Strategies: []workflowdomain.ExecutorStrategy{
					{Kind: "manual"},
				}},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "manual_task", Title: "Manually assigned {{host.title}}",
					Required: true,
				}},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done",
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"}, {From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("manual executor definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(t, "Manual executor template", definition)
	hostID := createWorkflowHostForTest(t, "Workflow executor manual host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "manual-executor-start")
	if started.Instance.Status != "needs_setup" {
		t.Fatalf("manual executor instance status = %s, want needs_setup", started.Instance.Status)
	}
	work := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
	if work.Status != "blocked" ||
		!jsonContainsWaitingReason(work.WaitingReasons, "executor_needs_setup") {
		t.Fatalf("manual executor node = %#v", work)
	}
	if len(started.Tasks) != 1 || started.Tasks[0].IssueID != nil {
		t.Fatalf("manual executor task materialized before setup: %#v", started.Tasks)
	}
	taskID := started.Tasks[0].ID
	taskRow, err := testHandler.Queries.GetWorkflowNodeTaskInWorkspace(
		ctx,
		db.GetWorkflowNodeTaskInWorkspaceParams{
			ID: parseUUID(taskID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || taskRow.ExecutorResolutionID.Valid {
		t.Fatalf("manual task resolution binding = %#v, err=%v", taskRow, err)
	}
	resolutions, err := testHandler.Queries.ListWorkflowExecutorResolutions(
		ctx,
		db.ListWorkflowExecutorResolutionsParams{
			WorkflowNodeInstanceID: parseUUID(work.ID),
			WorkspaceID:            parseUUID(testWorkspaceID),
		},
	)
	if err != nil || len(resolutions) != 1 ||
		resolutions[0].Strategy != "manual" ||
		resolutions[0].Status != "needs_setup" {
		t.Fatalf("manual executor resolutions = %#v, err=%v", resolutions, err)
	}
	if err := NewWorkflowSweeper(testHandler).SweepOnce(ctx); err != nil {
		t.Fatalf("sweep manual executor diagnostics: %v", err)
	}
	diagnosticsRecorder := httptest.NewRecorder()
	diagnosticsRequest := withURLParam(
		newRequest(
			http.MethodGet,
			"/api/workflow-instances/"+started.Instance.ID+
				"/diagnostics?workspace_id="+testWorkspaceID,
			nil,
		),
		"instanceId",
		started.Instance.ID,
	)
	testHandler.GetWorkflowInstanceDiagnostics(
		diagnosticsRecorder,
		diagnosticsRequest,
	)
	if diagnosticsRecorder.Code != http.StatusOK {
		t.Fatalf(
			"workflow diagnostics status = %d, body = %s",
			diagnosticsRecorder.Code,
			diagnosticsRecorder.Body.String(),
		)
	}
	var diagnostics struct {
		InstanceRevision    int64            `json:"instance_revision"`
		Nodes               []map[string]any `json:"nodes"`
		Events              []map[string]any `json:"events"`
		RecentSweeperEvents []map[string]any `json:"recent_sweeper_events"`
	}
	if err := json.Unmarshal(diagnosticsRecorder.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode workflow diagnostics: %v", err)
	}
	if diagnostics.InstanceRevision < 1 || len(diagnostics.Nodes) != 3 ||
		len(diagnostics.Events) == 0 || len(diagnostics.RecentSweeperEvents) == 0 {
		t.Fatalf("workflow diagnostics = %#v", diagnostics)
	}

	resolveWorkflowExecutor(t, work.ID, taskID, "manual-executor-resolve")
	resumed, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{
			ID: parseUUID(started.Instance.ID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || resumed.Status != "running" {
		t.Fatalf("manual executor instance did not resume: %#v, err=%v", resumed, err)
	}
	resumedNode := latestWorkflowNodeForTest(t, started.Instance.ID, "work")
	if resumedNode.Status != "active" {
		t.Fatalf("manual executor node status = %s, want active", resumedNode.Status)
	}
	taskRow, err = testHandler.Queries.GetWorkflowNodeTaskInWorkspace(
		ctx,
		db.GetWorkflowNodeTaskInWorkspaceParams{
			ID: parseUUID(taskID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || !taskRow.ExecutorResolutionID.Valid || !taskRow.IssueID.Valid {
		t.Fatalf("resolved manual task = %#v, err=%v", taskRow, err)
	}
	var assigneeType *string
	var assigneeID *string
	if err := testPool.QueryRow(ctx, `
		SELECT assignee_type, assignee_id::text FROM issue WHERE id = $1
	`, taskRow.IssueID).Scan(&assigneeType, &assigneeID); err != nil {
		t.Fatalf("load manually assigned issue: %v", err)
	}
	if assigneeType == nil || *assigneeType != "member" ||
		assigneeID == nil || *assigneeID != testUserID {
		t.Fatalf("manual issue assignee = %v/%v", assigneeType, assigneeID)
	}
}

func TestWorkflowPreviousSelectedExecutorUsesValidatedSubmission(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "workflow-previous-selected-agent", nil)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Previous selected executor",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "select", Kind: "activity", Name: "Select executor",
				OwnerRole: "owner", IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{
					Fields: []workflowdomain.SubmissionField{{
						Key: "selected_agent", Name: "Selected agent",
						Type: "agent", Required: true,
					}},
				},
				Completion: workflowdomain.CompletionDefinition{SubmissionRequired: true},
			},
			{
				Key: "work", Kind: "activity", Name: "Selected work",
				OwnerRole: "owner", IssuePolicy: "fixed",
				Executor: workflowdomain.ExecutorDefinition{Strategies: []workflowdomain.ExecutorStrategy{
					{
						Kind: "previous_selected", Node: "select",
						Field: "selected_agent",
					},
					{Kind: "fallback_role", Role: "owner"},
				}},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "selected_task", Title: "Selected {{host.title}}", Required: true,
				}},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done",
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "select"}, {From: "select", To: "work"},
			{From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("previous selected definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t, "Previous selected executor template", definition,
	)
	hostID := createWorkflowHostForTest(t, "Workflow executor previous host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "previous-executor-start")
	selectNode := findWorkflowNodeResponse(t, started.Nodes, "select", 1)
	postWorkflowSubmissionPayload(
		t, selectNode.ID, "previous-executor-submission",
		map[string]any{"selected_agent": agentID},
	)
	reconcileWorkflowForTest(t, started.Instance.ID, "previous-executor-reconcile")

	workNode := latestWorkflowNodeForTest(t, started.Instance.ID, "work")
	tasks, err := testHandler.Queries.ListWorkflowNodeTasks(
		ctx,
		db.ListWorkflowNodeTasksParams{
			WorkflowNodeInstanceID: workNode.ID,
			WorkspaceID:            parseUUID(testWorkspaceID),
		},
	)
	if err != nil || len(tasks) != 1 || !tasks[0].IssueID.Valid {
		t.Fatalf("previous-selected task = %#v, err=%v", tasks, err)
	}
	resolution, err := testHandler.Queries.GetWorkflowExecutorResolutionInWorkspace(
		ctx,
		db.GetWorkflowExecutorResolutionInWorkspaceParams{
			ID: tasks[0].ExecutorResolutionID, WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || resolution.Strategy != "previous_selected" ||
		resolution.Status != "resolved" || uuidToString(resolution.ActorID) != agentID {
		t.Fatalf("previous-selected resolution = %#v, err=%v", resolution, err)
	}
	var assigneeType *string
	var assigneeID *string
	if err := testPool.QueryRow(ctx, `
		SELECT assignee_type, assignee_id::text FROM issue WHERE id = $1
	`, tasks[0].IssueID).Scan(&assigneeType, &assigneeID); err != nil {
		t.Fatalf("load previous-selected issue: %v", err)
	}
	if assigneeType == nil || *assigneeType != "agent" ||
		assigneeID == nil || *assigneeID != agentID {
		t.Fatalf("previous-selected issue assignee = %v/%v", assigneeType, assigneeID)
	}
}

func TestWorkflowCapabilityMatchUsesStructuredEnabledSkill(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "workflow-capability-agent", nil)
	createWorkflowAgentSkillForTest(t, agentID, "release-engineering", true)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Capability executor",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "delivery_pool", Name: "Delivery pool", Required: true,
			AllowedActorTypes: []string{"agent", "squad"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Capability work",
				IssuePolicy: "fixed",
				Executor: workflowdomain.ExecutorDefinition{Strategies: []workflowdomain.ExecutorStrategy{
					{
						Kind: "capability_match", Role: "delivery_pool",
						Capability: "release-engineering",
					},
					{Kind: "manual"},
				}},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "capability_task", Title: "Capability {{host.title}}", Required: true,
				}},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done",
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"}, {From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("capability definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t, "Capability executor template", definition,
	)
	hostID := createWorkflowHostForTest(t, "Workflow executor capability host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "delivery_pool", "actor_type": "agent", "actor_id": agentID,
	}}, "capability-executor-start")
	if started.Instance.Status != "running" || len(started.Tasks) != 1 ||
		started.Tasks[0].IssueID == nil {
		t.Fatalf("capability workflow start = %#v", started)
	}
	task, err := testHandler.Queries.GetWorkflowNodeTaskInWorkspace(
		ctx,
		db.GetWorkflowNodeTaskInWorkspaceParams{
			ID: parseUUID(started.Tasks[0].ID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || !task.ExecutorResolutionID.Valid {
		t.Fatalf("capability task = %#v, err=%v", task, err)
	}
	resolution, err := testHandler.Queries.GetWorkflowExecutorResolutionInWorkspace(
		ctx,
		db.GetWorkflowExecutorResolutionInWorkspaceParams{
			ID: task.ExecutorResolutionID, WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || resolution.Strategy != "capability_match" ||
		resolution.Status != "resolved" || uuidToString(resolution.ActorID) != agentID {
		t.Fatalf("capability resolution = %#v, err=%v", resolution, err)
	}
	var assigneeType *string
	var assigneeID *string
	if err := testPool.QueryRow(ctx, `
		SELECT assignee_type, assignee_id::text FROM issue WHERE id = $1
	`, task.IssueID).Scan(&assigneeType, &assigneeID); err != nil {
		t.Fatalf("load capability issue: %v", err)
	}
	if assigneeType == nil || *assigneeType != "agent" ||
		assigneeID == nil || *assigneeID != agentID {
		t.Fatalf("capability issue assignee = %v/%v", assigneeType, assigneeID)
	}
}

func TestWorkflowDirectExecutorDefaultsAndIssueOverrides(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	defaultAgentID := createHandlerTestAgent(t, "workflow-default-agent", nil)
	overrideAgentID := createHandlerTestAgent(t, "workflow-override-agent", nil)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Direct executor",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Backend development",
				IssuePolicy: "fixed",
				Executor: workflowdomain.ExecutorDefinition{
					Strategies: []workflowdomain.ExecutorStrategy{
						{
							Kind: "fixed_actor", ActorType: "agent",
							ActorID: defaultAgentID,
						},
						{Kind: "manual"},
					},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{
					{
						Key: "implementation", Title: "Implement {{host.title}}",
						Required: true,
					},
					{
						Key: "verification", Title: "Verify {{host.title}}",
						AssigneeType: "agent", AssigneeID: overrideAgentID,
						Required: true,
					},
				},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done",
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("direct executor definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t,
		"Direct executor template",
		definition,
	)
	hostID := createWorkflowHostForTest(t, "Workflow direct executor host")
	started := startWorkflowForTest(
		t,
		hostID,
		templateID,
		nil,
		"direct-executor-start",
	)
	if started.Instance.Status != "running" || len(started.Tasks) != 2 {
		t.Fatalf("direct executor workflow start = %#v", started)
	}
	workNode := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
	participants, err := testHandler.Queries.ListWorkflowNodeParticipants(
		ctx,
		db.ListWorkflowNodeParticipantsParams{
			WorkflowNodeInstanceID: parseUUID(workNode.ID),
			WorkspaceID:            parseUUID(testWorkspaceID),
		},
	)
	if err != nil || len(participants) != 1 ||
		participants[0].Role != "owner" ||
		participants[0].ActorType != "agent" ||
		uuidToString(participants[0].ActorID) != defaultAgentID {
		t.Fatalf("direct node owner participants = %#v, err=%v", participants, err)
	}

	expected := map[string]string{
		"implementation": defaultAgentID,
		"verification":   overrideAgentID,
	}
	for _, task := range started.Tasks {
		if task.IssueID == nil {
			t.Fatalf("task %s was not materialized: %#v", task.TaskKey, task)
		}
		var assigneeType *string
		var assigneeID *string
		if err := testPool.QueryRow(ctx, `
			SELECT assignee_type, assignee_id::text
			FROM issue
			WHERE id = $1
		`, *task.IssueID).Scan(&assigneeType, &assigneeID); err != nil {
			t.Fatalf("load direct executor issue: %v", err)
		}
		if assigneeType == nil || *assigneeType != "agent" ||
			assigneeID == nil || *assigneeID != expected[task.TaskKey] {
			t.Fatalf(
				"task %s assignee = %v/%v, want agent/%s",
				task.TaskKey,
				assigneeType,
				assigneeID,
				expected[task.TaskKey],
			)
		}
	}
}

func TestWorkflowSquadExecutorMaterializationWakesLeader(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	leaderID := createHandlerTestAgent(
		t,
		"Workflow squad executor leader",
		nil,
	)
	squadID := createCommentTriggerPreviewSquad(
		t,
		"Workflow executor squad",
		leaderID,
	)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Squad executor",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"squad"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "owner", IssuePolicy: "fixed",
				Executor: workflowdomain.ExecutorDefinition{
					Strategies: []workflowdomain.ExecutorStrategy{
						{Kind: "fixed_role", Role: "owner"},
						{Kind: "manual"},
					},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "squad_work", Title: "Squad work for {{host.title}}",
					AssigneeRole: "owner", Required: true,
				}},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done",
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("squad executor definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t,
		"Squad executor template",
		definition,
	)
	hostID := createWorkflowHostForTest(t, "Squad executor host")
	started := startWorkflowForTest(
		t,
		hostID,
		templateID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "squad",
			"actor_id": squadID,
		}},
		"squad-executor-start",
	)
	work := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
	issueID := workflowTaskIssueForNode(t, started.Tasks, work.ID)
	var assigneeType, assigneeID string
	if err := testPool.QueryRow(ctx, `
		SELECT assignee_type, assignee_id::text
		FROM issue WHERE id = $1
	`, issueID).Scan(&assigneeType, &assigneeID); err != nil {
		t.Fatalf("load squad workflow issue: %v", err)
	}
	if assigneeType != "squad" || assigneeID != squadID {
		t.Fatalf(
			"squad workflow issue assignee=%s/%s want squad/%s",
			assigneeType,
			assigneeID,
			squadID,
		)
	}
	var leaderTaskCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task_queue
		WHERE issue_id = $1
		  AND agent_id = $2
		  AND is_leader_task = true
		  AND status IN ('queued', 'dispatched', 'running')
	`, issueID, leaderID).Scan(&leaderTaskCount); err != nil {
		t.Fatalf("count workflow squad leader tasks: %v", err)
	}
	if leaderTaskCount != 1 {
		t.Fatalf(
			"workflow squad materialization queued %d leader tasks, want 1",
			leaderTaskCount,
		)
	}
}

func TestWorkflowAgentSubmissionAndVerdictRemainControlledSuggestion(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "workflow-suggestion-agent", nil)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Controlled agent suggestion",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "worker", Name: "Worker", Required: true,
			AllowedActorTypes: []string{"agent"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Agent work",
				OwnerRole: "worker", IssuePolicy: "fixed",
				Executor: workflowdomain.ExecutorDefinition{Strategies: []workflowdomain.ExecutorStrategy{
					{Kind: "fixed_role", Role: "worker"}, {Kind: "manual"},
				}},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "agent_task", Title: "Agent {{host.title}}",
					AssigneeRole: "worker", Required: true,
				}},
				SubmissionSchema: &workflowdomain.SubmissionSchema{
					Fields: []workflowdomain.SubmissionField{{
						Key: "result", Name: "Result", Type: "text", Required: true,
					}},
				},
				Verdict: &workflowdomain.VerdictDefinition{
					Evaluator: "member", RequiredResult: "pass",
				},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done", SubmissionRequired: true,
					VerdictRequired: "pass",
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"}, {From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("controlled suggestion definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t, "Controlled agent suggestion template", definition,
	)
	hostID := createWorkflowHostForTest(t, "Workflow executor agent suggestion host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "worker", "actor_type": "agent", "actor_id": agentID,
	}}, "agent-suggestion-start")
	work := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
	issueID := workflowTaskIssueForNode(t, started.Tasks, work.ID)
	var agentTaskID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent_task_queue
		WHERE issue_id = $1 AND agent_id = $2
		ORDER BY created_at DESC LIMIT 1
	`, issueID, agentID).Scan(&agentTaskID); err != nil {
		t.Fatalf("load workflow agent task: %v", err)
	}

	submissionRecorder := httptest.NewRecorder()
	submissionRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+work.ID+"/submissions?workspace_id="+testWorkspaceID,
			map[string]any{
				"payload": map[string]any{"result": "agent result"},
				"summary": "Agent result", "source_issue_id": issueID,
				"idempotency_key": "agent-suggestion-submission",
			},
		),
		"nodeInstanceId",
		work.ID,
	)
	submissionRequest.Header.Set("X-Agent-ID", agentID)
	submissionRequest.Header.Set("X-Task-ID", agentTaskID)
	testHandler.CreateWorkflowNodeSubmission(submissionRecorder, submissionRequest)
	if submissionRecorder.Code != http.StatusCreated {
		t.Fatalf(
			"agent submission status = %d, body = %s",
			submissionRecorder.Code,
			submissionRecorder.Body.String(),
		)
	}
	var submissionResponse struct {
		Submission workflowSubmissionResponse `json:"submission"`
	}
	if err := json.Unmarshal(
		submissionRecorder.Body.Bytes(),
		&submissionResponse,
	); err != nil {
		t.Fatalf("decode agent submission: %v", err)
	}
	if submissionResponse.Submission.SubmittedByType != "agent" ||
		submissionResponse.Submission.SubmittedByID == nil ||
		*submissionResponse.Submission.SubmittedByID != agentID {
		t.Fatalf("agent submission actor = %#v", submissionResponse.Submission)
	}

	verdictRecorder := httptest.NewRecorder()
	verdictRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+work.ID+"/verdicts?workspace_id="+testWorkspaceID,
			map[string]any{
				"result": "pass", "reason": "Agent recommends pass",
				"evidence": []any{}, "confidence": 0.9,
				"idempotency_key": "agent-suggestion-verdict",
			},
		),
		"nodeInstanceId",
		work.ID,
	)
	verdictRequest.Header.Set("X-Agent-ID", agentID)
	verdictRequest.Header.Set("X-Task-ID", agentTaskID)
	testHandler.CreateWorkflowNodeVerdict(verdictRecorder, verdictRequest)
	if verdictRecorder.Code != http.StatusCreated {
		t.Fatalf(
			"agent verdict status = %d, body = %s",
			verdictRecorder.Code,
			verdictRecorder.Body.String(),
		)
	}
	var verdictResponse struct {
		Verdict workflowVerdictResponse `json:"verdict"`
	}
	if err := json.Unmarshal(verdictRecorder.Body.Bytes(), &verdictResponse); err != nil {
		t.Fatalf("decode agent verdict: %v", err)
	}
	if verdictResponse.Verdict.EvaluatorType != "agent" ||
		verdictResponse.Verdict.EvaluatorID == nil ||
		*verdictResponse.Verdict.EvaluatorID != agentID {
		t.Fatalf("agent verdict actor = %#v", verdictResponse.Verdict)
	}
	currentNode := latestWorkflowNodeForTest(t, started.Instance.ID, "work")
	if currentNode.LatestVerdictID.Valid {
		t.Fatalf(
			"agent suggestion became current workflow truth: %s",
			uuidToString(currentNode.LatestVerdictID),
		)
	}
}

func TestWorkflowManualActivityWaitsForExplicitMemberCompletion(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Manual activity",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "manual", Kind: "activity", Name: "Manual review",
				OwnerRole: "owner", IssuePolicy: "none",
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "manual"}, {From: "manual", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("manual definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t,
		"Manual activity template",
		definition,
	)
	hostID := createWorkflowHostForTest(t, "Manual activity host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "manual-activity-start")
	manual := findWorkflowNodeResponse(t, started.Nodes, "manual", 1)

	reconcileWorkflowForTest(t, started.Instance.ID, "manual-activity-before-complete")
	waiting := latestWorkflowNodeForTest(t, started.Instance.ID, "manual")
	if waiting.Status != "waiting" {
		t.Fatalf("manual node status = %s, want waiting", waiting.Status)
	}
	if !strings.Contains(string(waiting.WaitingReasons), "manual_completion_required") {
		t.Fatalf("manual waiting reasons = %s", waiting.WaitingReasons)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+manual.ID+"/complete?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason":          "Manual review completed",
				"idempotency_key": "manual-activity-complete",
			},
		),
		"nodeInstanceId",
		manual.ID,
	)
	testHandler.CompleteWorkflowNode(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("manual complete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	completed, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{
			ID: parseUUID(started.Instance.ID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || completed.Status != "completed" {
		t.Fatalf("manual instance = %#v, err=%v", completed, err)
	}
	verdicts, err := testHandler.Queries.ListWorkflowVerdicts(
		ctx,
		db.ListWorkflowVerdictsParams{
			WorkflowNodeInstanceID: parseUUID(manual.ID),
			WorkspaceID:            parseUUID(testWorkspaceID),
		},
	)
	if err != nil || len(verdicts) != 1 ||
		verdicts[0].EvaluatorType != "member" ||
		!strings.Contains(string(verdicts[0].Basis), "manual_completion") {
		t.Fatalf("manual verdicts = %#v, err=%v", verdicts, err)
	}
}

func TestWorkflowDeterministicVerdictReevaluatesStructuredCondition(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Deterministic verdict",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "decision", Kind: "activity", Name: "Decision",
				OwnerRole: "owner", IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{
					Policy: "single",
					Fields: []workflowdomain.SubmissionField{{
						Key: "approved", Name: "Approved", Type: "boolean", Required: true,
					}},
				},
				Verdict: &workflowdomain.VerdictDefinition{
					Evaluator: "deterministic", RequiredResult: "pass",
					Condition: json.RawMessage(
						`{"source":"node_submission","node":"decision","key":"approved","op":"eq","value":true}`,
					),
				},
				Completion: workflowdomain.CompletionDefinition{
					SubmissionRequired: true, VerdictRequired: "pass",
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "decision"}, {From: "decision", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("deterministic definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t, "Deterministic verdict template", definition,
	)
	hostID := createWorkflowHostForTest(t, "Deterministic verdict host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "deterministic-verdict-start")
	decision := findWorkflowNodeResponse(t, started.Nodes, "decision", 1)

	postWorkflowSubmissionPayload(
		t,
		decision.ID,
		"deterministic-verdict-fail",
		map[string]any{"approved": false},
	)
	reconcileWorkflowForTest(t, started.Instance.ID, "deterministic-verdict-fail-reconcile")
	waiting := latestWorkflowNodeForTest(t, started.Instance.ID, "decision")
	if waiting.Status != "waiting" ||
		!strings.Contains(string(waiting.WaitingReasons), "verdict_not_passed") {
		t.Fatalf("deterministic fail node = %#v", waiting)
	}

	postWorkflowSubmissionPayload(
		t,
		decision.ID,
		"deterministic-verdict-pass",
		map[string]any{"approved": true},
	)
	reconcileWorkflowForTest(t, started.Instance.ID, "deterministic-verdict-pass-reconcile")
	instance, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{
			ID: parseUUID(started.Instance.ID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || instance.Status != "completed" {
		t.Fatalf("deterministic instance = %#v, err=%v", instance, err)
	}
	verdicts, err := testHandler.Queries.ListWorkflowVerdicts(
		ctx,
		db.ListWorkflowVerdictsParams{
			WorkflowNodeInstanceID: parseUUID(decision.ID),
			WorkspaceID:            parseUUID(testWorkspaceID),
		},
	)
	if err != nil || len(verdicts) != 2 ||
		verdicts[0].Result != "pass" || verdicts[1].Result != "fail" {
		t.Fatalf("deterministic verdicts = %#v, err=%v", verdicts, err)
	}
}

func TestWorkflowRequiredIssueCancellationPolicy(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)

	for _, test := range []struct {
		name           string
		policy         string
		wantInstance   string
		wantNode       string
		wantReasonCode string
	}{
		{
			name:   "done policy blocks",
			policy: "done", wantInstance: "running", wantNode: "blocked",
			wantReasonCode: "required_issue_cancelled",
		},
		{
			name:   "terminal policy accepts cancellation",
			policy: "terminal", wantInstance: "completed", wantNode: "completed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cleanupWorkflowRuntimeTest(t)
			ctx := context.Background()
			definition := workflowdomain.Definition{
				SchemaVersion: workflowdomain.DefinitionSchemaVersion,
				Name:          "Cancellation " + test.policy,
				AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
				Roles: []workflowdomain.RoleDefinition{{
					Key: "owner", Name: "Owner", Required: true,
					AllowedActorTypes: []string{"member"},
				}},
				Nodes: []workflowdomain.NodeDefinition{
					{Key: "start", Kind: "start", Name: "Start"},
					{
						Key: "work", Kind: "activity", Name: "Work",
						OwnerRole: "owner", IssuePolicy: "fixed",
						Executor: workflowdomain.ExecutorDefinition{
							Strategies: []workflowdomain.ExecutorStrategy{
								{Kind: "fixed_role", Role: "owner"},
								{Kind: "manual"},
							},
						},
						IssueTemplates: []workflowdomain.IssueTemplate{{
							Key: "required_work", Title: "Work on {{host.title}}",
							AssigneeRole: "owner", Required: true,
						}},
						Completion: workflowdomain.CompletionDefinition{
							RequiredIssueOutcome: test.policy,
						},
					},
					{Key: "end", Kind: "end", Name: "End"},
				},
				Edges: []workflowdomain.EdgeDefinition{
					{From: "start", To: "work"},
					{From: "work", To: "end"},
				},
			}
			if err := workflowdomain.ValidateDefinition(definition); err != nil {
				t.Fatalf("cancellation definition invalid: %v", err)
			}
			templateID := createPublishedWorkflowTemplateForTest(
				t,
				"Cancellation "+test.policy,
				definition,
			)
			hostID := createWorkflowHostForTest(
				t,
				"Cancellation "+test.policy+" host",
			)
			started := startWorkflowForTest(
				t,
				hostID,
				templateID,
				[]map[string]any{{
					"role_key": "owner", "actor_type": "member",
					"actor_id": testUserID,
				}},
				"cancellation-"+test.policy+"-start",
			)
			work := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
			issueID := workflowTaskIssueForNode(t, started.Tasks, work.ID)
			if _, err := testPool.Exec(
				ctx,
				`UPDATE issue SET status = 'cancelled' WHERE id = $1`,
				issueID,
			); err != nil {
				t.Fatalf("cancel required issue: %v", err)
			}
			reconcileWorkflowForTest(
				t,
				started.Instance.ID,
				"cancellation-"+test.policy+"-reconcile",
			)

			instance, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
				ctx,
				db.GetWorkflowInstanceInWorkspaceParams{
					ID:          parseUUID(started.Instance.ID),
					WorkspaceID: parseUUID(testWorkspaceID),
				},
			)
			if err != nil || instance.Status != test.wantInstance {
				t.Fatalf(
					"instance status=%q want=%q err=%v",
					instance.Status,
					test.wantInstance,
					err,
				)
			}
			current := latestWorkflowNodeForTest(t, started.Instance.ID, "work")
			if current.Status != test.wantNode {
				t.Fatalf(
					"node status=%q want=%q reasons=%s",
					current.Status,
					test.wantNode,
					current.WaitingReasons,
				)
			}
			if test.wantReasonCode != "" &&
				!jsonContainsWaitingReason(
					current.WaitingReasons,
					test.wantReasonCode,
				) {
				t.Fatalf(
					"waiting reasons=%s want code=%q",
					current.WaitingReasons,
					test.wantReasonCode,
				)
			}
		})
	}
}

func TestWorkflowBlockedVerdictBlocksNodeUntilPassingRevision(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Blocked verdict",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "review", Kind: "activity", Name: "Review",
				OwnerRole: "owner", IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{
					Policy: "single",
					Fields: []workflowdomain.SubmissionField{{
						Key: "result", Name: "Result", Type: "text", Required: true,
					}},
				},
				Verdict: &workflowdomain.VerdictDefinition{
					Evaluator: "member", RequiredResult: "pass",
				},
				Completion: workflowdomain.CompletionDefinition{
					SubmissionRequired: true,
					VerdictRequired:    "pass",
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "review"},
			{From: "review", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("blocked verdict definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t,
		"Blocked verdict template",
		definition,
	)
	hostID := createWorkflowHostForTest(t, "Blocked verdict host")
	started := startWorkflowForTest(
		t,
		hostID,
		templateID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member",
			"actor_id": testUserID,
		}},
		"blocked-verdict-start",
	)
	review := findWorkflowNodeResponse(t, started.Nodes, "review", 1)
	postWorkflowSubmissionPayload(
		t,
		review.ID,
		"blocked-verdict-submission",
		map[string]any{"result": "Needs external approval"},
	)
	postWorkflowVerdictResult(
		t,
		review.ID,
		"blocked",
		"Waiting for compliance approval",
		"blocked-verdict-blocked",
	)

	blocked := latestWorkflowNodeForTest(t, started.Instance.ID, "review")
	if blocked.Status != "blocked" ||
		!jsonContainsWaitingReason(blocked.WaitingReasons, "verdict_blocked") {
		t.Fatalf(
			"blocked verdict node status=%q reasons=%s",
			blocked.Status,
			blocked.WaitingReasons,
		)
	}
	instance, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{
			ID:          parseUUID(started.Instance.ID),
			WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || instance.Status != "running" {
		t.Fatalf("blocked verdict instance=%#v err=%v", instance, err)
	}

	postWorkflowVerdictResult(
		t,
		review.ID,
		"pass",
		"Compliance approved",
		"blocked-verdict-pass",
	)
	completed, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{
			ID:          parseUUID(started.Instance.ID),
			WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || completed.Status != "completed" {
		t.Fatalf("passing revision instance=%#v err=%v", completed, err)
	}
}

func TestWorkflowSubmissionReplayAfterNodeCompletion(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Submission replay",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "submit", Kind: "activity", Name: "Submit",
				OwnerRole: "owner", IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{
					Policy: "single",
					Fields: []workflowdomain.SubmissionField{{
						Key: "result", Name: "Result", Type: "text", Required: true,
					}},
				},
				Completion: workflowdomain.CompletionDefinition{
					SubmissionRequired: true,
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "submit"},
			{From: "submit", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("submission replay definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t,
		"Submission replay template",
		definition,
	)
	hostID := createWorkflowHostForTest(t, "Submission replay host")
	started := startWorkflowForTest(
		t,
		hostID,
		templateID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member",
			"actor_id": testUserID,
		}},
		"submission-replay-start",
	)
	submit := findWorkflowNodeResponse(t, started.Nodes, "submit", 1)
	postSubmission(
		t,
		submit.ID,
		"submission-replay-completed-node",
		"Delivered",
	)

	instance, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{
			ID:          parseUUID(started.Instance.ID),
			WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || instance.Status != "completed" {
		t.Fatalf("submission replay instance=%#v err=%v", instance, err)
	}
	var submissionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM workflow_node_submission
		WHERE workflow_node_instance_id = $1
	`, submit.ID).Scan(&submissionCount); err != nil {
		t.Fatalf("count replayed submissions: %v", err)
	}
	if submissionCount != 1 {
		t.Fatalf(
			"submission replay created %d revisions, want 1",
			submissionCount,
		)
	}
}

func TestWorkflowInstanceListPersonalizesInterventionsAndCursorOrder(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	var viewerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Workflow list viewer', 'workflow-list-viewer@multica.ai')
		RETURNING id
	`).Scan(&viewerID); err != nil {
		t.Fatalf("create workflow list viewer: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, viewerID); err != nil {
		t.Fatalf("add workflow list viewer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(
			context.Background(),
			`DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`,
			testWorkspaceID,
			viewerID,
		)
		_, _ = testPool.Exec(
			context.Background(),
			`DELETE FROM "user" WHERE id = $1`,
			viewerID,
		)
	})

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Personalized list",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "owner", IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{
					Policy: "single",
					Fields: []workflowdomain.SubmissionField{{
						Key: "result", Name: "Result", Type: "text", Required: true,
					}},
				},
				Completion: workflowdomain.CompletionDefinition{
					SubmissionRequired: true,
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("personalized list definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t,
		"Personalized list template",
		definition,
	)
	actionableHostID := createWorkflowHostForTest(
		t,
		"Viewer-owned workflow",
	)
	actionable := startWorkflowForTest(
		t,
		actionableHostID,
		templateID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member",
			"actor_id": viewerID,
		}},
		"personalized-list-actionable",
	)
	reconcileWorkflowForTest(
		t,
		actionable.Instance.ID,
		"personalized-list-actionable-reconcile",
	)
	unrelatedHostID := createWorkflowHostForTest(
		t,
		"Unrelated newer workflow",
	)
	unrelated := startWorkflowForTest(
		t,
		unrelatedHostID,
		templateID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member",
			"actor_id": testUserID,
		}},
		"personalized-list-unrelated",
	)
	reconcileWorkflowForTest(
		t,
		unrelated.Instance.ID,
		"personalized-list-unrelated-reconcile",
	)

	type listResponse struct {
		Instances  []workflowInstanceResponse `json:"instances"`
		Total      int64                      `json:"total"`
		NextCursor *string                    `json:"next_cursor"`
	}
	list := func(query string) listResponse {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := newRequestAs(
			viewerID,
			http.MethodGet,
			"/api/workflow-instances?workspace_id="+testWorkspaceID+query,
			nil,
		)
		testHandler.ListWorkflowInstances(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf(
				"ListWorkflowInstances status=%d body=%s",
				recorder.Code,
				recorder.Body.String(),
			)
		}
		var response listResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode workflow instance list: %v", err)
		}
		return response
	}

	actionableOnly := list(
		"&status=active&intervention_type=submit_result&limit=10",
	)
	if actionableOnly.Total != 1 ||
		len(actionableOnly.Instances) != 1 ||
		actionableOnly.Instances[0].ID != actionable.Instance.ID ||
		actionableOnly.Instances[0].NextAction != "submit_result" {
		t.Fatalf("actionable personalized list = %#v", actionableOnly)
	}
	healthyOnly := list(
		"&status=active&intervention_type=view_current_activity&limit=10",
	)
	if healthyOnly.Total != 1 ||
		len(healthyOnly.Instances) != 1 ||
		healthyOnly.Instances[0].ID != unrelated.Instance.ID ||
		healthyOnly.Instances[0].NextAction != "view_current_activity" {
		t.Fatalf("healthy personalized list = %#v", healthyOnly)
	}

	firstPage := list("&status=active&limit=1")
	if firstPage.Total != 2 ||
		len(firstPage.Instances) != 1 ||
		firstPage.Instances[0].ID != actionable.Instance.ID ||
		firstPage.NextCursor == nil {
		t.Fatalf("first personalized page = %#v", firstPage)
	}
	secondPage := list(
		"&status=active&limit=1&cursor=" + *firstPage.NextCursor,
	)
	if secondPage.Total != 2 ||
		len(secondPage.Instances) != 1 ||
		secondPage.Instances[0].ID != unrelated.Instance.ID ||
		secondPage.NextCursor != nil {
		t.Fatalf("second personalized page = %#v", secondPage)
	}
}

func TestWorkflowConcurrentReconcilersCommitOneTransition(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Concurrent reconcile",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "owner", IssuePolicy: "fixed",
				Executor: workflowdomain.ExecutorDefinition{
					Strategies: []workflowdomain.ExecutorStrategy{
						{Kind: "fixed_role", Role: "owner"},
						{Kind: "manual"},
					},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "work", Title: "Complete {{host.title}}",
					AssigneeRole: "owner", Required: true,
				}},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done",
				},
			},
			{
				Key: "review", Kind: "activity", ActivityMode: "work",
				Name: "Review", OwnerRole: "owner", IssuePolicy: "none",
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "review"},
			{From: "review", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("concurrent reconcile definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t,
		"Concurrent reconcile template",
		definition,
	)
	hostID := createWorkflowHostForTest(t, "Concurrent reconcile host")
	started := startWorkflowForTest(
		t,
		hostID,
		templateID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member",
			"actor_id": testUserID,
		}},
		"concurrent-reconcile-start",
	)
	workNode := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
	issueID := workflowTaskIssueForNode(t, started.Tasks, workNode.ID)
	if _, err := testPool.Exec(
		ctx,
		`UPDATE issue SET status = 'done', updated_at = now() WHERE id = $1`,
		issueID,
	); err != nil {
		t.Fatalf("complete concurrent reconcile issue: %v", err)
	}

	results := make(chan error, 2)
	for index := range 2 {
		go func(index int) {
			_, err := testHandler.reconcileWorkflowInstance(
				ctx,
				parseUUID(testWorkspaceID),
				parseUUID(started.Instance.ID),
				"system",
				pgtype.UUID{},
				"concurrent-reconcile-"+strconv.Itoa(index),
			)
			results <- err
		}(index)
	}
	for range 2 {
		err := <-results
		if err != nil && !errors.Is(err, errWorkflowNoop) {
			t.Fatalf("concurrent reconcile: %v", err)
		}
	}

	completedWork := latestWorkflowNodeForTest(
		t,
		started.Instance.ID,
		"work",
	)
	currentReview := latestWorkflowNodeForTest(
		t,
		started.Instance.ID,
		"review",
	)
	if completedWork.Status != "completed" ||
		currentReview.Attempt != 1 ||
		currentReview.Status != "waiting" {
		t.Fatalf(
			"concurrent transition work=%#v review=%#v",
			completedWork,
			currentReview,
		)
	}
	var completionEvents int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM workflow_event
		WHERE workflow_instance_id = $1
		  AND workflow_node_instance_id = $2
		  AND event_type = 'node.completed'
	`, started.Instance.ID, workNode.ID).Scan(&completionEvents); err != nil {
		t.Fatalf("count concurrent completion events: %v", err)
	}
	if completionEvents != 1 {
		t.Fatalf(
			"concurrent reconcilers wrote %d completion events, want 1",
			completionEvents,
		)
	}
}

func TestWorkflowPerRequiredTaskSubmissionPolicy(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Per task submissions",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Parallel work",
				OwnerRole: "owner", IssuePolicy: "fixed",
				Executor: workflowdomain.ExecutorDefinition{Strategies: []workflowdomain.ExecutorStrategy{
					{Kind: "fixed_role", Role: "owner"}, {Kind: "manual"},
				}},
				IssueTemplates: []workflowdomain.IssueTemplate{
					{Key: "first", Title: "First {{host.title}}", Required: true},
					{Key: "second", Title: "Second {{host.title}}", Required: true},
				},
				SubmissionSchema: &workflowdomain.SubmissionSchema{
					Policy: "per_required_task",
					Fields: []workflowdomain.SubmissionField{{
						Key: "result", Name: "Result", Type: "text", Required: true,
					}},
				},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done", SubmissionRequired: true,
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"}, {From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("per-task definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t, "Per-task submissions template", definition,
	)
	hostID := createWorkflowHostForTest(t, "Per-task submissions host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "per-task-submission-start")
	work := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
	if len(started.Tasks) != 2 {
		t.Fatalf("per-task workflow tasks = %#v", started.Tasks)
	}
	issueIDs := make([]string, 0, 2)
	for _, task := range started.Tasks {
		if task.WorkflowNodeInstanceID == work.ID && task.IssueID != nil {
			issueIDs = append(issueIDs, *task.IssueID)
		}
	}
	if len(issueIDs) != 2 {
		t.Fatalf("per-task issue ids = %#v", issueIDs)
	}
	if _, err := testPool.Exec(
		ctx,
		`UPDATE issue SET status = 'done' WHERE id = ANY($1::uuid[])`,
		issueIDs,
	); err != nil {
		t.Fatalf("complete per-task issues: %v", err)
	}

	postSubmission := func(key, issueID string) {
		recorder := httptest.NewRecorder()
		request := withURLParam(
			newRequest(
				http.MethodPost,
				"/api/workflow-node-instances/"+work.ID+"/submissions?workspace_id="+testWorkspaceID,
				map[string]any{
					"payload":         map[string]any{"result": key},
					"source_issue_id": issueID,
					"idempotency_key": key,
				},
			),
			"nodeInstanceId",
			work.ID,
		)
		testHandler.CreateWorkflowNodeSubmission(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("post per-task submission status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
	}
	postSubmission("per-task-first", issueIDs[0])
	reconcileWorkflowForTest(t, started.Instance.ID, "per-task-first-reconcile")
	waiting := latestWorkflowNodeForTest(t, started.Instance.ID, "work")
	if waiting.Status != "waiting" ||
		!strings.Contains(string(waiting.WaitingReasons), "task_submission_required") {
		t.Fatalf("per-task waiting node = %#v", waiting)
	}

	postSubmission("per-task-second", issueIDs[1])
	reconcileWorkflowForTest(t, started.Instance.ID, "per-task-second-reconcile")
	instance, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{
			ID: parseUUID(started.Instance.ID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || instance.Status != "completed" {
		t.Fatalf("per-task instance = %#v, err=%v", instance, err)
	}
}

func TestWorkflowMissingRoleCreatesInboxAction(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Needs setup",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "owner",
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("needs setup definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t, "Needs setup template", definition,
	)
	hostID := createWorkflowHostForTest(t, "Workflow needs setup host")
	started := startWorkflowForTest(
		t, hostID, templateID, nil, "needs-setup-inbox-start",
	)
	if started.Instance.Status != "needs_setup" {
		t.Fatalf("workflow status = %q, want needs_setup", started.Instance.Status)
	}

	var count int
	var details []byte
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) OVER (), details
		FROM inbox_item
		WHERE workspace_id = $1
		  AND issue_id = $2
		  AND recipient_type = 'member'
		  AND recipient_id = $3
		  AND type = 'workflow_action_required'
		  AND severity = 'action_required'
		  AND details->>'reason' = 'needs_setup'
		LIMIT 1
	`, testWorkspaceID, hostID, testUserID).Scan(&count, &details); err != nil {
		t.Fatalf("load needs-setup inbox action: %v", err)
	}
	if count != 1 || !strings.Contains(string(details), `"owner"`) {
		t.Fatalf("needs-setup inbox count=%d details=%s", count, details)
	}

	roleBody := map[string]any{
		"role_assignments": []map[string]any{{
			"role_key": "owner", "actor_type": "member",
			"actor_id": testUserID,
		}},
		"idempotency_key": "needs-setup-role-replay",
	}
	updateRoles := func() (int, workflowInstanceDetailResponse, string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := withURLParam(
			newRequest(
				http.MethodPost,
				"/api/workflow-instances/"+started.Instance.ID+
					"/roles?workspace_id="+testWorkspaceID,
				roleBody,
			),
			"instanceId",
			started.Instance.ID,
		)
		testHandler.UpdateWorkflowInstanceRoles(recorder, request)
		var response workflowInstanceDetailResponse
		if recorder.Code == http.StatusOK {
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode workflow role update: %v", err)
			}
		}
		return recorder.Code, response, recorder.Body.String()
	}
	firstStatus, firstRoles, firstBody := updateRoles()
	if firstStatus != http.StatusOK ||
		firstRoles.Instance.Status != "running" {
		t.Fatalf(
			"first role update status=%d instance=%#v body=%s",
			firstStatus,
			firstRoles.Instance,
			firstBody,
		)
	}
	replayStatus, replayRoles, replayBody := updateRoles()
	if replayStatus != http.StatusOK ||
		replayRoles.Instance.ID != firstRoles.Instance.ID ||
		replayRoles.Instance.Revision != firstRoles.Instance.Revision {
		t.Fatalf(
			"replayed role update status=%d instance=%#v body=%s",
			replayStatus,
			replayRoles.Instance,
			replayBody,
		)
	}
}

func TestWorkflowNodeOwnerCanCompleteButOnlyAdminCanRollback(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	var ownerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Workflow node owner', 'workflow-node-owner@multica.ai')
		RETURNING id
	`).Scan(&ownerID); err != nil {
		t.Fatalf("create workflow node owner: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, ownerID); err != nil {
		t.Fatalf("add workflow node owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(
			context.Background(),
			`DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`,
			testWorkspaceID,
			ownerID,
		)
		_, _ = testPool.Exec(
			context.Background(),
			`DELETE FROM "user" WHERE id = $1`,
			ownerID,
		)
	})

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Owner rollback",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "owner",
			},
			{
				Key: "review", Kind: "activity", Name: "Review",
				OwnerRole: "owner",
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "review"},
			{From: "review", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("owner rollback definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowTemplateForTest(
		t, "Owner rollback template", definition,
	)
	hostID := createWorkflowHostForTest(t, "Workflow owner rollback host")
	started := startWorkflowForTest(
		t,
		hostID,
		templateID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": ownerID,
		}},
		"owner-rollback-start",
	)
	work := findWorkflowNodeResponse(t, started.Nodes, "work", 1)

	complete := httptest.NewRecorder()
	completeRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+work.ID+
				"/complete?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason":          "Owner completed the activity",
				"idempotency_key": "owner-complete-work",
			},
		),
		"nodeInstanceId",
		work.ID,
	)
	completeRequest.Header.Set("X-User-ID", ownerID)
	testHandler.CompleteWorkflowNode(complete, completeRequest)
	if complete.Code != http.StatusOK {
		t.Fatalf(
			"node owner complete status = %d, body = %s",
			complete.Code,
			complete.Body.String(),
		)
	}
	review := latestWorkflowNodeForTest(t, started.Instance.ID, "review")
	if review.Status != "active" && review.Status != "waiting" {
		t.Fatalf("review status = %q, want active or waiting", review.Status)
	}

	rollback := httptest.NewRecorder()
	rollbackRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+work.ID+
				"/rollback?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason":          "New information requires rework",
				"idempotency_key": "owner-rollback-work",
			},
		),
		"nodeInstanceId",
		work.ID,
	)
	rollbackRequest.Header.Set("X-User-ID", ownerID)
	testHandler.RollbackWorkflowNode(rollback, rollbackRequest)
	if rollback.Code != http.StatusForbidden {
		t.Fatalf(
			"node owner rollback status = %d, want forbidden, body = %s",
			rollback.Code,
			rollback.Body.String(),
		)
	}
	rollback = httptest.NewRecorder()
	rollbackRequest = withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+work.ID+
				"/rollback?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason":          "Admin approved rework",
				"idempotency_key": "admin-rollback-work",
			},
		),
		"nodeInstanceId",
		work.ID,
	)
	testHandler.RollbackWorkflowNode(rollback, rollbackRequest)
	if rollback.Code != http.StatusOK {
		t.Fatalf(
			"workspace admin rollback status = %d, body = %s",
			rollback.Code,
			rollback.Body.String(),
		)
	}
	reworked := latestWorkflowNodeForTest(t, started.Instance.ID, "work")
	if reworked.Attempt != 2 || reworked.Status != "active" {
		t.Fatalf("reworked node = %#v, want active attempt 2", reworked)
	}
}

func createWorkflowHostForTest(t *testing.T, title string) string {
	t.Helper()
	var hostID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id,
			number, position
		) VALUES ($1, $2, 'todo', 'none', 'member', $3, $4, 0)
		RETURNING id
	`, testWorkspaceID, title, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create workflow host %q: %v", title, err)
	}
	return hostID
}

func startWorkflowForTest(
	t *testing.T,
	hostID, templateID string,
	roleAssignments []map[string]any,
	idempotencyKey string,
) workflowInstanceDetailResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID,
			map[string]any{
				"template_id":      templateID,
				"role_assignments": roleAssignments,
				"idempotency_key":  idempotencyKey,
			},
		),
		"id",
		hostID,
	)
	testHandler.StartIssueWorkflow(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf(
			"StartIssueWorkflow status = %d, body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	var response workflowInstanceDetailResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode workflow start: %v", err)
	}
	return response
}

func createWorkflowAgentSkillForTest(
	t *testing.T,
	agentID, name string,
	enabled bool,
) {
	t.Helper()
	ctx := context.Background()
	var skillID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO skill (
			workspace_id, name, description, content, config, created_by
		) VALUES ($1, $2, '', '', '{}'::jsonb, $3)
		RETURNING id
	`, testWorkspaceID, name, testUserID).Scan(&skillID); err != nil {
		t.Fatalf("create workflow agent skill: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_skill (agent_id, skill_id, enabled)
		VALUES ($1, $2, $3)
	`, agentID, skillID, enabled); err != nil {
		t.Fatalf("assign workflow agent skill: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_skill WHERE skill_id = $1`, skillID)
		testPool.Exec(context.Background(), `DELETE FROM skill WHERE id = $1`, skillID)
	})
}

func createPublishedWorkflowTemplateForTest(
	t *testing.T,
	name string,
	definition workflowdomain.Definition,
) string {
	t.Helper()
	ctx := context.Background()
	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template (workspace_id, name, status, created_by)
		VALUES ($1, $2, 'published', $3)
		RETURNING id
	`, testWorkspaceID, name, testUserID).Scan(&templateID); err != nil {
		t.Fatalf("create workflow template %q: %v", name, err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template_version (
			workspace_id, template_id, version, status, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, 'published', $3, 'test', $4, $4, now())
		RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create workflow template version %q: %v", name, err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_template SET latest_published_version_id = $1 WHERE id = $2
	`, versionID, templateID); err != nil {
		t.Fatalf("publish workflow template %q: %v", name, err)
	}
	return templateID
}

func workflowTaskIssueForNode(
	t *testing.T,
	tasks []workflowTaskResponse,
	nodeID string,
) string {
	t.Helper()
	for _, task := range tasks {
		if task.WorkflowNodeInstanceID == nodeID && task.IssueID != nil {
			return *task.IssueID
		}
	}
	t.Fatalf("workflow issue for node %s not found in %#v", nodeID, tasks)
	return ""
}

func postWorkflowSubmissionPayload(
	t *testing.T,
	nodeID, idempotencyKey string,
	payload map[string]any,
) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-instances/"+nodeID+"/submissions?workspace_id="+testWorkspaceID, map[string]any{
		"payload": payload, "summary": "DAG result", "idempotency_key": idempotencyKey,
	}), "nodeInstanceId", nodeID)
	testHandler.CreateWorkflowNodeSubmission(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("CreateWorkflowNodeSubmission DAG status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func reconcileWorkflowForTest(t *testing.T, instanceID, idempotencyKey string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPost, "/api/workflow-instances/"+instanceID+"/reconcile?workspace_id="+testWorkspaceID, map[string]any{
		"idempotency_key": idempotencyKey,
	}), "instanceId", instanceID)
	testHandler.ReconcileWorkflowInstance(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ReconcileWorkflowInstance status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func completeWorkflowIssue(t *testing.T, issueID string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID+"?workspace_id="+testWorkspaceID, map[string]any{
		"status": "done",
	}), "id", issueID)
	testHandler.UpdateIssue(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("UpdateIssue workflow completion status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func postSubmission(t *testing.T, nodeID, idempotencyKey, result string) {
	t.Helper()
	body := map[string]any{
		"payload": map[string]any{"result": result},
		"summary": result, "idempotency_key": idempotencyKey,
	}
	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+nodeID+
				"/submissions?workspace_id="+testWorkspaceID,
			body,
		),
		"nodeInstanceId",
		nodeID,
	)
	testHandler.CreateWorkflowNodeSubmission(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("CreateWorkflowNodeSubmission status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var first struct {
		Submission workflowSubmissionResponse `json:"submission"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode workflow submission: %v", err)
	}

	replayRecorder := httptest.NewRecorder()
	replayRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+nodeID+
				"/submissions?workspace_id="+testWorkspaceID,
			body,
		),
		"nodeInstanceId",
		nodeID,
	)
	testHandler.CreateWorkflowNodeSubmission(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusOK {
		t.Fatalf(
			"CreateWorkflowNodeSubmission replay status = %d, body = %s",
			replayRecorder.Code,
			replayRecorder.Body.String(),
		)
	}
	var replay struct {
		Submission workflowSubmissionResponse `json:"submission"`
	}
	if err := json.Unmarshal(replayRecorder.Body.Bytes(), &replay); err != nil {
		t.Fatalf("decode replayed workflow submission: %v", err)
	}
	if replay.Submission.ID != first.Submission.ID {
		t.Fatalf(
			"submission replay changed identity: first=%s replay=%s",
			first.Submission.ID,
			replay.Submission.ID,
		)
	}
}

func decideAcceptance(t *testing.T, instanceID string, body map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPost, "/api/workflow-instances/"+instanceID+"/acceptances?workspace_id="+testWorkspaceID, body), "instanceId", instanceID)
	testHandler.DecideWorkflowAcceptance(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("DecideWorkflowAcceptance status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var first struct {
		Acceptance workflowAcceptanceResponse `json:"acceptance"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode workflow acceptance: %v", err)
	}

	replayRecorder := httptest.NewRecorder()
	replayRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-instances/"+instanceID+
				"/acceptances?workspace_id="+testWorkspaceID,
			body,
		),
		"instanceId",
		instanceID,
	)
	testHandler.DecideWorkflowAcceptance(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusOK {
		t.Fatalf(
			"DecideWorkflowAcceptance replay status = %d, body = %s",
			replayRecorder.Code,
			replayRecorder.Body.String(),
		)
	}
	var replay struct {
		Acceptance workflowAcceptanceResponse `json:"acceptance"`
	}
	if err := json.Unmarshal(replayRecorder.Body.Bytes(), &replay); err != nil {
		t.Fatalf("decode replayed workflow acceptance: %v", err)
	}
	if replay.Acceptance.ID != first.Acceptance.ID {
		t.Fatalf(
			"acceptance replay changed identity: first=%s replay=%s",
			first.Acceptance.ID,
			replay.Acceptance.ID,
		)
	}
}

func confirmWorkflowNode(t *testing.T, nodeID, idempotencyKey string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-instances/"+nodeID+"/confirm?workspace_id="+testWorkspaceID, map[string]any{
		"decision": "approved", "comment": "Reviewed", "idempotency_key": idempotencyKey,
	}), "nodeInstanceId", nodeID)
	testHandler.ConfirmWorkflowNode(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("ConfirmWorkflowNode status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	replayRecorder := httptest.NewRecorder()
	replayRequest := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-instances/"+nodeID+"/confirm?workspace_id="+testWorkspaceID, map[string]any{
		"decision": "approved", "comment": "Reviewed", "idempotency_key": idempotencyKey,
	}), "nodeInstanceId", nodeID)
	testHandler.ConfirmWorkflowNode(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusOK {
		t.Fatalf("ConfirmWorkflowNode replay status = %d, body = %s", replayRecorder.Code, replayRecorder.Body.String())
	}
}

func postWorkflowVerdict(t *testing.T, nodeID, idempotencyKey string) {
	t.Helper()
	postWorkflowVerdictResult(
		t,
		nodeID,
		"pass",
		"Reviewed result",
		idempotencyKey,
	)
}

func postWorkflowVerdictResult(
	t *testing.T,
	nodeID, result, reason, idempotencyKey string,
) {
	t.Helper()
	body := map[string]any{
		"result": result, "reason": reason,
		"evidence": []any{}, "idempotency_key": idempotencyKey,
	}
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-instances/"+nodeID+"/verdicts?workspace_id="+testWorkspaceID, body), "nodeInstanceId", nodeID)
	testHandler.CreateWorkflowNodeVerdict(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("CreateWorkflowNodeVerdict status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	replayRecorder := httptest.NewRecorder()
	replayRequest := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-instances/"+nodeID+"/verdicts?workspace_id="+testWorkspaceID, body), "nodeInstanceId", nodeID)
	testHandler.CreateWorkflowNodeVerdict(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusOK {
		t.Fatalf("CreateWorkflowNodeVerdict replay status = %d, body = %s", replayRecorder.Code, replayRecorder.Body.String())
	}
}

func createDynamicWorkflowIssue(
	t *testing.T,
	nodeID, title, idempotencyKey string,
) workflowTaskResponse {
	t.Helper()
	body := map[string]any{
		"title": title, "initial_status": "todo", "priority": "none",
		"required": false, "idempotency_key": idempotencyKey,
	}
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-instances/"+nodeID+"/issues?workspace_id="+testWorkspaceID, body), "nodeInstanceId", nodeID)
	testHandler.CreateWorkflowNodeIssue(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("CreateWorkflowNodeIssue status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var first struct {
		Task workflowTaskResponse `json:"task"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode dynamic workflow task: %v", err)
	}

	replayRecorder := httptest.NewRecorder()
	replayRequest := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-instances/"+nodeID+"/issues?workspace_id="+testWorkspaceID, body), "nodeInstanceId", nodeID)
	testHandler.CreateWorkflowNodeIssue(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusOK {
		t.Fatalf("CreateWorkflowNodeIssue replay status = %d, body = %s", replayRecorder.Code, replayRecorder.Body.String())
	}
	var replay struct {
		Task workflowTaskResponse `json:"task"`
	}
	if err := json.Unmarshal(replayRecorder.Body.Bytes(), &replay); err != nil {
		t.Fatalf("decode dynamic workflow task replay: %v", err)
	}
	if replay.Task.ID != first.Task.ID {
		t.Fatalf("dynamic task replay changed ID: first=%s replay=%s", first.Task.ID, replay.Task.ID)
	}
	return first.Task
}

func resolveWorkflowExecutor(t *testing.T, nodeID, taskID, idempotencyKey string) {
	t.Helper()
	body := map[string]any{
		"task_id": taskID, "actor_type": "member", "actor_id": testUserID,
		"reason": "Manual reassignment", "idempotency_key": idempotencyKey,
	}
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-instances/"+nodeID+"/resolve-executor?workspace_id="+testWorkspaceID, body), "nodeInstanceId", nodeID)
	testHandler.ResolveWorkflowNodeExecutor(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("ResolveWorkflowNodeExecutor status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func changeWorkflowTask(
	t *testing.T,
	taskID, action, idempotencyKey string,
) workflowTaskResponse {
	t.Helper()
	body := map[string]any{
		"reason": "Test action", "idempotency_key": idempotencyKey,
	}
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-tasks/"+taskID+"/"+action+"?workspace_id="+testWorkspaceID, body), "taskId", taskID)
	if action == "retry" {
		testHandler.RetryWorkflowNodeTask(recorder, request)
	} else {
		testHandler.DetachWorkflowNodeTask(recorder, request)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s workflow task status = %d, body = %s", action, recorder.Code, recorder.Body.String())
	}
	var response struct {
		Task workflowTaskResponse `json:"task"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode %s workflow task: %v", action, err)
	}
	return response.Task
}

func transitionWorkflowNode(
	t *testing.T,
	nodeID, action, idempotencyKey string,
) {
	t.Helper()
	body := map[string]any{
		"reason": "Test node transition", "idempotency_key": idempotencyKey,
	}
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-instances/"+nodeID+"/"+action+"?workspace_id="+testWorkspaceID, body), "nodeInstanceId", nodeID)
	switch action {
	case "complete":
		testHandler.CompleteWorkflowNode(recorder, request)
	case "skip":
		testHandler.SkipWorkflowNode(recorder, request)
	case "rollback":
		testHandler.RollbackWorkflowNode(recorder, request)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s workflow node status = %d, body = %s", action, recorder.Code, recorder.Body.String())
	}

	replayRecorder := httptest.NewRecorder()
	replayRequest := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-instances/"+nodeID+"/"+action+"?workspace_id="+testWorkspaceID, body), "nodeInstanceId", nodeID)
	switch action {
	case "complete":
		testHandler.CompleteWorkflowNode(replayRecorder, replayRequest)
	case "skip":
		testHandler.SkipWorkflowNode(replayRecorder, replayRequest)
	case "rollback":
		testHandler.RollbackWorkflowNode(replayRecorder, replayRequest)
	}
	if replayRecorder.Code != http.StatusOK {
		t.Fatalf("%s workflow node replay status = %d, body = %s", action, replayRecorder.Code, replayRecorder.Body.String())
	}
}

func jsonContainsWaitingReason(raw []byte, code string) bool {
	var reasons []workflowdomain.WaitingReason
	if json.Unmarshal(raw, &reasons) != nil {
		return false
	}
	for _, reason := range reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}

func findWorkflowNodeResponse(t *testing.T, nodes []workflowNodeResponse, key string, attempt int32) workflowNodeResponse {
	t.Helper()
	for _, node := range nodes {
		if node.NodeKey == key && node.Attempt == attempt {
			return node
		}
	}
	t.Fatalf("node %s attempt %d not found", key, attempt)
	return workflowNodeResponse{}
}

func latestWorkflowNodeForTest(t *testing.T, instanceID, key string) db.WorkflowNodeInstance {
	t.Helper()
	node, err := testHandler.Queries.GetLatestWorkflowNodeAttempt(context.Background(), db.GetLatestWorkflowNodeAttemptParams{
		WorkflowInstanceID: parseUUID(instanceID), WorkspaceID: parseUUID(testWorkspaceID), NodeKey: key,
	})
	if err != nil {
		t.Fatalf("load latest node %s: %v", key, err)
	}
	return node
}

func cleanupWorkflowRuntimeTest(t *testing.T) {
	t.Helper()
	cleanup := func() {
		ctx := context.Background()
		for _, table := range []string{
			"workflow_event", "workflow_acceptance", "workflow_node_confirmation",
			"workflow_node_verdict", "workflow_node_submission", "workflow_executor_resolution",
			"workflow_node_task", "workflow_node_participant", "workflow_node_instance",
			"workflow_instance_role_assignment", "workflow_instance",
			"workflow_template_version", "workflow_template",
		} {
			if _, err := testPool.Exec(ctx, "DELETE FROM "+table+" WHERE workspace_id = $1", testWorkspaceID); err != nil {
				t.Fatalf("cleanup %s: %v", table, err)
			}
		}
		if _, err := testPool.Exec(ctx, `DELETE FROM issue WHERE workspace_id = $1 AND (title IN ('Workflow runtime host', 'Atomic workflow host', 'Workflow guard host', 'Workflow required child', 'Workflow optional child', 'Workflow DAG host', 'Workflow any join host', 'Workflow needs setup host', 'Workflow owner rollback host', 'Workflow fairness host', 'Dynamic investigation', 'Retry investigation') OR title LIKE 'Workflow executor %' OR origin_type = 'workflow')`, testWorkspaceID); err != nil {
			t.Fatalf("cleanup workflow issues: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
}
