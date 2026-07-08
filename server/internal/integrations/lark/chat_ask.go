package lark

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Structured chat ask delivery (docs/chat-ask-structured-signal-spec.md).
// The agent declares the interaction via `multica chat ask`; the server
// publishes EventChatAsk and this file renders it into Lark: confirm/choice
// as an interactive card whose buttons carry chatAskCardValue, input as a
// plain text question. No text heuristics anywhere on this path.

const (
	chatAskCardActionKind = "multica.chat.ask"

	chatAskChoiceApprove = "approve"
	chatAskChoiceDecline = "decline"
)

// chatAskCardValue is the button payload for ask cards. Reply is the
// normalized answer text dispatched into the chat session when clicked; the
// receipt card is rendered by the click handler from the stored ask row, so
// no content copy needs to travel in the value.
type chatAskCardValue struct {
	Kind     string `json:"kind"`
	AskID    string `json:"ask_id"`
	Choice   string `json:"choice"`
	Reply    string `json:"reply"`
	ChatType string `json:"chat_type"`
	// ThreadID keeps a per-topic session's click answer routed back into
	// the SAME topic session: inbound session keys derive from
	// chat_id + thread_id, and the card callback does not echo the thread.
	ThreadID      string `json:"thread_id,omitempty"`
	AllowedOpenID string `json:"allowed_open_id"`
	IssuedAtUnix  int64  `json:"issued_at"`
	ExpiresAtUnix int64  `json:"expires_at"`
}

func parseChatAskCardValue(raw json.RawMessage) (chatAskCardValue, bool) {
	if len(raw) == 0 {
		return chatAskCardValue{}, false
	}
	var v chatAskCardValue
	if err := json.Unmarshal(raw, &v); err != nil {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil || encoded == "" {
			return chatAskCardValue{}, false
		}
		if err := json.Unmarshal([]byte(encoded), &v); err != nil {
			return chatAskCardValue{}, false
		}
	}
	if v.Kind != chatAskCardActionKind || v.AskID == "" || v.Choice == "" ||
		v.Reply == "" || v.AllowedOpenID == "" ||
		v.IssuedAtUnix <= 0 || v.ExpiresAtUnix <= v.IssuedAtUnix {
		return chatAskCardValue{}, false
	}
	return v, true
}

func (v chatAskCardValue) expired(now time.Time) bool {
	return now.Unix() > v.ExpiresAtUnix
}

func chatAskPayloadFromEvent(payload any) (protocol.ChatAskPayload, bool) {
	switch p := payload.(type) {
	case protocol.ChatAskPayload:
		return p, p.AskID != ""
	case map[string]any:
		raw, err := json.Marshal(p)
		if err != nil {
			return protocol.ChatAskPayload{}, false
		}
		var out protocol.ChatAskPayload
		if err := json.Unmarshal(raw, &out); err != nil {
			return protocol.ChatAskPayload{}, false
		}
		return out, out.AskID != ""
	}
	return protocol.ChatAskPayload{}, false
}

func chatAskResolvedPayloadFromEvent(payload any) (protocol.ChatAskResolvedPayload, bool) {
	switch p := payload.(type) {
	case protocol.ChatAskResolvedPayload:
		return p, p.AskID != ""
	case map[string]any:
		raw, err := json.Marshal(p)
		if err != nil {
			return protocol.ChatAskResolvedPayload{}, false
		}
		var out protocol.ChatAskResolvedPayload
		if err := json.Unmarshal(raw, &out); err != nil {
			return protocol.ChatAskResolvedPayload{}, false
		}
		return out, out.AskID != ""
	}
	return protocol.ChatAskResolvedPayload{}, false
}

