package handler

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/service"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The verdict WTE-14841's Critic actually produced. It rejected a fix that had
// deleted the button it was meant to wire up, with four findings — a correct
// review the protocol could not read, because the keys are its own.
const unreadableCriticVerdict = `{"verdict":"reject","reason":"the fix removed the button instead of wiring it","blocking_findings":["schema.ts still holds a single domain"],"confidence":0.93}`

// A Critic gets one chance to restate an unreadable verdict before a human is
// called. Blocking on the first bad shape threw away a review that had already
// been done, and asking forever would let a Critic stuck on its own vocabulary
// spin without end.
func TestWorkflowAgentCriticRetriesOneUnreadableVerdict(t *testing.T) {
	for _, test := range []struct {
		name        string
		retryOutput string
		wantBlocked bool
		wantAttempt int32
	}{
		{
			name:        "restated verdict is honoured",
			retryOutput: `{"result":"fail","reason":"the fix removed the button instead of wiring it"}`,
			wantBlocked: false,
			wantAttempt: 2, // rejection sent the node to rework
		},
		{
			name:        "a second unreadable verdict blocks",
			retryOutput: unreadableCriticVerdict,
			wantBlocked: true,
			wantAttempt: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
			cleanupWorkflowRuntimeTest(t)
			ctx := context.Background()
			workerID := createHandlerTestAgent(t, "critic-retry-worker-"+test.name, nil)
			criticID := createHandlerTestAgent(t, "critic-retry-reviewer-"+test.name, nil)

			work, instanceID := startCriticRetryFixture(t, workerID, criticID, test.name)
			criticTask := latestCriticTaskForTest(t, work)

			// First verdict: sound, unreadable.
			if err := testHandler.recordWorkflowAgentCriticVerdict(
				ctx, criticTask, unreadableCriticVerdict,
			); err != nil {
				t.Fatalf("record first Critic verdict: %v", err)
			}

			node := latestWorkflowNodeForTest(t, instanceID, "work")
			if node.Status == "blocked" {
				t.Fatalf("first unreadable verdict blocked the node instead of re-asking")
			}
			if node.LatestVerdictID.Valid {
				t.Fatalf("a verdict was recorded from output that could not be read")
			}

			retryTask := latestCriticTaskForTest(t, work)
			if uuidToString(retryTask.ID) == uuidToString(criticTask.ID) {
				t.Fatal("no retry task was enqueued")
			}
			direct, ok := service.ParseWorkflowNodeTaskContext(retryTask)
			if !ok || direct.VerdictRetry == nil {
				t.Fatalf("retry task carries no verdict retry: %#v", direct)
			}
			if !strings.Contains(direct.VerdictRetry.Problem, "unknown field") {
				t.Fatalf("retry does not name the objection: %q", direct.VerdictRetry.Problem)
			}
			if direct.VerdictRetry.Wrote != unreadableCriticVerdict {
				t.Fatalf("retry does not quote the Critic: %q", direct.VerdictRetry.Wrote)
			}
			if uuidToString(retryTask.AgentID) != criticID {
				t.Fatalf("retry went to a different reviewer: %s", uuidToString(retryTask.AgentID))
			}

			// Second verdict.
			if err := testHandler.recordWorkflowAgentCriticVerdict(
				ctx, retryTask, test.retryOutput,
			); err != nil {
				t.Fatalf("record retry Critic verdict: %v", err)
			}

			final := latestWorkflowNodeForTest(t, instanceID, "work")
			if blocked := final.Status == "blocked" &&
				jsonContainsWaitingReason(final.WaitingReasons, "verdict_blocked"); blocked != test.wantBlocked {
				t.Fatalf(
					"blocked = %v, want %v (status=%q reasons=%s)",
					blocked, test.wantBlocked, final.Status, final.WaitingReasons,
				)
			}
			if final.Attempt != test.wantAttempt {
				t.Fatalf("work attempt = %d, want %d", final.Attempt, test.wantAttempt)
			}

			// A third unreadable verdict must not queue a fourth Critic.
			if test.wantBlocked {
				after := latestCriticTaskForTest(t, work)
				if uuidToString(after.ID) != uuidToString(retryTask.ID) {
					t.Fatal("a second retry was enqueued; the retry is not bounded to one")
				}
			}
		})
	}
}

