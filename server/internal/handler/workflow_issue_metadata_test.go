package handler

import (
	"encoding/json"
	"testing"
)

func TestWorkflowIssueMetadataCarriesInvariants(t *testing.T) {
	raw, err := workflowIssueMetadata("11111111-1111-1111-1111-111111111111", "design", "MUL-123")
	if err != nil {
		t.Fatalf("workflowIssueMetadata() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	want := map[string]any{
		"instance_id": "11111111-1111-1111-1111-111111111111",
		"node_key":    "design",
		"host_issue":  "MUL-123",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("metadata[%q] = %v, want %v", key, got[key], value)
		}
	}
}

// The payload is a snapshot taken while the graph is still live, so it must not
// carry anything that changes during the run. Upstream/downstream edges shift
// when a template is republished or a node is skipped or rolled back, and a
// stale copy behind a GIN index would be read as authoritative.
func TestWorkflowIssueMetadataOmitsMutableGraphState(t *testing.T) {
	raw, err := workflowIssueMetadata("instance", "design", "MUL-123")
	if err != nil {
		t.Fatalf("workflowIssueMetadata() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("metadata has %d keys (%v), want exactly the 3 invariants", len(got), got)
	}
	for _, mutable := range []string{
		"upstream", "downstream", "status", "summary", "artifacts", "attempt",
	} {
		if _, exists := got[mutable]; exists {
			t.Errorf("metadata must not carry mutable key %q", mutable)
		}
	}
}