// chatAskReplyText normalizes the answer text a button click dispatches into
// the session (spec R4): the agent reads this as the user's message.
func chatAskReplyText(payload protocol.ChatAskPayload, choice string) string {
	switch choice {
	case chatAskChoiceApprove:
		if payload.Action != "" {
			return "确认：" + payload.Action
		}
		return "确认"
	case chatAskChoiceDecline:
		if payload.Action != "" {
			return "取消：" + payload.Action
		}
		return "取消"
	default:
		return choice
	}
}

// sendChatAsk delivers a structured ask into the session's Lark chat.
func (p *Patcher) sendChatAsk(ctx context.Context, creds InstallationCredentials, inst Installation, binding ChatSessionBinding, payload protocol.ChatAskPayload) error {
	target := threadReplyTarget(binding)
	if payload.Type == "input" {
		text := payload.Message
		if payload.Hint != "" {
			text += "\n（提示：" + payload.Hint + "）"
		}
		return sendWithThreadFallback(p.cfg.Logger, "send ask input", target, func(t ReplyTarget) error {
			_, err := p.client.SendTextMessage(ctx, SendTextParams{
				InstallationID: creds,
				ChatID:         outboundChatID(binding),
				Text:           text,
				ReplyTarget:    t,
			})
			return err
		})
	}

	taskID, err := util.ParseUUID(payload.TaskID)
	if err != nil {
		return fmt.Errorf("chat ask task id: %w", err)
	}
	allowedOpenID, ok := p.confirmationAllowedOpenID(ctx, inst.WorkspaceID, binding.InstallationID, taskID)
	if !ok {
		// Without a bound requester there is no one authorized to click;
		// fall back to the plain question so the conversation can continue
		// by text (same degradation as the legacy confirmation card).
		p.cfg.Logger.Warn("lark: chat ask fell back to text because requester binding was unavailable",
			"ask_id", payload.AskID,
			"task_id", payload.TaskID)
		return sendWithThreadFallback(p.cfg.Logger, "send ask fallback text", target, func(t ReplyTarget) error {
			_, err := p.client.SendTextMessage(ctx, SendTextParams{
				InstallationID: creds,
				ChatID:         outboundChatID(binding),
				Text:           payload.Message,
				ReplyTarget:    t,
			})
			return err
		})
	}

	threadID := ""
	if binding.LastThreadID.Valid {
		threadID = binding.LastThreadID.String
	}
	cardJSON, err := renderChatAskCard(payload, binding.ChatType, threadID, allowedOpenID, p.cfg.Now())
	if err != nil {
		return fmt.Errorf("render chat ask card: %w", err)
	}
	var messageID string
	err = sendWithThreadFallback(p.cfg.Logger, "send ask card", target, func(t ReplyTarget) error {
		id, err := p.client.SendInteractiveCard(ctx, SendCardParams{
			InstallationID: creds,
			ChatID:         outboundChatID(binding),
			CardJSON:       cardJSON,
			ReplyTarget:    t,
		})
		messageID = id
		return err
	})
	if err != nil {
		return err
	}
	if messageID != "" {
		askID, err := util.ParseUUID(payload.AskID)
		if err != nil {
			return fmt.Errorf("chat ask id: %w", err)
		}
		if err := p.queries.UpdateChatAskChannelMessage(ctx, db.UpdateChatAskChannelMessageParams{
			ID:               askID,
			ChannelMessageID: messageID,
		}); err != nil {
			p.cfg.Logger.Warn("lark: failed to persist ask card message id",
				"ask_id", payload.AskID,
				"message_id", messageID,
				"error", err)
		}
	}
	return nil
}

