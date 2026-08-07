package workflow

import (
	"errors"
	"strings"
	"testing"
)

func TestParseCriticOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		approved bool
		comment  string
		wantErr  bool
	}{
		{name: "approve", output: `{"approved":true,"comment":"meets AC"}`, approved: true, comment: "meets AC"},
		{name: "reject", output: "```json\n{\"approved\":false,\"comment\":\"add the missing test\"}\n```", comment: "add the missing test"},
		{name: "reject needs reason", output: `{"approved":false,"comment":""}`, wantErr: true},
		{name: "approved is required", output: `{"comment":"looks fine"}`, wantErr: true},
		{name: "unknown field", output: `{"approved":true,"comment":"ok","score":1}`, wantErr: true},
		{name: "prose is refused", output: `Approved: {"approved":true,"comment":"ok"}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseCriticOutput(test.output)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Approved != test.approved || got.Comment != test.comment {
				t.Fatalf("unexpected verdict: %+v", got)
			}
		})
	}
}

// A verdict the platform cannot read stops the node, which is right. What was
// wrong is that the unreadable text was then thrown away: the node blocked
// with "invalid character 'å' looking for beginning of value" and nothing
// else, so whoever came to unblock it could not see what the Critic had
// actually said — nor could anyone diagnose why the protocol was missed.
//
// The reviewer's words are the evidence. Keep them.
func TestDescribeCriticParseFailureKeepsTheOutput(t *testing.T) {
	output := "经过审查，我认为这次修复是充分的，可以通过。"

	_, err := ParseCriticOutput(output)
	if err == nil {
		t.Fatal("expected prose to be refused")
	}

	described := DescribeCriticParseFailure(err, output)
	if !strings.Contains(described, output) {
		t.Errorf("description dropped the critic's own words:\n%s", described)
	}
	if !strings.Contains(described, "Workflow Critic Protocol v1") {
		t.Errorf("description does not name the protocol:\n%s", described)
	}
}

// An output long enough to be a whole review must not be pasted wholesale into
// a waiting reason that a UI renders inline.
func TestDescribeCriticParseFailureTruncates(t *testing.T) {
	long := strings.Repeat("x", 4000)
	described := DescribeCriticParseFailure(errors.New("boom"), long)
	if len(described) > 1200 {
		t.Errorf("description is %d chars, want a bounded summary", len(described))
	}
	if !strings.Contains(described, "truncated") {
		t.Errorf("truncation is not disclosed:\n%s", described[:200])
	}
}
