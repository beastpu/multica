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
	// maxConfirmationCardContentRunes caps the prompt copy embedded in each
	// button value (see confirmationCardValue.Content). Both buttons carry
	// it, so the cap keeps the card comfortably under Lark's size limits.
	maxConfirmationCardContentRunes = 600
)

type confirmationCardValue struct {
	Kind    string `json:"kind"`
	Action  string `json:"action"`
	Message string `json:"message"`
	// Content is a truncated copy of the card's prompt text. The
	// card.action.trigger callback does not echo the card body, so this is
	// the only way the resolved-card ACK can keep the original question
	// visible after the buttons are removed. Optional: cards sent before
	// this field existed resolve to a result-only card.
	Content       string `json:"content,omitempty"`
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
	_, ok := chatConfirmationReplyMessage(content)
	return ok
}

// chatConfirmationReplyMessage detects a confirmation prompt in a chat reply
// using the EXPLICIT tiers only (quoted 回复“确认X” / standalone 确认X line).
// The soft prompt-cue tier was retired from the chat path: agents on
// ask-capable channels declare confirmations via `multica chat ask`
// (docs/chat-ask-structured-signal-spec.md), and the cue tier's misfires on
// information requests ("请提供…确认后…") produced dead buttons.
func chatConfirmationReplyMessage(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || !strings.Contains(trimmed, "确认") {
		return "", false
	}
	if message, ok := confirmationReplyFromQuotedReply(trimmed); ok {
		return message, true
	}
	return standaloneConfirmationReply(trimmed)
}

// confirmationReplyMessage is the full three-tier detection (explicit tiers
// plus the guarded prompt-cue tier). Still used by the issue inbox
// confirmation flow, which has no structured-ask replacement yet.
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

// informationRequestCues mark replies that need the user to SUPPLY something
// (a full domain name, an ID, a file, …). A confirm button cannot answer such
// a prompt — the task needs free-form input — so the soft prompt-cue tier
// below must not render a confirmation card when one of these is present.
// The explicit tiers (quoted 回复“确认X” and a standalone 确认X line) still
// win over this guard: there the agent literally instructed a 确认 reply.
var informationRequestCues = []string{
	"请提供", "请补充", "请输入", "请告知", "请给出", "请上传", "请指定", "请附上",
}

func containsInformationRequestCue(content string) bool {
	for _, cue := range informationRequestCues {
		if strings.Contains(content, cue) {
			return true
		}
	}
	return false
}

