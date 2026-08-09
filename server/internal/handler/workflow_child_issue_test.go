package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkflowTaskIssueDescription(t *testing.T) {
	tests := []struct {
		name            string
		template        string
		node            string
		host            string
		hostDescription string
		purpose         string
		want            string
	}{
		{
			name:     "template instructions carry the reference",
			template: "Produce the technical design.",
			node:     "Design node",
			host:     "MUL-123",
			want:     "Produce the technical design.\n\n> Parent requirement: MUL-123",
		},
		{
			// The identifier alone is not something a person can work from in a
			// notification or on a phone, which is where most executors meet
			// the child issue first.
			name:            "the requirement is quoted under its reference",
			template:        "Produce the technical design.",
			host:            "MUL-123",
			hostDescription: "Import users from a CSV file.\nValidate each row.",
			want: "Produce the technical design.\n\n" +
				"> Parent requirement: MUL-123\n>\n" +
				"> Import users from a CSV file.\n> Validate each row.",
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
		{
			// A run started without a host issue has nothing that states the
			// requirement, so the workflow's own purpose is what the executor
			// gets — marked as the weaker thing it is.
			name:    "a run with no host issue falls back to the workflow purpose",
			node:    "Design node",
			purpose: "Ship a small feature end to end.",
			want:    "Design node\n\n> What this workflow is for: Ship a small feature end to end.",
		},
		{
			// The host issue is the actual requirement. Printing the workflow's
			// generic purpose beside it would compete with the real one.
			name:    "the host reference wins over the workflow purpose",
			node:    "Design node",
			host:    "MUL-123",
			purpose: "Ship a small feature end to end.",
			want:    "Design node\n\n> Parent requirement: MUL-123",
		},
		{
			name:    "purpose alone is still worth writing",
			purpose: "Ship a small feature end to end.",
			want:    "> What this workflow is for: Ship a small feature end to end.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := workflowTaskIssueDescription(
				test.template, test.node, test.host, test.hostDescription, test.purpose,
			)
			if !got.Valid || got.String != test.want {
				t.Errorf("workflowTaskIssueDescription() = %q (valid=%v), want %q",
					got.String, got.Valid, test.want)
			}
		})
	}
}

func TestWorkflowTaskIssueDescriptionIsEmptyWithoutContent(t *testing.T) {
	got := workflowTaskIssueDescription("", "", "", "", "")
	if got.Valid {
		t.Errorf("workflowTaskIssueDescription() = %q, want an unset value", got.String)
	}
}

// The excerpt is a convenience, not the record. It always travels under the
// identifier, because the host issue keeps being edited and this copy does not
// follow — a reader who needs the current text has to be told where it lives.
func TestWorkflowTaskIssueDescriptionQuotesUnderTheReference(t *testing.T) {
	hostDescription := "Import users from a CSV file, validating each row."
	got := workflowTaskIssueDescription(
		"Review the requirement.", "", "MUL-123", hostDescription, "",
	)
	if !strings.Contains(got.String, "MUL-123") {
		t.Errorf("description dropped the host reference: %q", got.String)
	}
	if !strings.Contains(got.String, "> "+hostDescription) {
		t.Errorf("description did not quote the requirement: %q", got.String)
	}
	if strings.Index(got.String, "MUL-123") > strings.Index(got.String, hostDescription) {
		t.Errorf("the excerpt must sit under its reference: %q", got.String)
	}
}

// A long requirement would otherwise bury the node's own instructions, and the
// identifier above the excerpt is what the reader follows for the rest.
func TestWorkflowTaskIssueDescriptionTruncatesALongRequirement(t *testing.T) {
	got := workflowTaskIssueDescription(
		"", "", "MUL-123", strings.Repeat("借", maxHostExcerptRunes+50), "",
	)
	if !strings.HasSuffix(got.String, "\n> …") {
		t.Errorf("a truncated excerpt must say so: %q", got.String[len(got.String)-40:])
	}
	if quoted := strings.Count(got.String, "借"); quoted != maxHostExcerptRunes {
		t.Errorf("quoted %d runes, want the %d-rune cap", quoted, maxHostExcerptRunes)
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
