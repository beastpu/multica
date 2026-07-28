package workflow

import (
	"testing"
)

func TestBuildSerialPlan(t *testing.T) {
	definition := validDefinition()
	plan, err := BuildSerialPlan(definition)
	if err != nil {
		t.Fatalf("BuildSerialPlan() error = %v", err)
	}
	if len(plan.Ordered) != 4 {
		t.Fatalf("len(Ordered) = %d, want 4", len(plan.Ordered))
	}
	next, ok := plan.Next("implementation")
	if !ok || next.Key != "acceptance" {
		t.Fatalf("Next(implementation) = (%q, %v), want acceptance", next.Key, ok)
	}
}

func TestBuildSerialPlanRejectsBranch(t *testing.T) {
	definition := validDefinition()
	definition.Nodes = append(definition.Nodes, NodeDefinition{Key: "extra", Kind: "end", Name: "Extra"})
	definition.Edges = append(definition.Edges, EdgeDefinition{From: "implementation", To: "extra"})
	if _, err := BuildSerialPlan(definition); err == nil {
		t.Fatal("BuildSerialPlan() accepted a branch")
	}
}
