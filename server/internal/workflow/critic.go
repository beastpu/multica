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
