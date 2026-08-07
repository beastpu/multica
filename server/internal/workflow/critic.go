package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// CriticOutput is the stable result contract for the built-in Critic protocol.
type CriticOutput struct {
	Approved bool   `json:"approved"`
	Comment  string `json:"comment"`
}

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
		Approved *bool  `json:"approved"`
		Comment  string `json:"comment"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return CriticOutput{}, fmt.Errorf("decode critic verdict: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return CriticOutput{}, errors.New("critic verdict must contain one JSON object")
	}
	if wire.Approved == nil {
		return CriticOutput{}, errors.New("critic verdict requires approved")
	}
	result := CriticOutput{
		Approved: *wire.Approved,
		Comment:  strings.TrimSpace(wire.Comment),
	}
	if !result.Approved && result.Comment == "" {
		return CriticOutput{}, errors.New("a rejected verdict requires a comment")
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
