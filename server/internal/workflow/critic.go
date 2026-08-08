package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// CriticOutput is the stable result contract for the built-in Critic protocol.
//
// It speaks the same words as POST /workflow-node-instances/{id}/verdicts and
// the workflow_node_verdict row: one verdict, one vocabulary. It used to say
// `approved`/`comment` instead, which cost twice. A reviewer hunting for the
// shape found the endpoint's `result`/`reason` and could not reconcile them —
// WTE-14841's Critic tried fourteen bodies and wrote its final answer in a
// blend of both. And a boolean cannot say `blocked`, so an agent Critic had no
// way to report that it could not judge at all, though the endpoint and the
// table have carried that state all along.
type CriticOutput struct {
	Result string `json:"result"`
	Reason string `json:"reason"`
}

// CriticResults are the verdicts a Critic may return, identical to the set the
// verdict endpoint accepts.
var CriticResults = []string{"pass", "fail", "blocked"}

// ParseCriticOutput accepts the exact JSON object requested by the protocol.
// A fenced object is tolerated because some providers insist on formatting
// JSON despite the instruction; prose and extra fields remain fail-closed.
func ParseCriticOutput(output string) (CriticOutput, error) {
	raw := strings.TrimSpace(output)
	if strings.HasPrefix(raw, "```") {
		firstLine := strings.IndexByte(raw, '\n')
		lastFence := strings.LastIndex(raw, "```")
		if firstLine < 0 || lastFence <= firstLine {
			return CriticOutput{}, errors.New("invalid fenced JSON verdict")
		}
		raw = strings.TrimSpace(raw[firstLine+1 : lastFence])
	}
	var wire struct {
		Result string `json:"result"`
		Reason string `json:"reason"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return CriticOutput{}, fmt.Errorf("decode critic verdict: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return CriticOutput{}, errors.New("critic verdict must contain one JSON object")
	}
	result := CriticOutput{
		Result: strings.TrimSpace(wire.Result),
		Reason: strings.TrimSpace(wire.Reason),
	}
	if !slices.Contains(CriticResults, result.Result) {
		return CriticOutput{}, fmt.Errorf(
			"critic verdict result must be one of %s", strings.Join(CriticResults, ", "),
		)
	}
	if result.Result != "pass" && result.Reason == "" {
		return CriticOutput{}, fmt.Errorf("a %s verdict requires a reason", result.Result)
	}
	return result, nil
}

// maxCriticOutputInReason bounds how much of an unreadable verdict travels
// into a waiting reason. Long enough to hold a real short review, short enough
// that a UI can render it inline.
const maxCriticOutputInReason = 800

// DescribeCriticParseFailure explains a rejected verdict in terms of what the
// Critic actually wrote.
//
// The parse error alone — "invalid character 'å' looking for beginning of
// value" — names a byte, not a problem. It leaves whoever comes to unblock the
// node unable to see the judgement that was made, and leaves nobody able to
// tell a Critic that answered in prose from one that never answered at all.
// The output is the evidence for both.
func DescribeCriticParseFailure(err error, output string) string {
	var sb strings.Builder
	sb.WriteString("Critic output did not match Workflow Critic Protocol v1: ")
	if err != nil {
		sb.WriteString(err.Error())
	}

	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		sb.WriteString("\n\nThe Critic produced no output.")
		return sb.String()
	}

	sb.WriteString("\n\nWhat the Critic wrote:\n")
	runes := []rune(trimmed)
	if len(runes) > maxCriticOutputInReason {
		sb.WriteString(string(runes[:maxCriticOutputInReason]))
		fmt.Fprintf(&sb, "\n… (truncated, %d characters total)", len(runes))
	} else {
		sb.WriteString(trimmed)
	}
	return sb.String()
}
