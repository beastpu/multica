package handler

import "testing"

// Every ordinary issue in the workspace runs through this parser on claim, so
// "not a workflow issue" has to be a clean miss rather than an error path —
// and a half-written block must not produce coordinates that would then be
// queried against.
func TestReadWorkflowIssueCoordinates(t *testing.T) {
	cases := []struct {
		name     string
		metadata string
		want     bool
	}{
		{name: "empty", metadata: "", want: false},
		{name: "null", metadata: "null", want: false},
		{name: "no workflow block", metadata: `{"other":{"a":1}}`, want: false},
		{name: "malformed json", metadata: `{"workflow":`, want: false},
		{
			name:     "workflow block wrong type",
			metadata: `{"workflow":"nope"}`,
			want:     false,
		},
		{
			name:     "missing node key",
			metadata: `{"workflow":{"instance_id":"run-1"}}`,
			want:     false,
		},
		{
			name:     "missing instance id",
			metadata: `{"workflow":{"node_key":"implement"}}`,
			want:     false,
		},
		{
			name:     "blank node key",
			metadata: `{"workflow":{"instance_id":"run-1","node_key":"  "}}`,
			want:     false,
		},
		{
			name:     "complete",
			metadata: `{"workflow":{"instance_id":"run-1","node_key":"implement","host_issue":"MUL-1"}}`,
			want:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := readWorkflowIssueCoordinates([]byte(tc.metadata))
			if ok != tc.want {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tc.want, got)
			}
			if !tc.want {
				return
			}
			if got.InstanceID != "run-1" || got.NodeKey != "implement" ||
				got.HostIssue != "MUL-1" {
				t.Errorf("unexpected coordinates: %+v", got)
			}
		})
	}
}