// startCriticRetryFixture runs a one-activity workflow up to the point where
// its Critic has been dispatched, and returns the node instance id and the
// run id.
func startCriticRetryFixture(
	t *testing.T,
	workerID, criticID, name string,
) (nodeInstanceID string, instanceID string) {
	t.Helper()
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Critic retry",
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
		t.Fatalf("critic retry definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(t, "Critic retry template "+name, definition)
	hostID := createWorkflowHostForTest(t, "Critic retry host "+name)
	started := startWorkflowForTest(t, hostID, templateID, nil, "critic-retry-start-"+name)
	work := findWorkflowNodeResponse(t, started.Nodes, "work", 1)

	var executionTaskID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM workflow_node_task
		WHERE workflow_node_instance_id = $1 AND source = 'execution'
	`, work.ID).Scan(&executionTaskID); err != nil {
		t.Fatalf("load Worker task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_queue
		SET status = 'completed', result = $2, completed_at = now()
		WHERE workflow_node_task_id = $1
	`, executionTaskID, []byte(`{"output":"implemented the requested behavior"}`)); err != nil {
		t.Fatalf("complete Worker task: %v", err)
	}
	if _, err := testHandler.Queries.CreateWorkflowArtifact(
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
	); err != nil {
		t.Fatalf("submit Worker artifact: %v", err)
	}
	if _, err := testHandler.reconcileWorkflowInstance(
		ctx, parseUUID(testWorkspaceID), parseUUID(started.Instance.ID),
		"system", pgtype.UUID{}, "critic-retry-delivered-"+name,
	); err != nil && !errors.Is(err, errWorkflowNoop) {
		t.Fatalf("reconcile Worker delivery: %v", err)
	}
	return work.ID, started.Instance.ID
}

func latestCriticTaskForTest(t *testing.T, nodeInstanceID string) db.AgentTaskQueue {
	t.Helper()
	ctx := context.Background()
	var carrierID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM workflow_node_task
		WHERE workflow_node_instance_id = $1 AND source = 'critic'
	`, nodeInstanceID).Scan(&carrierID); err != nil {
		t.Fatalf("load Critic carrier: %v", err)
	}
	task, err := testHandler.Queries.GetLatestAgentTaskForWorkflowNodeTask(
		ctx, parseUUID(carrierID),
	)
	if err != nil {
		t.Fatalf("load Critic agent task: %v", err)
	}
	return task
}

// A node whose reviewer is an agent must not report that it needs a manual
// executor. The critic carrier never holds an executor resolution — the
// reviewer comes from the role binding at dispatch — so the sweeper's
// "unresolved executor" test matched it on every pass, and every reviewed node
// carried a demand for work nobody could perform.
func TestSweeperDoesNotAskForAnExecutorForTheCritic(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	workerID := createHandlerTestAgent(t, "critic-executor-worker", nil)
	criticID := createHandlerTestAgent(t, "critic-executor-reviewer", nil)

	work, instanceID := startCriticRetryFixture(t, workerID, criticID, "executor")

	var criticSource string
	if err := testPool.QueryRow(ctx, `
		SELECT source FROM workflow_node_task
		WHERE workflow_node_instance_id = $1 AND source = 'critic'
	`, work).Scan(&criticSource); err != nil {
		t.Fatalf("the fixture produced no critic carrier: %v", err)
	}
	// The premise: it has no executor resolution, and never will.
	var resolved bool
	if err := testPool.QueryRow(ctx, `
		SELECT executor_resolution_id IS NOT NULL FROM workflow_node_task
		WHERE workflow_node_instance_id = $1 AND source = 'critic'
	`, work).Scan(&resolved); err != nil {
		t.Fatalf("load critic carrier: %v", err)
	}
	if resolved {
		t.Skip("critic carriers now carry an executor resolution; this guard is obsolete")
	}

	if err := NewWorkflowSweeper(testHandler).SweepOnce(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	node := latestWorkflowNodeForTest(t, instanceID, "work")
	if jsonContainsWaitingReason(node.WaitingReasons, "executor_unresolved") {
		t.Fatalf(
			"an agent-reviewed node was told it needs a manual executor: %s",
			node.WaitingReasons,
		)
	}
}
