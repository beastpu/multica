package lark

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	confirmationCardActionKind      = "multica.chat.confirmation"
	issueConfirmationCardActionKind = "multica.issue.confirmation"
	confirmationActionConfirm       = "confirm"
	confirmationActionCancel        = "cancel"
	confirmationMessageConfirm      = "确认执行"
	confirmationMessageCancel       = "取消执行"
	confirmationCardTTL             = 30 * time.Minute
	maxConfirmationMessageRunes     = 12
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

type issueConfirmationCardValue struct {
	Kind            string `json:"kind"`
	Action          string `json:"action"`
	Message         string `json:"message"`
	WorkspaceID     string `json:"workspace_id"`
	IssueID         string `json:"issue_id"`
	ParentCommentID string `json:"parent_comment_id"`
	RecipientID     string `json:"recipient_id"`
	AllowedOpenID   string `json:"allowed_open_id"`
	IssuedAtUnix    int64  `json:"issued_at"`
	ExpiresAtUnix   int64  `json:"expires_at"`
}

func chatReplyNeedsConfirmationAction(content string) bool {
	_, ok := confirmationReplyMessage(content)
	return ok
}

func confirmationReplyMessage(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || !strings.Contains(trimmed, "确认") {
		return "", false
	}
	if message, ok := confirmationReplyFromQuotedReply(trimmed); ok {
		return message, true
	}
	if message, ok := standaloneConfirmationReply(trimmed); ok {
		return message, true
	}
	return confirmationReplyFromPromptCue(trimmed)
}

func confirmationReplyFromQuotedReply(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, "回复") {
			continue
		}
		for _, pair := range [][2]string{
			{"“", "”"},
			{"\"", "\""},
			{"「", "」"},
			{"『", "』"},
			{"`", "`"},
			{"'", "'"},
		} {
			rest := line
			for {
				start := strings.Index(rest, pair[0])
				if start < 0 {
					break
				}
				afterStart := rest[start+len(pair[0]):]
				end := strings.Index(afterStart, pair[1])
				if end < 0 {
					break
				}
				if message, ok := normalizeConfirmationReplyMessage(afterStart[:end]); ok {
					return message, true
				}
				rest = afterStart[end+len(pair[1]):]
			}
		}
		if message, ok := confirmationReplyAfterMarker(line, "请回复"); ok {
			return message, true
		}
		if message, ok := confirmationReplyAfterMarker(line, "回复"); ok {
			return message, true
		}
	}
	return "", false
}

func confirmationReplyAfterMarker(line, marker string) (string, bool) {
	idx := strings.Index(line, marker)
	if idx < 0 {
		return "", false
	}
	after := strings.TrimLeft(line[idx+len(marker):], " \t\r\n:：,，")
	return scanConfirmationToken(after)
}

func standaloneConfirmationReply(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		if message, ok := normalizeConfirmationReplyMessage(trimStandaloneConfirmationLine(line)); ok {
			return message, true
		}
	}
	return "", false
}

func trimStandaloneConfirmationLine(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"- ", "* ", "• "} {
		line = strings.TrimSpace(strings.TrimPrefix(line, prefix))
	}
	return line
}

func confirmationReplyFromPromptCue(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(line, "确认") {
			continue
		}
		for offset := 0; offset < len(line); {
			idx := strings.Index(line[offset:], "确认")
			if idx < 0 {
				break
			}
			idx += offset
			candidate, ok := scanConfirmationToken(line[idx:])
			if !ok {
				offset = idx + len("确认")
				continue
			}
			if strings.Contains(line, candidate+"后") ||
				strings.Contains(line, "是否"+candidate) ||
				strings.Contains(line, "请"+candidate) {
				return candidate, true
			}
			offset = idx + len("确认")
		}
	}
	return "", false
}

func scanConfirmationToken(s string) (string, bool) {
	s = strings.TrimLeft(s, " \t\r\n`'\"“”‘’「」『』【】[]()（）")
	if !strings.HasPrefix(s, "确认") {
		return "", false
	}
	var b strings.Builder
	for _, r := range s {
		if confirmationTokenStopRune(r) {
			break
		}
		b.WriteRune(r)
	}
	return normalizeConfirmationReplyMessage(b.String())
}

