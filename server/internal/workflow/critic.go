package workflow

import (
	"fmt"
	"strings"
)

// CriticResults are the verdicts a review may declare, identical to the set the
// verdict endpoint and the workflow_node_verdict row accept, so a decision
// never has to be translated on its way to the database.
var CriticResults = []string{"pass", "fail", "blocked"}

// maxCriticOutputInReason bounds how much of a review's closing words travel
// into a waiting reason. Long enough to hold a real short review, short enough
// that a UI can render it inline.
const maxCriticOutputInReason = 800

// DescribeUndeclaredVerdict explains a review that ended without stating a
// verdict, in terms of what the reviewer said instead.
//
// The words matter because nothing else survives. A review that finishes
// without declaring leaves no verdict row, and the node stops with no account
// of why — which is how the earlier version of this failure became
// undiagnosable after the fact: the run that hit it had nothing left to
// inspect. Whatever the reviewer wrote is the only evidence of what it did.
func DescribeUndeclaredVerdict(output string) string {
	var sb strings.Builder
	sb.WriteString(
		"The review ended without declaring a verdict. It must run " +
			"`multica workflow review --decision " +
			strings.Join(CriticResults, "|") + " --reason \"...\"`.",
	)

	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		sb.WriteString("\n\nThe reviewer produced no output.")
		return sb.String()
	}

	sb.WriteString("\n\nWhat the reviewer wrote instead:\n")
	runes := []rune(trimmed)
	if len(runes) > maxCriticOutputInReason {
		sb.WriteString(string(runes[:maxCriticOutputInReason]))
		fmt.Fprintf(&sb, "\n… (truncated, %d characters total)", len(runes))
	} else {
		sb.WriteString(trimmed)
	}
	return sb.String()
}
