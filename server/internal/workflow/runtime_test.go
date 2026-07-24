package workflow

import (
	"encoding/json"
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

func TestValidateSubmissionPayload(t *testing.T) {
	schema := &SubmissionSchema{Fields: []SubmissionField{
		{Key: "summary", Name: "Summary", Type: "text", Required: true},
		{Key: "passed", Name: "Passed", Type: "boolean", Required: true},
		{Key: "date", Name: "Date", Type: "date"},
	}}
	valid := map[string]any{"summary": "done", "passed": true, "date": "2026-07-23"}
	if reasons := ValidateSubmissionPayload(schema, valid); len(reasons) != 0 {
		t.Fatalf("valid payload reasons = %#v", reasons)
	}

	invalidJSON := []byte(`{"passed":"yes","date":"not-a-date"}`)
	var invalid map[string]any
	if err := json.Unmarshal(invalidJSON, &invalid); err != nil {
		t.Fatal(err)
	}
	reasons := ValidateSubmissionPayload(schema, invalid)
	if len(reasons) != 3 {
		t.Fatalf("invalid payload reason count = %d, want 3: %#v", len(reasons), reasons)
	}
}