func confirmationTokenStopRune(r rune) bool {
	switch r {
	case ' ', '\t', '\r', '\n', '`', '\'', '"', '“', '”', '‘', '’', '「', '」', '『', '』', '【', '】', '[', ']', '(', ')', '（', '）',
		',', '，', '.', '。', '!', '！', '?', '？', ':', '：', ';', '；', '/', '\\', '|', '后', '再', '吗':
		return true
	default:
		return false
	}
}

func normalizeConfirmationReplyMessage(raw string) (string, bool) {
	s := trimConfirmationReplyToken(raw)
	if s == "确认" {
		return s, true
	}
	if !strings.HasPrefix(s, "确认") {
		return "", false
	}
	if len([]rune(s)) > maxConfirmationMessageRunes {
		return "", false
	}
	rest := strings.TrimPrefix(s, "确认")
	if rest == "" || invalidConfirmationObject(rest) || strings.ContainsAny(rest, " \t\r\n,，.。!！?？:：;；/\\|") {
		return "", false
	}
	return s, true
}

func trimConfirmationReplyToken(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, " \t\r\n`'\"“”‘’「」『』【】[]()（）")
	s = strings.TrimRight(s, " \t\r\n。.!！?？,，;；:：")
	s = strings.TrimSuffix(s, "吗")
	return strings.TrimSpace(s)
}

func invalidConfirmationObject(rest string) bool {
	switch rest {
	case "下一步", "一下", "是否", "信息", "内容", "问题", "后续", "结果":
		return true
	default:
		return false
	}
}

func messageNeedsConfirmationAction(content string) bool {
	return chatReplyNeedsConfirmationAction(content)
}

func confirmationCancelMessage(confirmMessage string) string {
	confirmMessage, ok := normalizeConfirmationReplyMessage(confirmMessage)
	if !ok {
		return confirmationMessageCancel
	}
	if confirmMessage == "确认" {
		return "取消"
	}
	return "取消" + strings.TrimPrefix(confirmMessage, "确认")
}

func normalizeCancellationReplyMessage(raw string) (string, bool) {
	s := trimConfirmationReplyToken(raw)
	if s == "取消" {
		return s, true
	}
	if !strings.HasPrefix(s, "取消") || len([]rune(s)) > maxConfirmationMessageRunes {
		return "", false
	}
	rest := strings.TrimPrefix(s, "取消")
	if rest == "" || strings.ContainsAny(rest, " \t\r\n,，.。!！?？:：;；/\\|") {
		return "", false
	}
	return s, true
}

func confirmationActionMessage(action, message string) (string, bool) {
	switch action {
	case confirmationActionConfirm:
		if message == "" {
			return confirmationMessageConfirm, true
		}
		return normalizeConfirmationReplyMessage(message)
	case confirmationActionCancel:
		if message == "" {
			return confirmationMessageCancel, true
		}
		return normalizeCancellationReplyMessage(message)
	default:
		return "", false
	}
}

