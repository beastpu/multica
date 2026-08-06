package workflow

import "testing"

// The mapping is the whole policy of the node→issue mirror, and it is pure, so
// it is testable without a database — which is what makes it the right place
// to put the rules that decide whether a run may overwrite what a person did.
func TestNodeIssueStatus(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		current string
		want    string
		wantOK  bool
	}{
		{"activation starts a todo carrier", "activated", "todo", "in_progress", true},
		{"activation starts a backlog carrier", "activated", "backlog", "in_progress", true},
		{
			name: "activation leaves work already under way alone",
			event: "activated", current: "in_progress", wantOK: false,
		},
		{"completion closes the carrier", "completed", "in_progress", "done", true},
		{"completion closes a carrier left in review", "completed", "in_review", "done", true},
		{"blocking marks work that had started", "blocked", "in_progress", "blocked", true},
		{"blocking marks work waiting on review", "blocked", "in_review", "blocked", true},
		{
			// An unstarted issue is waiting, not blocked. Saying otherwise
			// turns every node that has not begun into an alarm.
			name: "blocking leaves unstarted work alone",
			event: "blocked", current: "todo", wantOK: false,
		},
		{"a skipped node cancels its carrier", "skipped", "todo", "cancelled", true},

		// The rule that matters most: a person's terminal decision stands.
		{
			name: "a closed carrier is never reopened by activation",
			event: "activated", current: "done", wantOK: false,
		},
		{
			name: "a closed carrier is never re-closed",
			event: "completed", current: "done", wantOK: false,
		},
		{
			name: "a cancelled carrier is not completed by the run",
			event: "completed", current: "cancelled", wantOK: false,
		},
		{
			name: "a cancelled carrier is not blocked by the run",
			event: "blocked", current: "cancelled", wantOK: false,
		},
		{"an unknown transition changes nothing", "superseded", "in_progress", "", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := NodeIssueStatus(testCase.event, testCase.current)
			if ok != testCase.wantOK || got != testCase.want {
				t.Fatalf(
					"NodeIssueStatus(%q, %q) = (%q, %v), want (%q, %v)",
					testCase.event, testCase.current, got, ok,
					testCase.want, testCase.wantOK,
				)
			}
		})
	}
}
