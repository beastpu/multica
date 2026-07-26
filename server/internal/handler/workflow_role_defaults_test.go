package handler

import (
	"testing"

	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
)

func TestApplyWorkflowRoleDefaults(t *testing.T) {
	definition := workflowdomain.Definition{
		Roles: []workflowdomain.RoleDefinition{
			{
				Key: "fixer", Name: "Fixer", Required: true,
				AllowedActorTypes: []string{"agent"},
				DefaultActorType:  "agent",
				DefaultActorID:    "11111111-1111-1111-1111-111111111111",
			},
			{
				Key: "qa", Name: "QA", Required: true,
				AllowedActorTypes: []string{"member"},
			},
		},
	}

	applied := applyWorkflowRoleDefaults(definition, nil)
	if len(applied) != 1 {
		t.Fatalf("applyWorkflowRoleDefaults() = %d assignments, want 1", len(applied))
	}
	if applied[0].RoleKey != "fixer" || applied[0].Source != "fixed" ||
		applied[0].ActorType != "agent" {
		t.Fatalf("unexpected default assignment %+v", applied[0])
	}

	explicit := []workflowRoleAssignmentInput{{
		RoleKey: "fixer", ActorType: "agent",
		ActorID: "22222222-2222-2222-2222-222222222222", Source: "user_selected",
	}}
	kept := applyWorkflowRoleDefaults(definition, explicit)
	if len(kept) != 1 || kept[0].ActorID != explicit[0].ActorID {
		t.Fatalf("explicit assignment was overridden: %+v", kept)
	}
}
