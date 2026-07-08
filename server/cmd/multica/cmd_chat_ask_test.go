package main

import (
	"strings"
	"testing"
)

// TestBuildChatAskPayloadValidation: the client-side contract mirror — the
// agent must get an actionable error before any network call, and the
// confirm-without-action error must steer it toward input (the exact misuse
// the structured signal exists to prevent).
func TestBuildChatAskPayloadValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		askType string
		message string
		action  string
		options []string
		wantErr string
	}{
		{"missing message", "confirm", "  ", "a", nil, "--message is required"},
		{"missing type", "", "m", "", nil, "--type is required"},
		{"unknown type", "form", "m", "", nil, "unknown --type"},
		{"confirm without action", "confirm", "是否执行？", "", nil, "use --type input instead"},
		{"confirm with options", "confirm", "m", "a", []string{"x", "y"}, "--option is only valid with --type choice"},
		{"choice with action", "choice", "m", "a", []string{"x", "y"}, "--action is only valid with --type confirm"},
		{"choice with one option", "choice", "m", "", []string{"only"}, "2-6 --option"},
		{"choice with seven options", "choice", "m", "", []string{"1", "2", "3", "4", "5", "6", "7"}, "2-6 --option"},
		{"input with action", "input", "m", "a", nil, "neither --action nor --option"},
		{"input with options", "input", "m", "", []string{"x"}, "neither --action nor --option"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildChatAskPayload(tc.askType, tc.message, tc.action, tc.options, "")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestBuildChatAskPayloadShapes(t *testing.T) {
	t.Parallel()
	confirm, err := buildChatAskPayload("confirm", " 是否触发流水线？ ", " 触发流水线 X ", nil, "")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirm["type"] != "confirm" || confirm["message"] != "是否触发流水线？" || confirm["action"] != "触发流水线 X" {
		t.Fatalf("confirm payload = %v", confirm)
	}
	if _, ok := confirm["options"]; ok {
		t.Fatalf("confirm payload must not carry options: %v", confirm)
	}

	choice, err := buildChatAskPayload("choice", "选哪条？", "", []string{"主干", "预发"}, "")
	if err != nil {
		t.Fatalf("choice: %v", err)
	}
	if opts, ok := choice["options"].([]string); !ok || len(opts) != 2 {
		t.Fatalf("choice payload options = %v", choice["options"])
	}

	input, err := buildChatAskPayload("input", "请提供完整域名", "", nil, "example.com")
	if err != nil {
		t.Fatalf("input: %v", err)
	}
	if input["hint"] != "example.com" {
		t.Fatalf("input payload hint = %v", input["hint"])
	}
	if _, ok := input["action"]; ok {
		t.Fatalf("input payload must not carry action: %v", input)
	}
}