func renderConfirmationCard(content string, binding ChatSessionBinding, taskID, allowedOpenID string, now time.Time) (string, error) {
	confirmMessage, ok := confirmationReplyMessage(content)
	if !ok {
		confirmMessage = confirmationMessageConfirm
	}
	cancelMessage := confirmationCancelMessage(confirmMessage)
	issuedAt := now.Unix()
	expiresAt := now.Add(confirmationCardTTL).Unix()
	confirm := confirmationCardValue{
		Kind:          confirmationCardActionKind,
		Action:        confirmationActionConfirm,
		Message:       confirmMessage,
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
		Message:       cancelMessage,
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
						"text":  map[string]any{"tag": "plain_text", "content": confirmMessage},
						"type":  "primary",
						"value": confirm,
					},
					map[string]any{
						"tag":   "button",
						"text":  map[string]any{"tag": "plain_text", "content": cancelMessage},
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

// RenderIssueConfirmationResolvedCard replaces an issue inbox confirmation
// card after the user clicks one of its buttons. The original agent prompt is
// kept visible, but the interactive controls are removed so the card no longer
// invites a second action.
func RenderIssueConfirmationResolvedCard(content string, action IssueConfirmationCardAction) (string, error) {
	status := "已处理"
	template := "blue"
	switch action.Action {
	case confirmationActionConfirm:
		status = "已确认"
		template = "green"
	case confirmationActionCancel:
		status = "已取消"
	}
	message := strings.TrimSpace(action.Message)
	if message == "" {
		if fallback, ok := confirmationActionMessage(action.Action, ""); ok {
			message = fallback
		}
	}
	result := status
	if message != "" {
		result += "：" + message
	}
	content = strings.TrimSpace(content)
	if content == "" {
		content = result
	}
	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"template": template,
			"title": map[string]any{
				"tag":     "plain_text",
				"content": status,
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
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": "**" + result + "**",
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

func parseIssueConfirmationCardValue(raw json.RawMessage) (issueConfirmationCardValue, bool) {
	if len(raw) == 0 {
		return issueConfirmationCardValue{}, false
	}
	var v issueConfirmationCardValue
	if err := json.Unmarshal(raw, &v); err == nil {
		return normalizeIssueConfirmationCardValue(v)
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil || encoded == "" {
		return issueConfirmationCardValue{}, false
	}
	if err := json.Unmarshal([]byte(encoded), &v); err != nil {
		return issueConfirmationCardValue{}, false
	}
	return normalizeIssueConfirmationCardValue(v)
}

func normalizeConfirmationCardValue(v confirmationCardValue) (confirmationCardValue, bool) {
	if v.Kind != confirmationCardActionKind {
		return confirmationCardValue{}, false
	}
	message, ok := confirmationActionMessage(v.Action, v.Message)
	if !ok {
		return confirmationCardValue{}, false
	}
	v.Message = message
	v.ChatType = strings.ToLower(v.ChatType)
	if v.TaskID == "" ||
		v.ChatID == "" ||
		(v.ChatType != string(ChatTypeP2P) && v.ChatType != string(ChatTypeGroup)) ||
		v.AllowedOpenID == "" ||
		v.IssuedAtUnix <= 0 ||
		v.ExpiresAtUnix <= v.IssuedAtUnix {
		return confirmationCardValue{}, false
	}
	return v, true
}

func normalizeIssueConfirmationCardValue(v issueConfirmationCardValue) (issueConfirmationCardValue, bool) {
	if v.Kind != issueConfirmationCardActionKind {
		return issueConfirmationCardValue{}, false
	}
	message, ok := confirmationActionMessage(v.Action, v.Message)
	if !ok {
		return issueConfirmationCardValue{}, false
	}
	v.Message = message
	v.WorkspaceID = strings.TrimSpace(v.WorkspaceID)
	v.IssueID = strings.TrimSpace(v.IssueID)
	v.ParentCommentID = strings.TrimSpace(v.ParentCommentID)
	v.RecipientID = strings.TrimSpace(v.RecipientID)
	v.AllowedOpenID = strings.TrimSpace(v.AllowedOpenID)
	if v.WorkspaceID == "" ||
		v.IssueID == "" ||
		v.ParentCommentID == "" ||
		v.RecipientID == "" ||
		v.AllowedOpenID == "" ||
		v.IssuedAtUnix <= 0 ||
		v.ExpiresAtUnix <= v.IssuedAtUnix {
		return issueConfirmationCardValue{}, false
	}
	return v, true
}

func (v confirmationCardValue) expired(now time.Time) bool {
	return v.ExpiresAtUnix <= 0 || now.Unix() > v.ExpiresAtUnix
}

func (v issueConfirmationCardValue) expired(now time.Time) bool {
	return v.ExpiresAtUnix <= 0 || now.Unix() > v.ExpiresAtUnix
}

func (v issueConfirmationCardValue) dedupMessageID(operatorOpenID string) string {
	return "card_action:" + issueConfirmationCardActionKind + ":" + v.ParentCommentID + ":" + operatorOpenID
}

func (v issueConfirmationCardValue) toAction() IssueConfirmationCardAction {
	return IssueConfirmationCardAction{
		Action:          v.Action,
		Message:         v.Message,
		WorkspaceID:     v.WorkspaceID,
		IssueID:         v.IssueID,
		ParentCommentID: v.ParentCommentID,
		RecipientID:     v.RecipientID,
		AllowedOpenID:   v.AllowedOpenID,
		IssuedAtUnix:    v.IssuedAtUnix,
		ExpiresAtUnix:   v.ExpiresAtUnix,
	}
}
