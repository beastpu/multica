package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkflowTaskIssueDescription(t *testing.T) {
	tests := []struct {
		name     string
		template string
		node     string
		host     string
		want     string
	}{
		{
			name:     "template instructions carry the reference",
			template: "Produce the technical design.",
			node:     "Design node",
			host:     "MUL-123",
			want:     "Produce the technical design.\n\n> Parent requirement: MUL-123",
		},
		{
			name:     "node description is the fallback when the template omits one",
			template: "   ",
			node:     "Design node",
			host:     "MUL-123",
			want:     "Design node\n\n> Parent requirement: MUL-123",
		},
		{
			name: "reference alone is still worth writing",
			host: "MUL-123",
			want: "> Parent requirement: MUL-123",
		},
		{
			name:     "no host identifier leaves the instructions untouched",
			template: "Produce the technical design.",
			want:     "Produce the technical design.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := workflowTaskIssueDescription(test.template, test.node, test.host)
			if !got.Valid || got.String != test.want {
				t.Errorf("workflowTaskIssueDescription() = %q (valid=%v), want %q",
					got.String, got.Valid, test.want)
			}
		})
	}
}

func TestWorkflowTaskIssueDescriptionIsEmptyWithoutContent(t *testing.T) {
	got := workflowTaskIssueDescription("", "", "")
	if got.Valid {
		t.Errorf("workflowTaskIssueDescription() = %q, want an unset value", got.String)
	}
}

// The reference points at the host issue; copying its description would give
// every node child issue a private snapshot that stops following the original.
func TestWorkflowTaskIssueDescriptionReferencesRatherThanCopies(t *testing.T) {
	hostDescription := "Import users from a CSV file, validating each row."
	got := workflowTaskIssueDescription("Review the requirement.", "", "MUL-123")
	if strings.Contains(got.String, hostDescription) {
		t.Errorf("description embedded the host requirement: %q", got.String)
	}
	if !strings.Contains(got.String, "MUL-123") {
		t.Errorf("description dropped the host reference: %q", got.String)
	}
}

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
