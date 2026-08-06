package workflow

// NodeIssueStatus maps a node transition onto the status its carrier
// issues should show, or reports that they should be left alone.
//
// A terminal status is never overwritten. Someone who closed or cancelled an
// issue decided something, and a run pushing its own state over that decision
// would be the platform overruling a person. Blocking only applies to work
// that had started: an issue still in todo is not blocked, it is waiting, and
// saying otherwise turns every unstarted node into an alarm.
func NodeIssueStatus(event, current string) (string, bool) {
	if current == "done" || current == "cancelled" {
		return "", false
	}
	switch event {
	case "activated":
		if current == "todo" || current == "backlog" {
			return "in_progress", true
		}
	// The work is delivered and somebody has to look at it. Without this the
	// carrier read in progress for the whole review, which is why executors
	// used to set it themselves — the one signal a reviewer scanning a board
	// needs was the one the mirror did not send.
	case "in_review":
		if current == "todo" || current == "backlog" || current == "in_progress" {
			return "in_review", true
		}
	// Review cleared but the node still owes something, so the work is back
	// with whoever is doing it. Only pulls back from review; a node that was
	// already in progress is unaffected.
	case "waiting":
		if current == "in_review" {
			return "in_progress", true
		}
	case "completed":
		return "done", true
	case "blocked":
		if current == "in_progress" || current == "in_review" {
			return "blocked", true
		}
	// A node the run skipped, a node the run was cancelled out from under, and
	// an attempt a rework replaced all leave work that will never be done.
	// Leaving those carriers open would keep them on someone's board as things
	// to pick up — the superseded case worst of all, because the attempt that
	// replaced it opens a second issue for the same work.
	case "skipped", "cancelled", "superseded":
		return "cancelled", true
	}
	return "", false
}