// renderChatAskCard renders the confirm / choice interaction card. Buttons
// carry chatAskCardValue; the click handler validates against the stored ask
// row (status, operator, expiry) before dispatching.
func renderChatAskCard(payload protocol.ChatAskPayload, chatType, threadID, allowedOpenID string, now time.Time) (string, error) {
	issuedAt := now.Unix()
	expiresAt := payload.ExpiresAtUnix
	if expiresAt <= issuedAt {
		expiresAt = now.Add(confirmationCardTTL).Unix()
	}
	value := func(choice, reply string) chatAskCardValue {
		return chatAskCardValue{
			Kind:          chatAskCardActionKind,
			AskID:         payload.AskID,
			Choice:        choice,
			Reply:         reply,
			ChatType:      chatType,
			ThreadID:      threadID,
			AllowedOpenID: allowedOpenID,
			IssuedAtUnix:  issuedAt,
			ExpiresAtUnix: expiresAt,
		}
	}

	header := "需要确认"
	content := payload.Message
	var buttons []any
	switch payload.Type {
	case "confirm":
		content += "\n**待执行**：" + payload.Action
		buttons = []any{
			map[string]any{
				"tag":   "button",
				"text":  map[string]any{"tag": "plain_text", "content": "确认"},
				"type":  "primary",
				"value": value(chatAskChoiceApprove, chatAskReplyText(payload, chatAskChoiceApprove)),
			},
			map[string]any{
				"tag":   "button",
				"text":  map[string]any{"tag": "plain_text", "content": "取消"},
				"type":  "default",
				"value": value(chatAskChoiceDecline, chatAskReplyText(payload, chatAskChoiceDecline)),
			},
		}
	case "choice":
		header = "请选择"
		for _, opt := range payload.Options {
			buttons = append(buttons, map[string]any{
				"tag":   "button",
				"text":  map[string]any{"tag": "plain_text", "content": opt},
				"type":  "default",
				"value": value(opt, opt),
			})
		}
	default:
		return "", fmt.Errorf("chat ask type %q does not render a card", payload.Type)
	}

	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "blue",
			"title": map[string]any{
				"tag":     "plain_text",
				"content": header,
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
				"tag":     "action",
				"actions": buttons,
			},
			map[string]any{
				"tag": "note",
				"elements": []any{
					map[string]any{"tag": "plain_text", "content": "选项不合适可直接回复"},
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

// RenderChatAskCardActionResponse wraps the resolved-ask receipt card in the
// card.action.trigger ACK envelope, so the clicked card updates in place.
func RenderChatAskCardActionResponse(payload protocol.ChatAskResolvedPayload) (string, error) {
	cardJSON, err := renderChatAskResolvedCard(payload)
	if err != nil {
		return "", err
	}
	return wrapCardActionResponse(cardJSON)
}

// patchChatAskResolved rewrites a delivered ask card into its receipt form
// after the ask left the pending state (answered / superseded). Best-effort:
// the click-time status check remains the correctness backstop.
func (p *Patcher) patchChatAskResolved(ctx context.Context, creds InstallationCredentials, payload protocol.ChatAskResolvedPayload) error {
	if payload.ChannelMessageID == "" {
		return nil
	}
	cardJSON, err := renderChatAskResolvedCard(payload)
	if err != nil {
		return fmt.Errorf("render resolved ask card: %w", err)
	}
	return p.client.PatchInteractiveCard(ctx, PatchCardParams{
		InstallationID:    creds,
		LarkCardMessageID: payload.ChannelMessageID,
		CardJSON:          cardJSON,
	})
}

// renderChatAskResolvedCard is the receipt card for a resolved ask: original
// question kept, buttons gone, outcome line appended.
func renderChatAskResolvedCard(payload protocol.ChatAskResolvedPayload) (string, error) {
	status := "已处理"
	template := "blue"
	result := ""
	switch payload.Status {
	case "answered":
		status = "已回复"
		template = "green"
		result = payload.AnswerText
	case "superseded":
		status = "已失效"
		result = "已被新的请求取代"
	case "expired":
		status = "已过期"
		result = "确认已过期，请重新发起"
	}
	content := payload.Message
	if content == "" {
		content = status
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
		},
	}
	if result != "" {
		card["elements"] = append(card["elements"].([]any),
			map[string]any{"tag": "hr"},
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": "**" + status + "**：" + result,
				},
			},
		)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