func confirmationReplyFromPromptCue(content string) (string, bool) {
	if containsInformationRequestCue(content) {
		return "", false
	}
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

// renderConfirmationCard builds the chat confirmation prompt: the agent's
// question plus a confirm/cancel pair whose payload rides back through
// decodeChatConfirmationCardAction.
//
// It is schema 2.0 for both of its uses — sent on its own, and patched over a
// live progress card. One schema everywhere means no update ever turns a card
// into a different schema, which is an assumption about Lark we would rather
// not make. Buttons therefore carry their payload in `behaviors` callbacks
// rather than the schema-1.0 top-level `value`; the trigger event normalizes
// both into action.value, so the decode path is unaffected.
func renderConfirmationCard(content string, binding ChatSessionBinding, taskID, allowedOpenID string, now time.Time) (string, error) {
	content = truncateUTF8Bytes(content, 16*1024)
	confirmMessage, ok := chatConfirmationReplyMessage(content)
	if !ok {
		confirmMessage = confirmationMessageConfirm
	}
	cancelMessage := confirmationCancelMessage(confirmMessage)
	issuedAt := now.Unix()
	expiresAt := now.Add(confirmationCardTTL).Unix()
	// The value rides back through decodeChatConfirmationCardAction and
	// re-enters the inbound pipeline, so ChatID must be the REAL chat id
	// (a composite topic binding key is not a valid Lark chat id); together
	// with ThreadID it re-derives the same per-topic session key.
	chatID := string(outboundChatID(binding))
	embeddedContent := truncateConfirmationCardContent(content)
	value := func(action, message string) confirmationCardValue {
		v := confirmationCardValue{
			Kind: confirmationCardActionKind, Action: action, Message: message,
			Content: embeddedContent, TaskID: taskID, ChatID: chatID,
			ChatType: binding.ChatType, AllowedOpenID: allowedOpenID,
			IssuedAtUnix: issuedAt, ExpiresAtUnix: expiresAt,
		}
		if binding.LastThreadID.Valid {
			v.ThreadID = binding.LastThreadID.String
		}
		return v
	}
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			// Shared card: the receipt that replaces this one after a click has
			// to reach everyone who can see it, not just the clicker.
			"update_multi": true,
			"summary":      map[string]any{"content": "需要确认"},
		},
		"header": map[string]any{
			"template": "blue",
			"title":    map[string]any{"tag": "plain_text", "content": "需要确认"},
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{"tag": "markdown", "element_id": "confirmation_content", "content": content},
				map[string]any{
					"tag": "column_set", "element_id": "confirmation_actions",
					"flex_mode": "stretch", "horizontal_spacing": "8px",
					"columns": []any{
						map[string]any{"tag": "column", "width": "weighted", "weight": 1, "elements": []any{
							map[string]any{"tag": "button", "element_id": "confirmation_confirm", "text": map[string]any{"tag": "plain_text", "content": confirmMessage}, "type": "primary", "width": "fill", "behaviors": []any{
								map[string]any{"type": "callback", "value": value(confirmationActionConfirm, confirmMessage)},
							}},
						}},
						map[string]any{"tag": "column", "width": "weighted", "weight": 1, "elements": []any{
							map[string]any{"tag": "button", "element_id": "confirmation_cancel", "text": map[string]any{"tag": "plain_text", "content": cancelMessage}, "type": "default", "width": "fill", "behaviors": []any{
								map[string]any{"type": "callback", "value": value(confirmationActionCancel, cancelMessage)},
							}},
						}},
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

func truncateConfirmationCardContent(content string) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= maxConfirmationCardContentRunes {
		return string(runes)
	}
	return string(runes[:maxConfirmationCardContentRunes]) + "…"
}

// confirmationOutcome is what a receipt has to say once a confirmation card
// has been clicked: which way it went, and the words the clicked button
// carried. Shared by both flows; only the card body around it differs.
type confirmationOutcome struct {
	Status   string
	Template string
	Result   string
	Content  string
}

func confirmationOutcomeOf(content, action, rawMessage string) confirmationOutcome {
	out := confirmationOutcome{Status: "已处理", Template: "blue"}
	switch action {
	case confirmationActionConfirm:
		out.Status, out.Template = "已确认", "green"
	case confirmationActionCancel:
		out.Status = "已取消"
	}
	message := strings.TrimSpace(rawMessage)
	if message == "" {
		if fallback, ok := confirmationActionMessage(action, ""); ok {
			message = fallback
		}
	}
	out.Result = out.Status
	if message != "" {
		out.Result += "：" + message
	}
	out.Content = strings.TrimSpace(content)
	if out.Content == "" {
		out.Content = out.Result
	}
	return out
}

// RenderIssueConfirmationResolvedCard replaces an issue inbox confirmation
// card after the user clicks one of its buttons. The original agent prompt is
// kept visible, but the interactive controls are removed so the card no longer
// invites a second action.
//
// Schema 1.0, matching the issue inbox confirmation card it replaces. A receipt
// and the card it overwrites must agree on schema.
func RenderIssueConfirmationResolvedCard(content string, action IssueConfirmationCardAction) (string, error) {
	out := confirmationOutcomeOf(content, action.Action, action.Message)
	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"template": out.Template,
			"title": map[string]any{
				"tag":     "plain_text",
				"content": out.Status,
			},
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": out.Content,
				},
			},
			map[string]any{"tag": "hr"},
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": "**" + out.Result + "**",
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

// renderChatConfirmationResolvedCard is the same receipt for the chat flow,
// in schema 2.0 to match renderConfirmationCard. Prompt kept, buttons gone,
// outcome appended.
func renderChatConfirmationResolvedCard(content, action, rawMessage string) (string, error) {
	out := confirmationOutcomeOf(content, action, rawMessage)
	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
			"summary":          map[string]any{"content": out.Status},
		},
		"header": map[string]any{
			"template": out.Template,
			"title":    map[string]any{"tag": "plain_text", "content": out.Status},
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{"tag": "markdown", "element_id": "confirmation_content", "content": out.Content},
				map[string]any{"tag": "hr", "element_id": "confirmation_rule"},
				map[string]any{"tag": "markdown", "element_id": "confirmation_result", "content": "**" + out.Result + "**"},
			},
		},
	}
	raw, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// RenderIssueConfirmationCardActionResponse wraps the resolved card in the
// card.action.trigger response envelope Lark expects on the callback ACK path.
func RenderIssueConfirmationCardActionResponse(content string, action IssueConfirmationCardAction) (string, error) {
	cardJSON, err := RenderIssueConfirmationResolvedCard(content, action)
	if err != nil {
		return "", err
	}
	return wrapCardActionResponse(cardJSON)
}

// renderChatConfirmationCardActionResponse builds the card.action.trigger
// response for a chat confirmation click from the button value alone (the
// callback carries no card body — Content is the embedded prompt copy).
func renderChatConfirmationCardActionResponse(value confirmationCardValue) (string, error) {
	cardJSON, err := renderChatConfirmationResolvedCard(value.Content, value.Action, value.Message)
	if err != nil {
		return "", err
	}
	return wrapCardActionResponse(cardJSON)
}

func wrapCardActionResponse(cardJSON string) (string, error) {
	var card json.RawMessage
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		return "", err
	}
	resp := map[string]any{
		"card": map[string]any{
			"type": "raw",
			"data": card,
		},
	}
	raw, err := json.Marshal(resp)
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
