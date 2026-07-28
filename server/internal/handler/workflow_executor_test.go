package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func conditionalExecutorNode() workflowdomain.NodeDefinition {
	return workflowdomain.NodeDefinition{
		Key: "implement", Kind: "activity", Name: "Implement",
		Executor: workflowdomain.ExecutorDefinition{Strategies: []workflowdomain.ExecutorStrategy{
			{
				Kind: "fixed_role", Role: "agent_role",
				Condition: json.RawMessage(
					`{"source":"node_choice","node":"triage","key":"choice","op":"eq","value":"review"}`,
				),
			},
			{Kind: "fixed_role", Role: "human_role"},
			{Kind: "manual"},
		}},
	}
}

func executorTestRoles() map[string]validatedWorkflowRoleAssignment {
	agentID := parseUUID("11111111-1111-1111-1111-111111111111")
	memberID := parseUUID("22222222-2222-2222-2222-222222222222")
	return map[string]validatedWorkflowRoleAssignment{
		"agent_role": {RoleKey: "agent_role", ActorType: "agent", ActorID: agentID},
		"human_role": {RoleKey: "human_role", ActorType: "member", ActorID: memberID},
	}
}

func staticConditionEvaluator(matched bool, err error) workflowConditionEvaluator {
	return func(json.RawMessage) (bool, error) { return matched, err }
}

func TestResolveWorkflowTaskExecutorSkipsUnmatchedConditionalStrategy(t *testing.T) {
	decision, err := resolveWorkflowTaskExecutor(
		context.Background(), nil, db.Workspace{}.ID, db.WorkflowInstance{},
		conditionalExecutorNode(), workflowdomain.IssueTemplate{Key: "impl", Title: "Implement"},
		executorTestRoles(), staticConditionEvaluator(false, nil),
	)
	if err != nil {
		t.Fatalf("resolveWorkflowTaskExecutor() error = %v", err)
	}
	if decision.Assignment == nil || decision.Assignment.RoleKey != "human_role" {
		t.Fatalf(
			"decision = %+v, want fallthrough to human_role when condition is false",
			decision,
		)
	}
}

func TestResolveWorkflowTaskExecutorUsesMatchedConditionalStrategy(t *testing.T) {
	decision, err := resolveWorkflowTaskExecutor(
		context.Background(), nil, db.Workspace{}.ID, db.WorkflowInstance{},
		conditionalExecutorNode(), workflowdomain.IssueTemplate{Key: "impl", Title: "Implement"},
		executorTestRoles(), staticConditionEvaluator(true, nil),
	)
	if err != nil {
		t.Fatalf("resolveWorkflowTaskExecutor() error = %v", err)
	}
	if decision.Assignment == nil || decision.Assignment.RoleKey != "agent_role" {
		t.Fatalf(
			"decision = %+v, want agent_role when condition matches",
			decision,
		)
	}
}

func TestResolveWorkflowTaskExecutorPropagatesConditionErrors(t *testing.T) {
	wantErr := errors.New("condition evaluation failed")
	_, err := resolveWorkflowTaskExecutor(
		context.Background(), nil, db.Workspace{}.ID, db.WorkflowInstance{},
		conditionalExecutorNode(), workflowdomain.IssueTemplate{Key: "impl", Title: "Implement"},
		executorTestRoles(), staticConditionEvaluator(false, wantErr),
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("resolveWorkflowTaskExecutor() error = %v, want %v", err, wantErr)
	}
}
