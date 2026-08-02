package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestWorkflowNodeInputStateWaitsForAllImplicitMergePredecessors(t *testing.T) {
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Implicit merge",
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{Key: "left", Kind: "activity", Name: "Left"},
			{Key: "right", Kind: "activity", Name: "Right"},
			{Key: "merge", Kind: "activity", Name: "Merge"},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "left"},
			{From: "start", To: "right"},
			{From: "left", To: "merge"},
			{From: "right", To: "merge"},
			{From: "merge", To: "end"},
		},
	}
	plan, err := workflowdomain.BuildGraphPlan(definition)
	if err != nil {
		t.Fatalf("BuildGraphPlan() error = %v", err)
	}
	merge, ok := plan.Node("merge")
	if !ok {
		t.Fatal("merge node is missing from graph plan")
	}
	nodes := map[string]db.WorkflowNodeInstance{
		"left": {
			NodeKey: "left",
			Status:  "completed",
			Attempt: 1,
		},
		"right": {
			NodeKey: "right",
			Status:  "active",
			Attempt: 2,
		},
	}
	h := &Handler{}

	selected, settled, _, err := h.workflowNodeInputState(
		context.Background(),
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		plan,
		nodes,
		merge,
	)
	if err != nil {
		t.Fatalf("workflowNodeInputState() error = %v", err)
	}
	if selected || settled {
		t.Fatalf(
			"workflowNodeInputState() selected=%v settled=%v, want false and false",
			selected,
			settled,
		)
	}

	right := nodes["right"]
	right.Status = "completed"
	nodes["right"] = right
	selected, settled, maxAttempt, err := h.workflowNodeInputState(
		context.Background(),
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		plan,
		nodes,
		merge,
	)
	if err != nil {
		t.Fatalf("workflowNodeInputState() after completion error = %v", err)
	}
	if !selected || !settled || maxAttempt != 2 {
		t.Fatalf(
			"workflowNodeInputState() selected=%v settled=%v maxAttempt=%d, want true, true, 2",
			selected,
			settled,
			maxAttempt,
		)
	}
}
