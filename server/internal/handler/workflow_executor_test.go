package handler

import (
	"context"
	"testing"

	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func executorTestRoles() map[string]validatedWorkflowRoleAssignment {
	agentID := parseUUID("11111111-1111-1111-1111-111111111111")
	memberID := parseUUID("22222222-2222-2222-2222-222222222222")
	return map[string]validatedWorkflowRoleAssignment{
		"agent_role": {RoleKey: "agent_role", ActorType: "agent", ActorID: agentID},
		"human_role": {RoleKey: "human_role", ActorType: "member", ActorID: memberID},
	}
}

func executorNode(executor workflowdomain.ExecutorDefinition) workflowdomain.NodeDefinition {
	return workflowdomain.NodeDefinition{
		Key: "implement", Kind: "activity", Name: "Implement", Executor: &executor,
	}
}

func TestResolveWorkflowNodeExecutorUsesTheNamedRole(t *testing.T) {
	decision, err := resolveWorkflowNodeExecutor(
		context.Background(), nil, db.Workspace{}.ID,
		executorNode(workflowdomain.ExecutorDefinition{
			Kind: "role", Role: "agent_role",
			Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
		}),
		executorTestRoles(),
	)
	if err != nil {
		t.Fatalf("resolveWorkflowNodeExecutor() error = %v", err)
	}
	if decision.Assignment == nil || decision.Assignment.RoleKey != "agent_role" {
		t.Fatalf("decision = %+v, want agent_role", decision)
	}
	if decision.Strategy != "fixed_role" {
		t.Fatalf("strategy = %q, want fixed_role", decision.Strategy)
	}
}

func TestResolveWorkflowNodeExecutorFallsBackWhenTheRoleIsUnassigned(t *testing.T) {
	decision, err := resolveWorkflowNodeExecutor(
		context.Background(), nil, db.Workspace{}.ID,
		executorNode(workflowdomain.ExecutorDefinition{
			Kind: "role", Role: "unassigned_role",
			Fallback: &workflowdomain.ExecutorDefinition{Kind: "role", Role: "human_role"},
		}),
		executorTestRoles(),
	)
	if err != nil {
		t.Fatalf("resolveWorkflowNodeExecutor() error = %v", err)
	}
	if decision.Assignment == nil || decision.Assignment.RoleKey != "human_role" {
		t.Fatalf("decision = %+v, want fallback to human_role", decision)
	}
	if decision.Strategy != "fallback_role" {
		t.Fatalf("strategy = %q, want fallback_role", decision.Strategy)
	}
}

func TestResolveWorkflowNodeExecutorFallsToManual(t *testing.T) {
	for _, test := range []struct {
		name     string
		executor workflowdomain.ExecutorDefinition
	}{
		{
			name:     "no executor at all",
			executor: workflowdomain.ExecutorDefinition{},
		},
		{
			name: "unassigned role with no fallback",
			executor: workflowdomain.ExecutorDefinition{
				Kind: "role", Role: "unassigned_role",
			},
		},
		{
			name: "unassigned role falling back to manual",
			executor: workflowdomain.ExecutorDefinition{
				Kind: "role", Role: "unassigned_role",
				Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, err := resolveWorkflowNodeExecutor(
				context.Background(), nil, db.Workspace{}.ID,
				executorNode(test.executor), executorTestRoles(),
			)
			if err != nil {
				t.Fatalf("resolveWorkflowNodeExecutor() error = %v", err)
			}
			if decision.Assignment != nil {
				t.Fatalf("decision = %+v, want manual selection", decision)
			}
			if decision.Strategy != "manual" {
				t.Fatalf("strategy = %q, want manual", decision.Strategy)
			}
		})
	}
}
