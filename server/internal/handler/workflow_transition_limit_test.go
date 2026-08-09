package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// A run that reaches the transition safety limit has to leave a trace and reach
// a person.
//
// Every other way a run stops making progress writes a waiting reason and
// raises an intervention. Exhausting the limit returned a bare error that
// reached a log line and nothing else: the run still read as 'running', the UI
// showed nothing wrong, and because the transitions it had just written made it
// due again, the reconciler re-claimed it every cycle to spend the same work.
func TestWorkflowTransitionLimitIsRecordedAndRaised(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	instance := startTransitionLimitFixture(t, "recorded")

	testHandler.recordWorkflowTransitionLimit(
		ctx, instance, 128, map[string]int{"work": 64, "review": 64},
	)

	// The event is the durable account: it names the run and the activities that
	// kept completing, which is where a reader has to start.
	var payload string
	if err := testPool.QueryRow(ctx, `
		SELECT payload::text FROM workflow_event
		WHERE workflow_instance_id = $1
		  AND event_type = 'workflow.transition_limit_exceeded'
	`, uuidToString(instance.ID)).Scan(&payload); err != nil {
		t.Fatalf("the exhausted reconcile left no event: %v", err)
	}
	for _, want := range []string{"128", `"work": 64`, `"review": 64`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("event payload does not carry %q: %s", want, payload)
		}
	}

	// And somebody is told. A run that cannot settle is not a state the platform
	// recovers from on its own.
	var inboxCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM inbox_item
		WHERE workspace_id = $1 AND type = 'workflow_action_required'
		  AND details->>'reason' = 'transition_limit'
		  AND details->>'workflow_instance_id' = $2
	`, testWorkspaceID, uuidToString(instance.ID)).Scan(&inboxCount); err != nil {
		t.Fatalf("count inbox items: %v", err)
	}
	if inboxCount == 0 {
		t.Fatal("nobody was told the run had stopped settling")
	}
}

// The same cycle raises one finding; a different cycle is a new finding.
//
// The reconciler retries a deferred instance every cycle, so a key that did not
// depend on what the run is doing would either spam the inbox once every thirty
// seconds or — if fixed per run — hide a genuinely different stall behind the
// first one ever seen.
func TestWorkflowTransitionLimitRaisesOncePerCycle(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	instance := startTransitionLimitFixture(t, "repeat")
	sameCycle := map[string]int{"work": 64, "review": 64}

	testHandler.recordWorkflowTransitionLimit(ctx, instance, 128, sameCycle)
	testHandler.recordWorkflowTransitionLimit(ctx, instance, 128, sameCycle)

	if events := countTransitionLimitEvents(t, instance); events != 1 {
		t.Fatalf("the same stall was filed %d times, want 1", events)
	}

	// A different subset of the graph is a different problem, and has to be
	// visible even though the run already has a finding against it.
	testHandler.recordWorkflowTransitionLimit(
		ctx, instance, 128, map[string]int{"work": 128},
	)
	if events := countTransitionLimitEvents(t, instance); events != 2 {
		t.Fatalf("a different stall was filed %d times, want 2 total", events)
	}
}

func countTransitionLimitEvents(t *testing.T, instance db.WorkflowInstance) int {
	t.Helper()
	var events int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM workflow_event
		WHERE workflow_instance_id = $1
		  AND event_type = 'workflow.transition_limit_exceeded'
	`, uuidToString(instance.ID)).Scan(&events); err != nil {
		t.Fatalf("count transition limit events: %v", err)
	}
	return events
}

// startTransitionLimitFixture starts a minimal run. The limit itself is not
// reachable from a valid definition — the graph is acyclic and rework is
// capped — so the recorder is exercised directly against a real run rather
// than through a manufactured pathological workflow.
func startTransitionLimitFixture(t *testing.T, name string) db.WorkflowInstance {
	t.Helper()
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Transition limit",
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{Key: "work", Kind: "activity", Name: "Work", IssuePolicy: "auto"},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"}, {From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("transition limit definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(
		t, "Transition limit template "+name, definition,
	)
	hostID := createWorkflowHostForTest(t, "Transition limit host "+name)
	started := startWorkflowForTest(
		t, hostID, templateID, nil, "transition-limit-start-"+name,
	)
	instance, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
		context.Background(),
		db.GetWorkflowInstanceInWorkspaceParams{
			ID:          parseUUID(started.Instance.ID),
			WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil {
		t.Fatalf("load started run: %v", err)
	}
	return instance
}
