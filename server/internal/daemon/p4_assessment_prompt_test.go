package daemon

import (
	"strings"
	"testing"
)

func TestBuildP4AssessmentPrompt(t *testing.T) {
	out := BuildPrompt(Task{
		Kind:                  "agent_fix_p4_assessment",
		IssueID:               "issue-1",
		P4AssessmentBindingID: "binding-1",
	}, "codex")

	for _, want := range []string{
		"/api/operations/agent-fixes/binding-1/p4-evidence",
		"read-only",
		"Do not change the issue, comments, Feishu/Meego, P4, or Swarm",
		"exactly one JSON object",
		"delivery_attribution_prediction",
		"quality_prediction",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt missing %q\n---\n%s", want, out)
		}
	}
}
