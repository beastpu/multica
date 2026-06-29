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
		"`multica-agent-fix-p4-assessment` skill",
		"/api/operations/agent-fixes/binding-1/p4-evidence",
		"read-only",
		"Do not change the issue, comments, status, Feishu/Meego, P4, Swarm, or `agent_fix_review`",
		"exactly one JSON object",
		"Use `unknown` and warnings",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt missing %q\n---\n%s", want, out)
		}
	}

	for _, forbidden := range []string{
		"AI shelve CL",
		"Swarm companion CL",
		"human continuation CL",
		"delivery_attribution_prediction, quality_prediction, prediction_reasons",
	} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("prompt should leave detailed workflow to the skill, found %q\n---\n%s", forbidden, out)
		}
	}
}
