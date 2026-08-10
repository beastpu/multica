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
	"github.com/multica-ai/multica/server/internal/service"
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true, AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Implementation", OwnerRole: "owner",
				IssuePolicy: "fixed_and_dynamic", TimeoutMinutes: 1,
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "implementation", Title: "Implement {{host.title}}", Required: true,
				}},
				SubmissionSchema: &workflowdomain.SubmissionSchema{},
				Reviewer: &workflowdomain.ReviewerDefinition{
					Kind: "owner", Required: true,
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
		Acceptance: workflowdomain.AcceptanceDefinition{
			Policy: "member", ApproverRole: "owner",
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow (
			workspace_id, name, created_by
		) VALUES ($1, 'Runtime test template', $2)
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
		t.Fatalf("set published version: %v", err)
	}

	startRecorder := httptest.NewRecorder()
	startRequest := withURLParam(newRequest(http.MethodPost, "/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID, map[string]any{
		"workflow_id": templateID,
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
	// The sweeper and the reconciler both persist waiting reasons. A reconcile
	// triggered by any ordinary event (a comment, a submission, an issue status
	// change) must not erase the timeout the sweeper just recorded — the
	// instance derives `blocked_or_timeout` from it, so losing it makes a
	// stalled activity look identical to a healthy one.
	reconcileWorkflowForTest(t, instanceID, "runtime-test-timeout-survives-reconcile")
	reconciledNode := latestWorkflowNodeForTest(t, instanceID, "work")
	if !jsonContainsWaitingReason(reconciledNode.WaitingReasons, "node_timeout") {
		t.Fatalf(
			"node_timeout erased by reconcile: waiting reasons = %s",
			reconciledNode.WaitingReasons,
		)
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
				"issue_ids": []string{firstIssueID},
				"updates":   map[string]any{"status": "done"},
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
	// The executor has delivered and the node is now stopped on its reviewer,
	// which is what in_review means.
	nodeBeforeVerdict := latestWorkflowNodeForTest(t, instanceID, "work")
	if nodeBeforeVerdict.Status != "in_review" ||
		!jsonContainsWaitingReason(nodeBeforeVerdict.WaitingReasons, "review_required") {
		t.Fatalf("work node before verdict = %#v", nodeBeforeVerdict)
	}
	postWorkflowVerdict(t, firstWorkNode.ID, "runtime-test-verdict-1")

	assertPendingWorkflowAcceptance(t, instanceID)
	var firstAttemptIssueID string
	if err := testPool.QueryRow(ctx, `
		SELECT issue_id::text FROM workflow_node_task
		WHERE workflow_node_instance_id = $1 AND issue_id IS NOT NULL
		ORDER BY created_at DESC LIMIT 1
	`, firstWorkNode.ID).Scan(&firstAttemptIssueID); err != nil {
		t.Fatalf("read first attempt issue: %v", err)
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
	// Rework continues on the issue the previous attempt already used. A fresh
	// issue would strip the executor of its own prior work and leave the old
	// one orphaned in a non-terminal status, assigned to someone who is no
	// longer on the hook for it.
	if got := uuidToString(secondTasks[0].IssueID); got != firstAttemptIssueID {
		t.Fatalf(
			"rework materialized a new issue %s, want the prior attempt's %s",
			got, firstAttemptIssueID,
		)
	}
	// The reused carrier is reopened before binding and then follows its node
	// like any other: attempt 2 is active and an executor is on it, so the
	// board says in_progress. This still proves the reopen happened — had it
	// not, the issue would read done, and activation does not move a done
	// carrier at all.
	var reworkIssueStatus string
	if err := testPool.QueryRow(ctx,
		`SELECT status FROM issue WHERE id = $1`, firstAttemptIssueID,
	).Scan(&reworkIssueStatus); err != nil {
		t.Fatalf("read reused issue status: %v", err)
	}
	if reworkIssueStatus != "in_progress" {
		t.Fatalf("reused rework issue status = %q, want in_progress", reworkIssueStatus)
	}
	// The reused issue carries the prior attempt, but nothing on it says the
	// work was rejected or why. That has to reach the executor through the task
	// context, or it repeats the attempt that was just sent back.
	reworkIssue, err := testHandler.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID: parseUUID(firstAttemptIssueID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load reused rework issue: %v", err)
	}
	reworkContext := testHandler.workflowTaskContext(ctx, reworkIssue)
	if reworkContext == nil || reworkContext.Rework == nil {
		t.Fatalf("rework task context = %#v, want a rework block", reworkContext)
	}
	if reworkContext.Rework.Attempt != 2 ||
		reworkContext.Rework.Source != "manual_rollback" ||
		reworkContext.Rework.Reason != "Test node transition" {
		t.Fatalf("rework block = %#v", reworkContext.Rework)
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

	assertPendingWorkflowAcceptance(t, instanceID)
	decideAcceptance(t, instanceID, map[string]any{
		"status": "changes_requested", "reason": "Needs another pass",
		"rework_target_node_key": "work", "idempotency_key": "runtime-test-rework",
	})

	thirdWorkNode := latestWorkflowNodeForTest(t, instanceID, "work")
	if thirdWorkNode.Attempt != 3 || thirdWorkNode.Status != "active" {
		t.Fatalf("third work node = %#v", thirdWorkNode)
	}
	transitionWorkflowNode(t, uuidToString(thirdWorkNode.ID), "skip", "runtime-test-skip")

	assertPendingWorkflowAcceptance(t, instanceID)
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity",
				Name: "Work", OwnerRole: "owner",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "work_item", Title: "Do {{host.title}}",
					Required: true,
				}},
			},
			{
				Key: "acceptance", Kind: "activity",
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
		},
	}
	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow (workspace_id, name, created_by)
		VALUES ($1, 'Atomic test template', $2)
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
		t.Fatalf("create version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow SET latest_published_version_id = $1 WHERE id = $2
	`, versionID, templateID); err != nil {
		t.Fatalf("publish template: %v", err)
	}

	body := map[string]any{
		"title": "Atomic workflow host", "workflow_id": templateID,
		"idempotency_key": "atomic-create-test",
	}
	firstRecorder := httptest.NewRecorder()
	testHandler.CreateWorkflowRun(
		firstRecorder,
		newRequest(http.MethodPost, "/api/workflow-instances?workspace_id="+testWorkspaceID, body),
	)
	if firstRecorder.Code != http.StatusCreated {
		t.Fatalf("CreateWorkflowRun status = %d, body = %s", firstRecorder.Code, firstRecorder.Body.String())
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
	testHandler.CreateWorkflowRun(
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity",
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
	templateID := createPublishedWorkflowForTest(
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
						"workflow_id": templateID,
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

func TestWorkflowPauseResumeAndCancelCarrierLifecycle(t *testing.T) {
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
			workspace_id, workflow_id, workflow_version_id, host_issue_id,
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
	// Pausing suspends the run without ending anyone's work, so the carrier is
	// left exactly as it was. Only cancellation is terminal.
	var pausedIssueStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM issue WHERE id = $1
	`, issueID).Scan(&pausedIssueStatus); err != nil || pausedIssueStatus != "todo" {
		t.Fatalf(
			"pause/resume changed the carrier: status=%q err=%v",
			pausedIssueStatus, err,
		)
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
	// Cancelling closes the carriers with the nodes they belong to. This issue
	// exists only because the workflow made it (origin_type = 'workflow'); with
	// the run cancelled it is work nobody will do, and leaving it open puts it
	// on a board looking like something somebody should pick up. Issues a
	// person owns are never bound to a node task, so nothing here can reach
	// them — the host issue is governed separately by host_status_mode.
	var issueStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM issue WHERE id = $1
	`, issueID).Scan(&issueStatus); err != nil || issueStatus != "cancelled" {
		t.Fatalf("cancel left the carrier open: status=%q err=%v", issueStatus, err)
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
			workspace_id, workflow_id, workflow_version_id, host_issue_id,
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

	var hostCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE id = $1`, hostID).Scan(&hostCount); err != nil {
		t.Fatalf("count deleted workflow host: %v", err)
	}
	if hostCount != 0 {
		t.Fatalf("workflow host was not deleted: count=%d", hostCount)
	}
	var instanceStatus string
	var detachedHostID *string
	if err := testPool.QueryRow(ctx, `
		SELECT status, host_issue_id::text FROM workflow_instance WHERE id = $1
	`, instanceID).Scan(&instanceStatus, &detachedHostID); err != nil {
		t.Fatalf("load preserved workflow runtime: %v", err)
	}
	if instanceStatus != "cancelled" || detachedHostID != nil {
		t.Fatalf("preserved workflow runtime status=%q host=%v", instanceStatus, detachedHostID)
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
	ownerExecutor := workflowdomain.ExecutorDefinition{
		Kind: "role", Role: "owner",
		Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
	}
	requiredIssue := func(key, title string) []workflowdomain.IssueTemplate {
		return []workflowdomain.IssueTemplate{{
			Key: key, Title: title, Required: true,
		}}
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "DAG delivery",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true, AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "analysis", Kind: "activity", Name: "Analysis", OwnerRole: "owner",
				Executor: &ownerExecutor, IssuePolicy: "fixed",
				IssueTemplates:   requiredIssue("analysis_issue", "Analyze {{host.title}}"),
				SubmissionSchema: &workflowdomain.SubmissionSchema{},
				Outputs: []workflowdomain.OutputField{{
					Key: "path", Type: "enum", Values: []string{"review", "direct"}, Required: true,
				}},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done", SubmissionRequired: true,
				},
			},
			{
				Key: "implementation", Kind: "activity", Name: "Implementation", OwnerRole: "owner",
				Executor: &ownerExecutor, IssuePolicy: "fixed",
				IssueTemplates: requiredIssue("implementation_issue", "Implement {{host.title}}"),
				Completion:     workflowdomain.CompletionDefinition{RequiredIssueOutcome: "done"},
			},
			{Key: "join", Kind: "parallel_join", JoinMode: "all", Name: "Join"},
			{
				Key: "route", Kind: "gateway", Name: "Review route",
				Cases: []workflowdomain.GatewayCase{
					{ID: "c1", Label: "评审", When: `path == "review"`},
					{ID: "else", Label: "直接结束"},
				},
			},
			{
				Key: "review", Kind: "activity", Name: "Review", OwnerRole: "owner",
				Executor: &ownerExecutor, IssuePolicy: "fixed",
				IssueTemplates: requiredIssue("review_issue", "Review {{host.title}}"),
				Completion:     workflowdomain.CompletionDefinition{RequiredIssueOutcome: "done"},
			},
			{
				Key: "direct", Kind: "activity", Name: "Direct", OwnerRole: "owner",
				Executor: &ownerExecutor, IssuePolicy: "fixed",
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
			{From: "route", To: "review", FromCase: "c1"},
			{From: "route", To: "direct", FromCase: "else"},
			{From: "review", To: "review_end"},
			{From: "direct", To: "direct_end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("DAG definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(t, "DAG test template", definition)

	startRecorder := httptest.NewRecorder()
	startRequest := withURLParam(newRequest(http.MethodPost, "/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID, map[string]any{
		"workflow_id":      templateID,
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

	postWorkflowSubmissionPayload(
		t, analysisNode.ID, "dag-analysis-submission", map[string]any{"path": "review"},
	)
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
			Executor: &workflowdomain.ExecutorDefinition{
				Kind: "role", Role: "owner",
				Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
			},
			IssueTemplates: []workflowdomain.IssueTemplate{{
				Key: key + "_issue", Title: title + " {{host.title}}",
				Required: true,
			}},
			Completion: workflowdomain.CompletionDefinition{RequiredIssueOutcome: "done"},
		}
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Any join delivery",
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
	templateID := createPublishedWorkflowForTest(t, "Any join test template", definition)
	startRecorder := httptest.NewRecorder()
	startRequest := withURLParam(newRequest(http.MethodPost, "/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID, map[string]any{
		"workflow_id": templateID,
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Manual work",
				OwnerRole: "owner", IssuePolicy: "fixed",
				Executor: &workflowdomain.ExecutorDefinition{Kind: "manual"},
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
	templateID := createPublishedWorkflowForTest(t, "Manual executor template", definition)
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

func TestWorkflowCapabilityMatchUsesStructuredEnabledSkill(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "workflow-capability-agent", nil)
	createWorkflowAgentSkillForTest(t, agentID, "release-engineering", true)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Capability executor",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "delivery_pool", Name: "Delivery pool", Required: true,
			AllowedActorTypes: []string{"agent", "squad"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Capability work",
				IssuePolicy: "fixed",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "capability", Role: "delivery_pool",
					Capability: "release-engineering",
					Fallback:   &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
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
	templateID := createPublishedWorkflowForTest(
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

func TestWorkflowDirectExecutorOwnsEveryTask(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	defaultAgentID := createHandlerTestAgent(t, "workflow-default-agent", nil)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Direct executor",
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Backend development",
				IssuePolicy: "fixed",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "actor", ActorType: "agent", ActorID: defaultAgentID,
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{
					{
						Key: "implementation", Title: "Implement {{host.title}}",
						Required: true,
					},
					{
						Key: "verification", Title: "Verify {{host.title}}",
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
	templateID := createPublishedWorkflowForTest(
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

	// Both tasks answer to the node's executor. The issue template used to
	// carry its own assignee that outranked it, which is the "three places to
	// write it, the third one wins" the executor field replaced.
	expected := map[string]string{
		"implementation": defaultAgentID,
		"verification":   defaultAgentID,
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"squad"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "owner", IssuePolicy: "fixed",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "squad_work", Title: "Squad work for {{host.title}}",
					Required: true,
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
	templateID := createPublishedWorkflowForTest(
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

func TestWorkflowAgentSubmissionCannotSelfApprove(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "workflow-suggestion-agent", nil)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Controlled agent suggestion",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "worker", Name: "Worker", Required: true,
			AllowedActorTypes: []string{"agent"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Agent work",
				OwnerRole: "worker", IssuePolicy: "fixed",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "worker",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "agent_task", Title: "Agent {{host.title}}",
					Required: true,
				}},
				SubmissionSchema: &workflowdomain.SubmissionSchema{},
				Reviewer: &workflowdomain.ReviewerDefinition{
					Kind: "role", Role: "worker", Required: true,
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
		t.Fatalf("controlled suggestion definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(
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
	if verdictRecorder.Code != http.StatusConflict {
		t.Fatalf(
			"agent self-verdict status = %d, body = %s",
			verdictRecorder.Code,
			verdictRecorder.Body.String(),
		)
	}
	currentNode := latestWorkflowNodeForTest(t, started.Instance.ID, "work")
	if currentNode.LatestVerdictID.Valid {
		t.Fatalf(
			"worker self-verdict became current workflow truth: %s",
			uuidToString(currentNode.LatestVerdictID),
		)
	}

	// An agent that guesses the body must still be told it is at the wrong
	// door. Validating the body first turned a closed door into a lock to be
	// picked: WTE-14841's Critic tried fourteen shapes against this endpoint,
	// met "invalid request body" every time, and spent its whole run there.
	guessRecorder := httptest.NewRecorder()
	guessRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+work.ID+"/verdicts?workspace_id="+testWorkspaceID,
			map[string]any{"decision": "approved", "summary": "QA pass"},
		),
		"nodeInstanceId",
		work.ID,
	)
	guessRequest.Header.Set("X-Agent-ID", agentID)
	guessRequest.Header.Set("X-Task-ID", agentTaskID)
	testHandler.CreateWorkflowNodeVerdict(guessRecorder, guessRequest)
	if guessRecorder.Code != http.StatusConflict {
		t.Fatalf(
			"agent guessing the verdict body: status = %d, want %d, body = %s",
			guessRecorder.Code, http.StatusConflict, guessRecorder.Body.String(),
		)
	}
	if !strings.Contains(guessRecorder.Body.String(), "multica workflow review") {
		t.Fatalf(
			"refusal does not say where the verdict belongs: %s",
			guessRecorder.Body.String(),
		)
	}
}

func TestWorkflowAgentCriticCompletion(t *testing.T) {
	for _, test := range []struct {
		name        string
		decision    string
		reason      string
		wantStatus  string
		wantAttempt int32
	}{
		{name: "approval advances", decision: "pass", reason: "meets the acceptance criteria", wantStatus: "completed", wantAttempt: 1},
		{name: "rejection starts rework", decision: "fail", reason: "add the missing regression test", wantStatus: "running", wantAttempt: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
			cleanupWorkflowRuntimeTest(t)
			ctx := context.Background()
			workerID := createHandlerTestAgent(t, "workflow-critic-worker-"+test.name, nil)
			criticID := createHandlerTestAgent(t, "workflow-critic-reviewer-"+test.name, nil)

			definition := workflowdomain.Definition{
				SchemaVersion: workflowdomain.DefinitionSchemaVersion,
				Name:          "Agent Critic completion",
				Nodes: []workflowdomain.NodeDefinition{
					{Key: "start", Kind: "start", Name: "Start"},
					{
						Key: "work", Kind: "activity", Name: "Agent work",
						IssuePolicy: "none",
						Executor: &workflowdomain.ExecutorDefinition{
							Kind: "actor", ActorType: "agent", ActorID: workerID,
							Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
						},
						Reviewer: &workflowdomain.ReviewerDefinition{
							Kind: "actor", ActorType: "agent", ActorID: criticID,
							Required: true,
						},
						Artifacts: []workflowdomain.ArtifactRequirement{{
							Key: "implementation", Name: "Implementation", Required: true,
						}},
					},
					{Key: "end", Kind: "end", Name: "End"},
				},
				Edges: []workflowdomain.EdgeDefinition{
					{From: "start", To: "work"}, {From: "work", To: "end"},
				},
			}
			if err := workflowdomain.ValidateDefinition(definition); err != nil {
				t.Fatalf("critic workflow definition invalid: %v", err)
			}
			templateID := createPublishedWorkflowForTest(
				t, "Agent Critic template "+test.name, definition,
			)
			hostID := createWorkflowHostForTest(t, "Agent Critic host "+test.name)
			started := startWorkflowForTest(
				t, hostID, templateID, nil, "agent-critic-start-"+test.name,
			)
			work := findWorkflowNodeResponse(t, started.Nodes, "work", 1)

			var executionTaskID string
			if err := testPool.QueryRow(ctx, `
				SELECT id FROM workflow_node_task
				WHERE workflow_node_instance_id = $1 AND source = 'execution'
			`, work.ID).Scan(&executionTaskID); err != nil {
				t.Fatalf("load direct Worker task: %v", err)
			}
			if _, err := testPool.Exec(ctx, `
					UPDATE agent_task_queue
					SET status = 'completed', result = $2, completed_at = now()
					WHERE workflow_node_task_id = $1
				`, executionTaskID, []byte(`{"output":"implemented the requested behavior"}`)); err != nil {
				t.Fatalf("complete direct Worker task: %v", err)
			}
			artifact, err := testHandler.Queries.CreateWorkflowArtifact(
				ctx,
				db.CreateWorkflowArtifactParams{
					WorkspaceID:            parseUUID(testWorkspaceID),
					WorkflowInstanceID:     parseUUID(started.Instance.ID),
					WorkflowNodeInstanceID: parseUUID(work.ID),
					ArtifactKey:            "implementation", Attempt: 1,
					Kind: "document", Name: "Implementation",
					Content:         "implemented the requested behavior",
					SubmittedByType: "agent", SubmittedByID: parseUUID(workerID),
				},
			)
			if err != nil {
				t.Fatalf("submit Worker artifact: %v", err)
			}
			if _, err := testHandler.reconcileWorkflowInstance(
				ctx, parseUUID(testWorkspaceID), parseUUID(started.Instance.ID),
				"system", pgtype.UUID{}, "agent-critic-delivered-"+test.name,
			); err != nil && !errors.Is(err, errWorkflowNoop) {
				t.Fatalf("reconcile Worker delivery: %v", err)
			}

			// The review is found through the node, not through a task row
			// standing in for it.
			criticTask, err := testHandler.Queries.GetLatestAgentTaskForWorkflowNodeReview(
				ctx, parseUUID(work.ID),
			)
			if err != nil {
				t.Fatalf("load Critic agent task: %v", err)
			}
			// And it leaves nothing behind in the task table, where every row
			// is supposed to be a unit of work somebody can be assigned to.
			var carriers int
			if err := testPool.QueryRow(ctx, `
				SELECT count(*) FROM workflow_node_task
				WHERE workflow_node_instance_id = $1 AND source = 'critic'
			`, work.ID).Scan(&carriers); err != nil {
				t.Fatalf("count critic carriers: %v", err)
			}
			if carriers != 0 {
				t.Fatalf("the review still created %d task row(s)", carriers)
			}
			direct, ok := service.ParseWorkflowNodeTaskContext(criticTask)
			if !ok || direct.Phase != service.WorkflowNodeTaskPhaseCritic ||
				uuidToString(criticTask.AgentID) != criticID {
				t.Fatalf("unexpected Critic task: task=%#v context=%#v", criticTask, direct)
			}
			if err := testHandler.recordWorkflowAgentCriticVerdict(
				ctx, criticTask, "", test.decision, test.reason,
			); err != nil {
				t.Fatalf("record Critic verdict: %v", err)
			}

			instance, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
				ctx,
				db.GetWorkflowInstanceInWorkspaceParams{
					ID: parseUUID(started.Instance.ID), WorkspaceID: parseUUID(testWorkspaceID),
				},
			)
			if err != nil || instance.Status != test.wantStatus {
				t.Fatalf("instance status = %q, want %q, err=%v", instance.Status, test.wantStatus, err)
			}
			latest := latestWorkflowNodeForTest(t, started.Instance.ID, "work")
			if latest.Attempt != test.wantAttempt {
				t.Fatalf("latest work attempt = %d, want %d", latest.Attempt, test.wantAttempt)
			}
			reviewedArtifact, err := testHandler.Queries.GetWorkflowArtifact(
				ctx,
				db.GetWorkflowArtifactParams{
					ID: artifact.ID, WorkspaceID: parseUUID(testWorkspaceID),
				},
			)
			if err != nil {
				t.Fatalf("load reviewed artifact: %v", err)
			}
			wantArtifactStatus := "approved"
			if test.wantAttempt == 2 {
				wantArtifactStatus = "rejected"
			}
			if reviewedArtifact.ReviewStatus != wantArtifactStatus {
				t.Fatalf(
					"artifact review status = %q, want %q",
					reviewedArtifact.ReviewStatus,
					wantArtifactStatus,
				)
			}
			verdicts, err := testHandler.Queries.ListWorkflowVerdicts(
				ctx,
				db.ListWorkflowVerdictsParams{
					WorkflowNodeInstanceID: parseUUID(work.ID),
					WorkspaceID:            parseUUID(testWorkspaceID),
				},
			)
			if err != nil || len(verdicts) == 0 {
				t.Fatalf("load Critic verdicts: count=%d err=%v", len(verdicts), err)
			}
			var verdictBasis struct {
				ArtifactIDs []string `json:"artifact_ids"`
			}
			if err := json.Unmarshal(verdicts[0].Basis, &verdictBasis); err != nil {
				t.Fatalf("decode Critic verdict basis: %v", err)
			}
			if len(verdictBasis.ArtifactIDs) != 1 ||
				verdictBasis.ArtifactIDs[0] != uuidToString(artifact.ID) {
				t.Fatalf("Critic verdict artifact snapshot = %#v", verdictBasis.ArtifactIDs)
			}
			if test.wantAttempt == 2 {
				var reworkTaskID string
				if err := testPool.QueryRow(ctx, `
					SELECT queue.id
					FROM agent_task_queue queue
					JOIN workflow_node_task task ON task.id = queue.workflow_node_task_id
					WHERE task.workflow_node_instance_id = $1 AND task.source = 'execution'
					ORDER BY queue.created_at DESC LIMIT 1
				`, uuidToString(latest.ID)).Scan(&reworkTaskID); err != nil {
					t.Fatalf("load rework Worker task: %v", err)
				}
				reworkTask, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(reworkTaskID))
				if err != nil {
					t.Fatalf("load rework Worker context: %v", err)
				}
				workflowContext := testHandler.workflowTaskContextForDirectTask(ctx, reworkTask)
				if workflowContext == nil || workflowContext.Rework == nil ||
					workflowContext.Rework.Source != "critic" ||
					workflowContext.Rework.Reason != "add the missing regression test" {
					t.Fatalf("unexpected rework context: %#v", workflowContext)
				}
			}
		})
	}
}

func TestWorkflowManualActivityWaitsForExplicitMemberCompletion(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Manual activity",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "manual", Kind: "activity", Name: "Manual review",
				OwnerRole: "owner", IssuePolicy: "fixed",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "review", Title: "Review {{host.title}}", Required: true,
					InitialStatus: "todo",
				}},
				Completion: workflowdomain.CompletionDefinition{
					Mode: "manual", RequiredIssueOutcome: "done",
				},
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
	templateID := createPublishedWorkflowForTest(
		t,
		"Manual activity template",
		definition,
	)
	hostID := createWorkflowHostForTest(t, "Manual activity host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "manual-activity-start")
	manual := findWorkflowNodeResponse(t, started.Nodes, "manual", 1)
	manualIssueID := workflowTaskIssueForNode(t, started.Tasks, manual.ID)

	reconcileWorkflowForTest(t, started.Instance.ID, "manual-activity-before-complete")
	waiting := latestWorkflowNodeForTest(t, started.Instance.ID, "manual")
	if waiting.Status != "waiting" {
		t.Fatalf("manual node status = %s, want waiting", waiting.Status)
	}
	if !strings.Contains(string(waiting.WaitingReasons), "required_issue_not_done") {
		t.Fatalf("manual prerequisite reasons = %s", waiting.WaitingReasons)
	}

	earlyRecorder := httptest.NewRecorder()
	earlyRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+manual.ID+"/complete?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason":          "",
				"idempotency_key": "manual-activity-complete-early",
			},
		),
		"nodeInstanceId",
		manual.ID,
	)
	testHandler.CompleteWorkflowNode(earlyRecorder, earlyRequest)
	if earlyRecorder.Code != http.StatusConflict {
		t.Fatalf(
			"early manual complete status = %d, body = %s",
			earlyRecorder.Code,
			earlyRecorder.Body.String(),
		)
	}

	completeWorkflowIssue(t, manualIssueID)
	reconcileWorkflowForTest(t, started.Instance.ID, "manual-activity-ready")
	waiting = latestWorkflowNodeForTest(t, started.Instance.ID, "manual")
	if !strings.Contains(string(waiting.WaitingReasons), "manual_completion_required") {
		t.Fatalf("manual waiting reasons = %s", waiting.WaitingReasons)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+manual.ID+"/complete?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason":          "",
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
	replayRecorder := httptest.NewRecorder()
	replayRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+manual.ID+"/complete?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason":          "",
				"idempotency_key": "manual-activity-complete",
			},
		),
		"nodeInstanceId",
		manual.ID,
	)
	testHandler.CompleteWorkflowNode(replayRecorder, replayRequest)
	if replayRecorder.Code != http.StatusOK {
		t.Fatalf(
			"manual complete replay status = %d, body = %s",
			replayRecorder.Code,
			replayRecorder.Body.String(),
		)
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "decision", Kind: "activity", Name: "Decision",
				OwnerRole: "owner", IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{Policy: "single"},
				Outputs: []workflowdomain.OutputField{{
					Key: "proceed", Type: "enum", Values: []string{"end", "retry"},
				}},
				Reviewer: &workflowdomain.ReviewerDefinition{
					Kind: "auto", Required: true,
					Condition: json.RawMessage(
						`{"source":"node_submission","node":"decision","key":"proceed","op":"eq","value":"end"}`,
					),
				},
				Completion: workflowdomain.CompletionDefinition{
					SubmissionRequired: true,
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
	templateID := createPublishedWorkflowForTest(
		t, "Deterministic verdict template", definition,
	)
	hostID := createWorkflowHostForTest(t, "Deterministic verdict host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "deterministic-verdict-start")
	decision := findWorkflowNodeResponse(t, started.Nodes, "decision", 1)

	postWorkflowSubmissionPayload(
		t, decision.ID, "deterministic-verdict-fail", map[string]any{},
	)
	reconcileWorkflowForTest(t, started.Instance.ID, "deterministic-verdict-fail-reconcile")
	waiting := latestWorkflowNodeForTest(t, started.Instance.ID, "decision")
	if waiting.Status != "waiting" ||
		!strings.Contains(string(waiting.WaitingReasons), "verdict_not_passed") {
		t.Fatalf("deterministic fail node = %#v", waiting)
	}

	postWorkflowSubmissionPayload(
		t, decision.ID, "deterministic-verdict-pass", map[string]any{"proceed": "end"},
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

// A node that declares outputs owns a delivery contract: values are validated
// against it, failures come back structured enough for an agent to self-fix,
// and a summary-only submission missing required fields is refused rather than
// silently routing the downstream gateway to else.
func TestWorkflowSubmissionOutputValidation(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Output validation",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "triage", Kind: "activity", Name: "Triage",
				OwnerRole: "owner", IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{Policy: "single"},
				Outputs: []workflowdomain.OutputField{
					{Key: "is_bug", Type: "bool", Required: true},
					{Key: "severity", Type: "enum", Values: []string{"low", "high"}},
				},
				Completion: workflowdomain.CompletionDefinition{SubmissionRequired: true},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "triage"}, {From: "triage", To: "end"},
		},
	}
	templateID := createPublishedWorkflowForTest(t, "Output validation template", definition)
	hostID := createWorkflowHostForTest(t, "Output validation host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "output-validation-start")
	triage := findWorkflowNodeResponse(t, started.Nodes, "triage", 1)

	submit := func(payload map[string]any, key string) *httptest.ResponseRecorder {
		body := map[string]any{
			"payload": payload, "summary": "conclusion", "idempotency_key": key,
		}
		recorder := httptest.NewRecorder()
		request := withURLParam(newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+triage.ID+"/submissions?workspace_id="+testWorkspaceID,
			body,
		), "nodeInstanceId", triage.ID)
		testHandler.CreateWorkflowNodeSubmission(recorder, request)
		return recorder
	}

	t.Run("summary-only misses required fields", func(t *testing.T) {
		recorder := submit(map[string]any{}, "output-validation-empty")
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("empty payload status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var response struct {
			Error  string                            `json:"error"`
			Fields []workflowdomain.OutputFieldError `json:"fields"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode validation error: %v", err)
		}
		if response.Error != "output_validation_failed" ||
			len(response.Fields) != 1 ||
			response.Fields[0].Key != "is_bug" ||
			response.Fields[0].Problem != "missing_required" {
			t.Fatalf("validation response = %+v", response)
		}
	})

	t.Run("invalid enum names the accepted values", func(t *testing.T) {
		recorder := submit(
			map[string]any{"is_bug": true, "severity": "urgent"},
			"output-validation-enum",
		)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid enum status = %d", recorder.Code)
		}
		var response struct {
			Fields []workflowdomain.OutputFieldError `json:"fields"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode validation error: %v", err)
		}
		if len(response.Fields) != 1 || response.Fields[0].Problem != "invalid_enum" ||
			len(response.Fields[0].Expected) != 2 {
			t.Fatalf("enum error = %+v", response.Fields)
		}
	})

	t.Run("string values coerce to declared types", func(t *testing.T) {
		recorder := submit(
			map[string]any{"is_bug": "false", "severity": "low"},
			"output-validation-coerce",
		)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("coerced submit status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var response struct {
			Submission struct {
				Payload map[string]any `json:"payload"`
			} `json:"submission"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode submission: %v", err)
		}
		if response.Submission.Payload["is_bug"] != false {
			t.Fatalf("payload not normalized: %+v", response.Submission.Payload)
		}
	})
	_ = ctx
}

// A filter gateway is how a run adds a review without leaving the main line.
// Both branches have to activate off one delivery, and the node they rejoin
// has to wait for both rather than racing ahead on the first.
func TestWorkflowFilterGatewayActivatesEveryMatch(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	ownerExecutor := workflowdomain.ExecutorDefinition{
		Kind: "role", Role: "owner",
		Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
	}
	activity := func(key, name string) workflowdomain.NodeDefinition {
		return workflowdomain.NodeDefinition{
			Key: key, Kind: "activity", Name: name, OwnerRole: "owner",
			Executor: &ownerExecutor, IssuePolicy: "fixed",
			IssueTemplates: []workflowdomain.IssueTemplate{{
				Key: key + "_issue", Title: name + " {{host.title}}", Required: true,
			}},
			Completion: workflowdomain.CompletionDefinition{RequiredIssueOutcome: "done"},
		}
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Filter gateway",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "fix", Kind: "activity", Name: "Fix", OwnerRole: "owner",
				Executor: &ownerExecutor, IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{Policy: "single"},
				Outputs: []workflowdomain.OutputField{
					{Key: "touched_db", Type: "bool", Required: true},
					{Key: "touched_api", Type: "bool", Required: true},
				},
				Completion: workflowdomain.CompletionDefinition{SubmissionRequired: true},
			},
			{
				Key: "gates", Kind: "gateway", Name: "Extra reviews",
				Mode: workflowdomain.GatewayModeFilter,
				Cases: []workflowdomain.GatewayCase{
					{ID: "dba", Label: "需 DBA", When: `touched_db == true`},
					{ID: "api", Label: "需兼容评审", When: `touched_api == true`},
					{ID: "else", Label: "无附加门控"},
				},
			},
			activity("dba_review", "DBA review"),
			activity("api_review", "API review"),
			activity("merge", "Merge"),
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "fix"},
			{From: "fix", To: "gates"},
			{From: "gates", To: "dba_review", FromCase: "dba"},
			{From: "gates", To: "api_review", FromCase: "api"},
			{From: "gates", To: "merge", FromCase: "else"},
			{From: "dba_review", To: "merge"},
			{From: "api_review", To: "merge"},
			{From: "merge", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("filter definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(t, "Filter gateway template", definition)
	hostID := createWorkflowHostForTest(t, "Filter gateway host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "filter-gateway-start")
	fixNode := findWorkflowNodeResponse(t, started.Nodes, "fix", 1)

	// Both conditions hold, so both reviews are owed.
	postWorkflowSubmissionPayload(t, fixNode.ID, "filter-gateway-submit", map[string]any{
		"touched_db": true, "touched_api": true,
	})
	reconcileWorkflowForTest(t, started.Instance.ID, "filter-gateway-reconcile")

	dba := latestWorkflowNodeForTest(t, started.Instance.ID, "dba_review")
	api := latestWorkflowNodeForTest(t, started.Instance.ID, "api_review")
	merge := latestWorkflowNodeForTest(t, started.Instance.ID, "merge")
	if dba.Status == "skipped" || api.Status == "skipped" {
		t.Fatalf("filter gateway skipped a matching branch: dba=%s api=%s",
			dba.Status, api.Status)
	}
	// Merge is reachable directly through the else edge, but that edge lost;
	// it must wait for the two branches that won instead of starting now.
	if merge.Status != "pending" {
		t.Fatalf("merge ran before its reviews: %s", merge.Status)
	}

	routing, err := testHandler.Queries.GetWorkflowNodeRoutingEvent(
		ctx,
		db.GetWorkflowNodeRoutingEventParams{
			WorkflowInstanceID:     parseUUID(started.Instance.ID),
			WorkspaceID:            parseUUID(testWorkspaceID),
			WorkflowNodeInstanceID: latestWorkflowNodeForTest(t, started.Instance.ID, "gates").ID,
		},
	)
	if err != nil {
		t.Fatalf("load routing event: %v", err)
	}
	var payload struct {
		CaseIDs         []string        `json:"case_ids"`
		SelectedTargets []string        `json:"selected_targets"`
		Matched         map[string]bool `json:"matched"`
		Evidence        map[string]any  `json:"evidence"`
	}
	if err := json.Unmarshal(routing.Payload, &payload); err != nil {
		t.Fatalf("decode routing payload: %v", err)
	}
	if len(payload.CaseIDs) != 2 || len(payload.SelectedTargets) != 2 {
		t.Fatalf("routing payload = %+v, want both branches", payload)
	}
	// The decision is stored with the values behind it. Read off the
	// submission instead, a later rework would rewrite the explanation of a
	// branch that had already been taken.
	if !payload.Matched["dba"] || !payload.Matched["api"] {
		t.Fatalf("matched = %v, want both gates recorded as hit", payload.Matched)
	}
	if payload.Evidence["fix.touched_db"] != true ||
		payload.Evidence["fix.touched_api"] != true {
		t.Fatalf("evidence = %v, want the submitted flags", payload.Evidence)
	}

	// Finishing both reviews releases the merge.
	for _, node := range []db.WorkflowNodeInstance{dba, api} {
		tasks, taskErr := testHandler.Queries.ListWorkflowNodeTasks(
			ctx,
			db.ListWorkflowNodeTasksParams{
				WorkflowNodeInstanceID: node.ID, WorkspaceID: parseUUID(testWorkspaceID),
			},
		)
		if taskErr != nil || len(tasks) != 1 || !tasks[0].IssueID.Valid {
			t.Fatalf("tasks for %s = %#v, err = %v", node.NodeKey, tasks, taskErr)
		}
		completeWorkflowIssue(t, uuidToString(tasks[0].IssueID))
	}
	reconcileWorkflowForTest(t, started.Instance.ID, "filter-gateway-after-reviews")
	if merged := latestWorkflowNodeForTest(t, started.Instance.ID, "merge"); merged.Status == "pending" {
		t.Fatalf("merge still pending after both reviews finished")
	}
}

// Manual rollback is the path a person drives, so the cap has to refuse it
// outright rather than quietly allowing one more round.
func TestWorkflowReworkStopsAtAttemptCap(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)

	ownerExecutor := workflowdomain.ExecutorDefinition{
		Kind: "role", Role: "owner",
		Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Rework cap",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "fix", Kind: "activity", Name: "Fix", OwnerRole: "owner",
				Executor: &ownerExecutor, IssuePolicy: "none",
				// Two attempts total: the original run and one rework.
				Completion: workflowdomain.CompletionDefinition{MaxAttempts: 2},
			},
			{
				Key: "review", Kind: "activity", Name: "Review", OwnerRole: "owner",
				Executor: &ownerExecutor, IssuePolicy: "none",
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "fix"},
			{From: "fix", To: "review"},
			{From: "review", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("rework cap definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(t, "Rework cap template", definition)
	hostID := createWorkflowHostForTest(t, "Rework cap host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "rework-cap-start")

	// Rollback re-runs the node it is called on, so it targets fix directly.
	rollback := func(key string) *httptest.ResponseRecorder {
		node := latestWorkflowNodeForTest(t, started.Instance.ID, "fix")
		recorder := httptest.NewRecorder()
		request := withURLParam(newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+uuidToString(node.ID)+
				"/rollback?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason":          "needs another pass",
				"idempotency_key": key,
			},
		), "nodeInstanceId", uuidToString(node.ID))
		testHandler.RollbackWorkflowNode(recorder, request)
		return recorder
	}

	// Move to review so there is something to roll back from.
	advanceFix := func(key string) {
		fix := latestWorkflowNodeForTest(t, started.Instance.ID, "fix")
		transitionWorkflowNode(t, uuidToString(fix.ID), "complete", key)
		reconcileWorkflowForTest(t, started.Instance.ID, key+"-reconcile")
	}
	advanceFix("rework-cap-fix-1")
	if first := rollback("rework-cap-rollback-1"); first.Code != http.StatusOK {
		t.Fatalf("first rollback status = %d, body = %s", first.Code, first.Body.String())
	}
	if attempt := latestWorkflowNodeForTest(t, started.Instance.ID, "fix").Attempt; attempt != 2 {
		t.Fatalf("fix attempt after first rollback = %d, want 2", attempt)
	}

	// The second rollback would be attempt 3, past the cap of 2.
	advanceFix("rework-cap-fix-2")
	second := rollback("rework-cap-rollback-2")
	if second.Code != http.StatusConflict {
		t.Fatalf("second rollback status = %d, want 409; body = %s",
			second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "attempts") {
		t.Fatalf("rollback refusal does not explain the cap: %s", second.Body.String())
	}
	if attempt := latestWorkflowNodeForTest(t, started.Instance.ID, "fix").Attempt; attempt != 2 {
		t.Fatalf("fix attempt after refused rollback = %d, want it unchanged at 2", attempt)
	}
}

// A node that declares output fields owes those fields. The engine
// synthesises a submission for a node with no schema once its issues are done
// — useful for nodes that owe nothing structured, but it carries none of the
// declared fields, so accepting it would complete the node with an empty
// variable pool and route the gateway to else with nobody having decided
// anything. That is the exact silent failure declared outputs exist to stop.
func TestWorkflowDeclaredOutputsAreNotSatisfiedBySynthesis(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)

	ownerExecutor := workflowdomain.ExecutorDefinition{
		Kind: "role", Role: "owner",
		Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Declared outputs",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "triage", Kind: "activity", Name: "Triage", OwnerRole: "owner",
				Executor: &ownerExecutor, IssuePolicy: "fixed",
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "triage_issue", Title: "Triage {{host.title}}", Required: true,
				}},
				// No submission_schema — the default shape, and the one that
				// gets a synthesised submission.
				Outputs: []workflowdomain.OutputField{{
					Key: "is_bug", Type: "bool", Required: true,
				}},
				Completion: workflowdomain.CompletionDefinition{RequiredIssueOutcome: "done"},
			},
			{
				Key: "route", Kind: "gateway", Name: "Route",
				Cases: []workflowdomain.GatewayCase{
					{ID: "c1", Label: "非缺陷", When: `is_bug == false`},
					{ID: "else", Label: "继续"},
				},
			},
			{Key: "closed", Kind: "end", Name: "Closed"},
			{Key: "fixing", Kind: "end", Name: "Fixing"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "triage"},
			{From: "triage", To: "route"},
			{From: "route", To: "closed", FromCase: "c1"},
			{From: "route", To: "fixing", FromCase: "else"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(t, "Declared outputs template", definition)
	hostID := createWorkflowHostForTest(t, "Declared outputs host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "declared-outputs-start")

	// Finish the issue without ever delivering the declared field.
	triage := findWorkflowNodeResponse(t, started.Nodes, "triage", 1)
	tasks, err := testHandler.Queries.ListWorkflowNodeTasks(
		context.Background(),
		db.ListWorkflowNodeTasksParams{
			WorkflowNodeInstanceID: parseUUID(triage.ID),
			WorkspaceID:            parseUUID(testWorkspaceID),
		},
	)
	if err != nil || len(tasks) != 1 || !tasks[0].IssueID.Valid {
		t.Fatalf("triage tasks = %#v, err = %v", tasks, err)
	}
	completeWorkflowIssue(t, uuidToString(tasks[0].IssueID))
	reconcileWorkflowForTest(t, started.Instance.ID, "declared-outputs-reconcile")

	node := latestWorkflowNodeForTest(t, started.Instance.ID, "triage")
	if node.Status == "completed" {
		t.Fatalf(
			"triage completed without delivering is_bug; the gateway would route on an empty pool",
		)
	}
	if !strings.Contains(string(node.WaitingReasons), "output") {
		t.Fatalf("waiting reasons do not name the missing fields: %s", node.WaitingReasons)
	}
}

// "Urgent bugs skip the queue" routes on the requirement, not on anything an
// activity produced. Reading the host issue through the same variable syntax
// as a declared field is what keeps the condition language to one idea.
func TestWorkflowGatewayRoutesOnHostIssueField(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)

	ownerExecutor := workflowdomain.ExecutorDefinition{
		Kind: "role", Role: "owner",
		Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Host issue routing",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "triage", Kind: "activity", Name: "Triage", OwnerRole: "owner",
				Executor: &ownerExecutor, IssuePolicy: "none",
			},
			{
				Key: "route", Kind: "gateway", Name: "Route",
				Cases: []workflowdomain.GatewayCase{
					{ID: "hot", Label: "加急", When: `issue.priority == "urgent"`},
					{ID: "else", Label: "常规"},
				},
			},
			{Key: "hotfix", Kind: "end", Name: "Hotfix"},
			{Key: "normal", Kind: "end", Name: "Normal"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "triage"},
			{From: "triage", To: "route"},
			{From: "route", To: "hotfix", FromCase: "hot"},
			{From: "route", To: "normal", FromCase: "else"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("host issue definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(t, "Host issue routing template", definition)

	// The host issue carries the urgency; nothing else in the run does.
	var hostID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Urgent host', 'todo', 'urgent', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create urgent host issue: %v", err)
	}
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "host-issue-routing-start")

	triage := latestWorkflowNodeForTest(t, started.Instance.ID, "triage")
	transitionWorkflowNode(t, uuidToString(triage.ID), "complete", "host-issue-routing-complete")
	reconcileWorkflowForTest(t, started.Instance.ID, "host-issue-routing-reconcile")

	hotfix := latestWorkflowNodeForTest(t, started.Instance.ID, "hotfix")
	normal := latestWorkflowNodeForTest(t, started.Instance.ID, "normal")
	if hotfix.Status != "completed" {
		t.Fatalf("urgent host did not take the hotfix branch: %s", hotfix.Status)
	}
	if normal.Status != "skipped" {
		t.Fatalf("normal branch = %s, want skipped", normal.Status)
	}
}

// Readiness already noticed a failed agent run, but the node stayed "waiting"
// and nothing was sent: the run sat on work that had stopped until somebody
// happened to open it. Blocking makes it visible, and the notification is what
// carries it to the people responsible.
func TestWorkflowFailedAgentExecutionBlocksAndNotifies(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	// The shape this matters for: an agent executes the node directly, so a
	// failed run is the only thing that can stop it.
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args
		) VALUES ($1, 'failing-agent', '', 'cloud', '{}'::jsonb, $2, 'private', 1, $3,
			'', '{}'::jsonb, '[]'::jsonb)
		RETURNING id
	`, testWorkspaceID, handlerTestRuntimeID(t), testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Agent failure",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work", OwnerRole: "owner",
				IssuePolicy: "none",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "actor", ActorType: "agent", ActorID: agentID,
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"}, {From: "work", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	templateID := createPublishedWorkflowForTest(t, "Agent failure template", definition)
	hostID := createWorkflowHostForTest(t, "Agent failure host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "agent-failure-start")

	node := latestWorkflowNodeForTest(t, started.Instance.ID, "work")
	tasks, err := testHandler.Queries.ListWorkflowNodeTasks(ctx,
		db.ListWorkflowNodeTasksParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: parseUUID(testWorkspaceID),
		})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("node tasks = %#v, err = %v", tasks, err)
	}

	// The agent took the work and failed at it.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, workflow_node_task_id, status, priority,
			context, error, failure_reason, completed_at, originator_source
		) VALUES ($1, $2, $3, 'failed', 100, '{}'::jsonb,
			'runtime exited 1', 'execution_failed', now(), 'workflow_node')
	`, agentID, handlerTestRuntimeID(t), uuidToString(tasks[0].ID)); err != nil {
		t.Fatalf("create failed agent task: %v", err)
	}

	reconcileWorkflowForTest(t, started.Instance.ID, "agent-failure-reconcile")

	swept := latestWorkflowNodeForTest(t, started.Instance.ID, "work")
	if !jsonContainsWaitingReason(swept.WaitingReasons, "direct_execution_failed") {
		t.Fatalf("failed agent run not reported on the node: %s", swept.WaitingReasons)
	}
	// Blocked, not waiting: waiting reads as a step still making its way.
	if swept.Status != "blocked" {
		t.Fatalf("node status after agent failure = %q, want blocked", swept.Status)
	}
	// The inbox is how anyone finds out; an unreported stall is the whole bug.
	var inboxCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM inbox_item
		WHERE workspace_id = $1 AND type = 'workflow_action_required'
		  AND details->>'workflow_node_instance_id' = $2
	`, testWorkspaceID, uuidToString(swept.ID)).Scan(&inboxCount); err != nil {
		t.Fatalf("count inbox items: %v", err)
	}
	if inboxCount == 0 {
		t.Fatal("nobody was told the activity's agent had failed")
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
				Roles: []workflowdomain.RoleDefinition{{
					Key: "owner", Name: "Owner", Required: true,
					AllowedActorTypes: []string{"member"},
				}},
				Nodes: []workflowdomain.NodeDefinition{
					{Key: "start", Kind: "start", Name: "Start"},
					{
						Key: "work", Kind: "activity", Name: "Work",
						OwnerRole: "owner", IssuePolicy: "fixed",
						Executor: &workflowdomain.ExecutorDefinition{
							Kind: "role", Role: "owner",
							Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
						},
						IssueTemplates: []workflowdomain.IssueTemplate{{
							Key: "required_work", Title: "Work on {{host.title}}",
							Required: true,
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
			templateID := createPublishedWorkflowForTest(
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "review", Kind: "activity", Name: "Review",
				OwnerRole: "owner", IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{Policy: "single"},
				Reviewer: &workflowdomain.ReviewerDefinition{
					Kind: "role", Role: "owner", Required: true,
				},
				Completion: workflowdomain.CompletionDefinition{
					SubmissionRequired: true,
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
	templateID := createPublishedWorkflowForTest(
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "submit", Kind: "activity", Name: "Submit",
				OwnerRole: "owner", IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{Policy: "single"},
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
	templateID := createPublishedWorkflowForTest(
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "owner", IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{Policy: "single"},
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
	templateID := createPublishedWorkflowForTest(
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "owner", IssuePolicy: "fixed",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "work", Title: "Complete {{host.title}}",
					Required: true,
				}},
				Completion: workflowdomain.CompletionDefinition{
					RequiredIssueOutcome: "done",
				},
			},
			{
				Key: "review", Kind: "activity",
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
	templateID := createPublishedWorkflowForTest(
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Parallel work",
				OwnerRole: "owner", IssuePolicy: "fixed",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{
					{Key: "first", Title: "First {{host.title}}", Required: true},
					{Key: "second", Title: "Second {{host.title}}", Required: true},
				},
				SubmissionSchema: &workflowdomain.SubmissionSchema{Policy: "per_required_task"},
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
	templateID := createPublishedWorkflowForTest(
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

	// The role is deliberately not "owner": every start path now defaults an
	// unassigned owner role to the starter, so only a differently named
	// required role can still leave a run in needs_setup.
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Needs setup",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "approver", Name: "Approver", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "approver",
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
	templateID := createPublishedWorkflowForTest(
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
	if count != 1 || !strings.Contains(string(details), `"approver"`) {
		t.Fatalf("needs-setup inbox count=%d details=%s", count, details)
	}

	roleBody := map[string]any{
		"role_assignments": []map[string]any{{
			"role_key": "approver", "actor_type": "member",
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
	templateID := createPublishedWorkflowForTest(
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

// An `auto` activity declares no template and still gets an issue. Without one
// the node runs as a bare agent task: nothing states what the work is, the
// executor has nowhere to ask, and `multica workflow` cannot resolve the node
// because every one of its commands starts from an issue id.
func TestWorkflowAutoIssuePolicyCreatesOneNamedIssue(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Auto issue",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "代码实施",
				OwnerRole: "owner", IssuePolicy: "auto",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"}, {From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("auto definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(t, "Auto issue template", definition)
	hostID := createWorkflowHostForTest(t, "导入用户")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "auto-issue-test")

	workNode := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
	var taskKey, issueTitle, issueDescription string
	if err := testPool.QueryRow(ctx, `
		SELECT task.task_key, issue.title, coalesce(issue.description, '')
		FROM workflow_node_task task
		JOIN issue ON issue.id = task.issue_id
		WHERE task.workflow_node_instance_id = $1
	`, workNode.ID).Scan(&taskKey, &issueTitle, &issueDescription); err != nil {
		t.Fatalf("auto node produced no issue-backed task: %v", err)
	}
	if taskKey != "work" {
		t.Errorf("auto task key = %q, want the reserved auto key", taskKey)
	}
	// The node name is the only statement of the work when the host issue and
	// the node description say nothing, so it has to survive into the title.
	if !strings.Contains(issueTitle, "代码实施") {
		t.Errorf("auto issue title = %q, want the node name in it", issueTitle)
	}
	if !strings.Contains(issueTitle, "导入用户") {
		t.Errorf("auto issue title = %q, want it scoped to the host issue", issueTitle)
	}
	// A reference, never a copy: the requirement keeps moving on the host issue.
	if !strings.Contains(issueDescription, "Parent requirement:") {
		t.Errorf("auto issue description = %q, want the host reference", issueDescription)
	}

	var taskCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM workflow_node_task WHERE workflow_node_instance_id = $1
	`, workNode.ID).Scan(&taskCount); err != nil {
		t.Fatalf("count auto tasks: %v", err)
	}
	if taskCount != 1 {
		t.Errorf("auto node created %d tasks, want exactly one", taskCount)
	}
}

// An attachment artifact used to be announced on the issue without being put
// there: the trace comment said a file had been delivered and the file was
// reachable only through the artifact row, so the issue showed a claim with
// nothing behind it. The delivery has to land where it was announced.
func TestWorkflowAttachmentArtifactLandsOnTheNodeIssue(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Attachment delivery",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Build the report",
				OwnerRole: "owner", IssuePolicy: "auto",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
				Artifacts: []workflowdomain.ArtifactRequirement{{
					Key: "report", Name: "result.html", Kind: "attachment", Required: true,
				}},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"}, {From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("attachment definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(t, "Attachment delivery template", definition)
	hostID := createWorkflowHostForTest(t, "周报")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "attachment-artifact-test")
	workNode := findWorkflowNodeResponse(t, started.Nodes, "work", 1)

	var attachmentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO attachment (
			workspace_id, uploader_type, uploader_id, filename, url, content_type, size_bytes
		) VALUES ($1, 'member', $2, 'result.html', 'https://example.invalid/result.html',
			'text/html; charset=utf-8', 2616)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&attachmentID); err != nil {
		t.Fatalf("create attachment: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodPost,
		"/api/workflow-node-instances/"+workNode.ID+"/artifacts?workspace_id="+testWorkspaceID,
		// No issue_id: an agent submitting through the node does not have one
		// to give, which is exactly when the trace was being skipped.
		map[string]any{"artifact_key": "report", "attachment_id": attachmentID},
	), "nodeInstanceId", workNode.ID)
	testHandler.SubmitWorkflowArtifact(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("submit attachment artifact status = %d, body = %s",
			recorder.Code, recorder.Body.String())
	}

	var boundIssue string
	if err := testPool.QueryRow(ctx, `
		SELECT coalesce(issue_id::text, '') FROM attachment WHERE id = $1
	`, attachmentID).Scan(&boundIssue); err != nil {
		t.Fatalf("read attachment binding: %v", err)
	}
	var nodeIssue string
	if err := testPool.QueryRow(ctx, `
		SELECT issue_id FROM workflow_node_task
		WHERE workflow_node_instance_id = $1 AND issue_id IS NOT NULL
	`, workNode.ID).Scan(&nodeIssue); err != nil {
		t.Fatalf("read node issue: %v", err)
	}
	if boundIssue != nodeIssue {
		t.Errorf("attachment issue_id = %q, want the node issue %q", boundIssue, nodeIssue)
	}

	var traced int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM comment WHERE issue_id = $1 AND content LIKE '%result.html%'
	`, nodeIssue).Scan(&traced); err != nil {
		t.Fatalf("count trace comments: %v", err)
	}
	if traced != 1 {
		t.Errorf("trace comments on the node issue = %d, want exactly one", traced)
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
				"workflow_id":      templateID,
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

func createPublishedWorkflowForTest(
	t *testing.T,
	name string,
	definition workflowdomain.Definition,
) string {
	t.Helper()
	ctx := context.Background()
	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id
	`, testWorkspaceID, name, testUserID).Scan(&templateID); err != nil {
		t.Fatalf("create workflow template %q: %v", name, err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_version (
			workspace_id, workflow_id, version, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, $3, 'test', $4, $4, now())
		RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create workflow template version %q: %v", name, err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow SET latest_published_version_id = $1 WHERE id = $2
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

// postWorkflowSubmissionPayload submits a node's result. Branching reads the
// declared output fields inside the payload.
func postWorkflowSubmissionPayload(
	t *testing.T,
	nodeID, idempotencyKey string,
	payload map[string]any,
) {
	t.Helper()
	body := map[string]any{
		"payload": payload, "summary": "DAG result", "idempotency_key": idempotencyKey,
	}
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(http.MethodPost, "/api/workflow-node-instances/"+nodeID+"/submissions?workspace_id="+testWorkspaceID, body), "nodeInstanceId", nodeID)
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

// assertPendingWorkflowAcceptance checks the run stopped at its end gate.
// Acceptance is no longer a node, so what proves the run is waiting is a
// pending acceptance row plus an instance that has not completed.
func assertPendingWorkflowAcceptance(t *testing.T, instanceID string) {
	t.Helper()
	acceptance, err := testHandler.Queries.GetLatestWorkflowAcceptance(
		context.Background(),
		db.GetLatestWorkflowAcceptanceParams{
			WorkflowInstanceID: parseUUID(instanceID),
			WorkspaceID:        parseUUID(testWorkspaceID),
		},
	)
	if err != nil {
		t.Fatalf("load pending acceptance: %v", err)
	}
	// The revision is not the assertion — it restarts when a rollback clears
	// an undecided round. That the run is parked on its end gate is.
	if acceptance.Status != "pending" {
		t.Fatalf("acceptance = %#v, want pending", acceptance)
	}
	instance, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
		context.Background(),
		db.GetWorkflowInstanceInWorkspaceParams{
			ID: parseUUID(instanceID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || instance.Status != "running" {
		t.Fatalf("instance awaiting acceptance = %#v, err = %v", instance, err)
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
		if _, err := testPool.Exec(ctx, `
			DELETE FROM agent_task_queue
			WHERE workflow_node_task_id IN (
				SELECT id FROM workflow_node_task WHERE workspace_id = $1
			)
		`, testWorkspaceID); err != nil {
			t.Fatalf("cleanup workflow agent tasks: %v", err)
		}
		for _, table := range []string{
			"workflow_event", "workflow_acceptance",
			"workflow_node_verdict", "workflow_node_submission", "workflow_artifact",
			"workflow_executor_resolution",
			"workflow_node_task", "workflow_node_participant", "workflow_node_instance",
			"workflow_instance_role_assignment", "workflow_instance",
			"workflow_version", "workflow",
		} {
			if _, err := testPool.Exec(ctx, "DELETE FROM "+table+" WHERE workspace_id = $1", testWorkspaceID); err != nil {
				t.Fatalf("cleanup %s: %v", table, err)
			}
		}
		// Tests that need a second member create one under a fixed address, so
		// the rows have to go too. Without this the suite passes once and then
		// fails on the unique email — a false regression that costs more to
		// diagnose than the delete costs to run.
		if _, err := testPool.Exec(ctx, `
			DELETE FROM member WHERE workspace_id = $1 AND user_id IN (
				SELECT id FROM "user" WHERE email LIKE 'workflow-%@multica.ai'
			)
		`, testWorkspaceID); err != nil {
			t.Fatalf("cleanup workflow members: %v", err)
		}
		if _, err := testPool.Exec(ctx, `
			DELETE FROM "user" WHERE email LIKE 'workflow-%@multica.ai'
		`); err != nil {
			t.Fatalf("cleanup workflow users: %v", err)
		}
		if _, err := testPool.Exec(ctx, `DELETE FROM issue WHERE workspace_id = $1 AND (title IN ('Workflow runtime host', 'Atomic workflow host', 'Workflow guard host', 'Workflow required child', 'Workflow optional child', 'Workflow DAG host', 'Workflow any join host', 'Workflow needs setup host', 'Workflow owner rollback host', 'Workflow fairness host', 'Dynamic investigation', 'Retry investigation') OR title LIKE 'Workflow executor %' OR origin_type = 'workflow')`, testWorkspaceID); err != nil {
			t.Fatalf("cleanup workflow issues: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
}

// A person rejecting a delivery and the agent Critic rejecting one are
// different judgements, and the executor is told which sent it back. Both were
// stamped "critic_rework" because the human path reuses the Critic's rework
// helper, so a member's rejection reached the agent as "the Critic rejected
// you" — a reviewer it could go argue with instead of the person who is
// actually waiting.
func TestWorkflowMemberRejectionIsNotAttributedToTheCritic(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	ctx := context.Background()
	cleanupWorkflowRuntimeTest(t)

	var hostID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Rejection attribution host', 'todo', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create host issue: %v", err)
	}

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Rejection attribution",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true, AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Implementation", OwnerRole: "owner",
				IssuePolicy: "fixed_and_dynamic",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
				IssueTemplates: []workflowdomain.IssueTemplate{{
					Key: "implementation", Title: "Implement {{host.title}}", Required: true,
				}},
				SubmissionSchema: &workflowdomain.SubmissionSchema{},
				Reviewer: &workflowdomain.ReviewerDefinition{
					Kind: "owner", Required: true,
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
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow (workspace_id, name, created_by)
		VALUES ($1, 'Rejection attribution template', $2)
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
		t.Fatalf("set published version: %v", err)
	}

	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "rejection-attribution-start")

	work := latestWorkflowNodeForTest(t, started.Instance.ID, "work")
	var workIssueID string
	if err := testPool.QueryRow(ctx, `
		SELECT issue_id::text FROM workflow_node_task
		WHERE workflow_node_instance_id = $1 AND issue_id IS NOT NULL
		ORDER BY created_at DESC LIMIT 1
	`, work.ID).Scan(&workIssueID); err != nil {
		t.Fatalf("read work issue: %v", err)
	}
	completeWorkflowIssue(t, workIssueID)
	workNodeID := uuidToString(work.ID)
	postSubmission(t, workNodeID, "rejection-attribution-submission", "first result")

	postWorkflowVerdictResult(
		t, workNodeID, "fail", "Not what was asked for", "rejection-attribution-verdict",
	)

	var action string
	if err := testPool.QueryRow(ctx, `
		SELECT payload->>'action' FROM workflow_event
		WHERE workflow_instance_id = $1 AND event_type = 'node.rollback'
		ORDER BY created_at DESC LIMIT 1
	`, started.Instance.ID).Scan(&action); err != nil {
		t.Fatalf("read rollback event: %v", err)
	}
	if action != "manual_rework" {
		t.Fatalf("member rejection recorded action %q, want manual_rework", action)
	}

	reworkIssue, err := testHandler.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID: parseUUID(workIssueID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("load rework issue: %v", err)
	}
	reworkContext := testHandler.workflowTaskContext(ctx, reworkIssue)
	if reworkContext == nil || reworkContext.Rework == nil {
		t.Fatalf("rework task context = %#v, want a rework block", reworkContext)
	}
	if reworkContext.Rework.Source != "manual_review" ||
		reworkContext.Rework.Reason != "Not what was asked for" {
		t.Fatalf("rework block = %#v", reworkContext.Rework)
	}
}
