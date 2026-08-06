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
			name:  "activation leaves work already under way alone",
			event: "activated", current: "in_progress", wantOK: false,
		},
		{"completion closes the carrier", "completed", "in_progress", "done", true},
		{"completion closes a carrier left in review", "completed", "in_review", "done", true},
		{"blocking marks work that had started", "blocked", "in_progress", "blocked", true},
		{"blocking marks work waiting on review", "blocked", "in_review", "blocked", true},
		{
			// An unstarted issue is waiting, not blocked. Saying otherwise
			// turns every node that has not begun into an alarm.
			name:  "blocking leaves unstarted work alone",
			event: "blocked", current: "todo", wantOK: false,
		},
		{"a skipped node cancels its carrier", "skipped", "todo", "cancelled", true},
		{"a cancelled run cancels its carriers", "cancelled", "in_progress", "cancelled", true},
		{
			// The run being cancelled does not undo work someone finished
			// before it was.
			name:  "a cancelled run leaves finished work finished",
			event: "cancelled", current: "done", wantOK: false,
		},

		// The rule that matters most: a person's terminal decision stands.
		{
			name:  "a closed carrier is never reopened by activation",
			event: "activated", current: "done", wantOK: false,
		},
		{
			name:  "a closed carrier is never re-closed",
			event: "completed", current: "done", wantOK: false,
		},
		{
			name:  "a cancelled carrier is not completed by the run",
			event: "completed", current: "cancelled", wantOK: false,
		},
		{
			name:  "a cancelled carrier is not blocked by the run",
			event: "blocked", current: "cancelled", wantOK: false,
		},
		{"an unknown transition changes nothing", "reconciled", "in_progress", "", false},

		// The review round trip. Each of these was a state the board could not
		// show, which is why executors reached for the issue directly.
		{"delivery puts the carrier up for review", "in_review", "in_progress", "in_review", true},
		{
			// The node reached review without the carrier ever being started —
			// a direct-executor node whose issue was created late. Showing
			// review is still truer than showing todo.
			name:  "delivery reviews a carrier that never started",
			event: "in_review", current: "todo", want: "in_review", wantOK: true,
		},
		{
			name:  "delivery does not disturb a blocked carrier",
			event: "in_review", current: "blocked", wantOK: false,
		},
		{"a node still owing work pulls the carrier back", "waiting", "in_review", "in_progress", true},
		{
			name:  "waiting does not restart work already in progress",
			event: "waiting", current: "in_progress", wantOK: false,
		},
		{
			name:  "waiting does not start work nobody has picked up",
			event: "waiting", current: "todo", wantOK: false,
		},

		// Rework hands the same carrier to the next attempt and reopens it, so
		// superseding must leave the issue alone. Closing it here would cancel
		// the issue the replacement is about to pick up.
		{"superseding never touches the carrier", "superseded", "in_review", "", false},
		{"superseding never touches an unstarted carrier", "superseded", "todo", "", false},
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
