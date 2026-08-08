package workflow

import "testing"

// A host issue can carry several managed runs over its life, and each one
// maintains the issue's status as if it were the only one. That produced a
// loop rather than a one-off mistake:
//
//	a new run starts        → host set to in_progress
//	an older completed run  → reconciler claims it (host is not 'done')
//	                        → host set back to done
//	the new running run     → reconciler claims it (host is not 'in_progress')
//	                        → host set to in_progress … and around again
//
// Both runs are individually correct and together they flip the issue forever.
// Authority has to belong to one run: the newest one that is still live, and
// only to a finished run when nothing is live.
func TestManagedHostStatusAuthority(t *testing.T) {
	cases := []struct {
		name       string
		writer     string // the run asking to write
		liveExists bool   // is any run on this host still active?
		writerLive bool   // is the asking run itself active?
		allowed    bool
	}{
		{
			name:   "a live run owns the status",
			writer: "in_progress", liveExists: true, writerLive: true, allowed: true,
		},
		{
			// The exact loop observed: a completed run kept resetting the host
			// under a run that was still working.
			name:   "a finished run yields to a live one",
			writer: "done", liveExists: true, writerLive: false, allowed: false,
		},
		{
			name:   "a finished run may close the host when nothing is live",
			writer: "done", liveExists: false, writerLive: false, allowed: true,
		},
		{
			// The run completing IS the one that was live a moment ago; its own
			// completion must land.
			name:   "the run that just finished may still close the host",
			writer: "done", liveExists: false, writerLive: true, allowed: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ManagedHostStatusWriteAllowed(testCase.liveExists, testCase.writerLive)
			if got != testCase.allowed {
				t.Fatalf(
					"ManagedHostStatusWriteAllowed(live=%v, writerLive=%v) = %v, want %v",
					testCase.liveExists, testCase.writerLive, got, testCase.allowed,
				)
			}
		})
	}
}
