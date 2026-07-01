package lark

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	confirmationCardActionKind = "multica.chat.confirmation"
	confirmationActionConfirm  = "confirm"
	confirmationActionCancel   = "cancel"
	confirmationMessageConfirm = "确认执行"
	confirmationMessageCancel  = "取消执行"
	confirmationCardTTL        = 30 * time.Minute
)

type confirmationCardValue struct {
	Kind          string `json:"kind"`
	Action        string `json:"action"`
	Message       string `json:"message"`
	TaskID        string `json:"task_id"`
	ChatID        string `json:"chat_id"`
	ChatType      string `json:"chat_type"`
	ThreadID      string `json:"thread_id,omitempty"`
	AllowedOpenID string `json:"allowed_open_id,omitempty"`
	IssuedAtUnix  int64  `json:"issued_at"`
	ExpiresAtUnix int64  `json:"expires_at"`
}

func chatReplyNeedsConfirmationAction(content string) bool {
	trimmed := strings.TrimSpace(content)
	if !strings.Contains(trimmed, confirmationMessageConfirm) {
		return false
	}
	return strings.Contains(trimmed, "请回复") ||
		strings.Contains(trimmed, "回复“"+confirmationMessageConfirm+"”") ||
		strings.Contains(trimmed, "回复\""+confirmationMessageConfirm+"\"") ||
		hasStandaloneConfirmationLine(trimmed)
}

func hasStandaloneConfirmationLine(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == confirmationMessageConfirm {
			return true
		}
	}
	return false
}

func confirmationActionMessage(action string) (string, bool) {
	switch action {
	case confirmationActionConfirm:
		return confirmationMessageConfirm, true
	case confirmationActionCancel:
		return confirmationMessageCancel, true
	default:
		return "", false
	}
}

func renderConfirmationCard(content string, binding ChatSessionBinding, taskID, allowedOpenID string, now time.Time) (string, error) {
	issuedAt := now.Unix()
	expiresAt := now.Add(confirmationCardTTL).Unix()
	confirm := confirmationCardValue{
		Kind:          confirmationCardActionKind,
		Action:        confirmationActionConfirm,
		Message:       confirmationMessageConfirm,
		TaskID:        taskID,
		ChatID:        binding.ChannelChatID,
		ChatType:      binding.ChatType,
		AllowedOpenID: allowedOpenID,
		IssuedAtUnix:  issuedAt,
		ExpiresAtUnix: expiresAt,
	}
	cancel := confirmationCardValue{
		Kind:          confirmationCardActionKind,
		Action:        confirmationActionCancel,
		Message:       confirmationMessageCancel,
		TaskID:        taskID,
		ChatID:        binding.ChannelChatID,
		ChatType:      binding.ChatType,
		AllowedOpenID: allowedOpenID,
		IssuedAtUnix:  issuedAt,
		ExpiresAtUnix: expiresAt,
	}
	if binding.LastThreadID.Valid {
		confirm.ThreadID = binding.LastThreadID.String
		cancel.ThreadID = binding.LastThreadID.String
	}
	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "blue",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "需要确认",
			},
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": content,
				},
			},
			map[string]any{"tag": "hr"},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":   "button",
						"text":  map[string]any{"tag": "plain_text", "content": confirmationMessageConfirm},
						"type":  "primary",
						"value": confirm,
					},
					map[string]any{
						"tag":   "button",
						"text":  map[string]any{"tag": "plain_text", "content": "取消"},
						"type":  "default",
						"value": cancel,
					},
				},
			},
		},
	}
	raw, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func parseConfirmationCardValue(raw json.RawMessage) (confirmationCardValue, bool) {
	if len(raw) == 0 {
		return confirmationCardValue{}, false
	}
	var v confirmationCardValue
	if err := json.Unmarshal(raw, &v); err == nil {
		return normalizeConfirmationCardValue(v)
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil || encoded == "" {
		return confirmationCardValue{}, false
	}
	if err := json.Unmarshal([]byte(encoded), &v); err != nil {
		return confirmationCardValue{}, false
	}
	return normalizeConfirmationCardValue(v)
}

func normalizeConfirmationCardValue(v confirmationCardValue) (confirmationCardValue, bool) {
	if v.Kind != confirmationCardActionKind {
		return confirmationCardValue{}, false
	}
	message, ok := confirmationActionMessage(v.Action)
	if !ok {
		return confirmationCardValue{}, false
	}
	if v.Message == "" {
		v.Message = message
	}
	v.ChatType = strings.ToLower(v.ChatType)
	if v.Message != message ||
		v.TaskID == "" ||
		v.ChatID == "" ||
		(v.ChatType != string(ChatTypeP2P) && v.ChatType != string(ChatTypeGroup)) ||
		v.AllowedOpenID == "" ||
		v.IssuedAtUnix <= 0 ||
		v.ExpiresAtUnix <= v.IssuedAtUnix {
		return confirmationCardValue{}, false
	}
	return v, true
}

func (v confirmationCardValue) expired(now time.Time) bool {
	return v.ExpiresAtUnix <= 0 || now.Unix() > v.ExpiresAtUnix
}
