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
	case "completed":
		return "done", true
	case "blocked":
		if current == "in_progress" || current == "in_review" {
			return "blocked", true
		}
	// A node the run skipped and a node the run was cancelled out from under
	// both leave work that will never be done. Leaving the carrier open would
	// keep it on someone's board as a thing to pick up.
	case "skipped", "cancelled":
		return "cancelled", true
	}
	return "", false
}
