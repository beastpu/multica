package lark

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestRenderConfirmationCardEmbedsRealChatID pins the card round-trip for
// per-topic sessions: the value the button carries back through
// decodeChatConfirmationCardAction must hold the REAL chat id (a composite
// binding key is not a valid Lark chat id, and the recomputed session key
// must land back in the same per-topic session via chat_id + thread_id).
func TestRenderConfirmationCardEmbedsRealChatID(t *testing.T) {
	t.Parallel()
	binding := ChatSessionBinding{
		ChannelChatID: "oc_real:omt_topic1",
		Config:        []byte(`{"chat_id":"oc_real"}`),
		ChatType:      "group",
		LastThreadID:  pgtype.Text{String: "omt_topic1", Valid: true},
	}
	cardJSON, err := renderConfirmationCard("是否确认？【确认执行】", binding, "task-1", "ou_user", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(cardJSON, `\"chat_id\":\"oc_real\"`) && !strings.Contains(cardJSON, `"chat_id":"oc_real"`) {
		t.Fatalf("card value must embed the real chat id, got: %s", cardJSON)
	}
	if strings.Contains(cardJSON, "oc_real:omt_topic1") {
		t.Fatalf("card value must not leak the composite binding key: %s", cardJSON)
	}
	if !strings.Contains(cardJSON, "omt_topic1") {
		t.Fatalf("card value must keep the thread id for session re-routing: %s", cardJSON)
	}
}

// TestRenderConfirmationCardUsesCallbackBehaviors pins the schema-2.0 button
// shape. A 2.0 card rejects the schema-1.0 top-level `value`, and the card is
// 2.0 in both of its uses so that patching a live progress card into a
// confirmation prompt never changes schema mid-flight.
func TestRenderConfirmationCardUsesCallbackBehaviors(t *testing.T) {
	t.Parallel()
	cardJSON, err := renderConfirmationCard(
		"是否确认？【确认执行】",
		ChatSessionBinding{ChannelChatID: "oc_real", ChatType: "p2p"},
		"task-1", "ou_user", time.Unix(1700000000, 0),
	)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var doc any
	if err := json.Unmarshal([]byte(cardJSON), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var buttons []map[string]any
	collectCardElementsByTag(doc, "button", &buttons)
	if len(buttons) != 2 {
		t.Fatalf("buttons=%d want 2: %s", len(buttons), cardJSON)
	}
	actions := map[string]bool{}
	for _, button := range buttons {
		if _, legacy := button["value"]; legacy {
			t.Fatalf("schema 2.0 button must not use the legacy top-level value: %v", button)
		}
		behaviors, _ := button["behaviors"].([]any)
		if len(behaviors) != 1 {
			t.Fatalf("button callback behaviors=%v", button["behaviors"])
		}
		behavior, _ := behaviors[0].(map[string]any)
		value, _ := behavior["value"].(map[string]any)
		action, _ := value["action"].(string)
		if behavior["type"] != "callback" || action == "" {
			t.Fatalf("invalid callback behavior=%v", behavior)
		}
		actions[action] = true
	}
	if !actions[confirmationActionConfirm] || !actions[confirmationActionCancel] {
		t.Fatalf("callback actions=%v", actions)
	}
}

func TestRenderIssueConfirmationResolvedCardRemovesActions(t *testing.T) {
	t.Parallel()
	cardJSON, err := RenderIssueConfirmationResolvedCard("是否确认发布？", IssueConfirmationCardAction{
		Action:  confirmationActionConfirm,
		Message: "确认发布",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, cardJSON)
	}
	if cfg, _ := doc["config"].(map[string]any); cfg == nil || cfg["update_multi"] != true {
		t.Fatalf("resolved card must stay patchable with update_multi=true: %v", doc["config"])
	}
	for _, want := range []string{"是否确认发布？", "已确认", "确认发布"} {
		if !strings.Contains(cardJSON, want) {
			t.Fatalf("resolved card missing %q: %s", want, cardJSON)
		}
	}
	if containsCardTag(doc, "action") || strings.Contains(cardJSON, `"tag":"button"`) {
		t.Fatalf("resolved card must not keep actionable buttons: %s", cardJSON)
	}
}

func TestRenderIssueConfirmationResolvedCardCancelResult(t *testing.T) {
	t.Parallel()
	cardJSON, err := RenderIssueConfirmationResolvedCard("是否确认提及？", IssueConfirmationCardAction{
		Action:  confirmationActionCancel,
		Message: "取消提及",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"是否确认提及？", "已取消", "取消提及"} {
		if !strings.Contains(cardJSON, want) {
			t.Fatalf("resolved cancel card missing %q: %s", want, cardJSON)
		}
	}
}

func TestRenderIssueConfirmationCardActionResponseUsesRawCard(t *testing.T) {
	t.Parallel()
	respJSON, err := RenderIssueConfirmationCardActionResponse("是否确认发布？", IssueConfirmationCardAction{
		Action:  confirmationActionConfirm,
		Message: "确认发布",
	})
	if err != nil {
		t.Fatalf("render response: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, respJSON)
	}
	card, _ := resp["card"].(map[string]any)
	if card == nil || card["type"] != "raw" {
		t.Fatalf("callback response must use raw card payload: %v", resp)
	}
	data, _ := card["data"].(map[string]any)
	if data == nil {
		t.Fatalf("callback response card.data missing: %v", resp)
	}
	if containsCardTag(data, "action") || strings.Contains(respJSON, `"tag":"button"`) {
		t.Fatalf("callback response must replace buttons with receipt card: %s", respJSON)
	}
	for _, want := range []string{"是否确认发布？", "已确认", "确认发布"} {
		if !strings.Contains(respJSON, want) {
			t.Fatalf("callback response missing %q: %s", want, respJSON)
		}
	}
}

// TestChatReplyInformationRequestSkipsConfirmationCard: a chat reply that
// needs the user to SUPPLY something (here a full domain name) must not
// render confirm/cancel buttons — the chat path retired the soft prompt-cue
// tier entirely, so "请确认"/"确认后" phrasing alone never produces buttons.
func TestChatReplyInformationRequestSkipsConfirmationCard(t *testing.T) {
	t.Parallel()
	contents := []string{
		"| 项目 | 内容 |\n|---|---|\n| 状态 | 待确认 |\n| 原因 | 当前输入不是完整域名，无法确定要查询的 FQDN |\n| 请确认 | 请提供完整域名，例如 `example.com` 或 `sub.example.com`，确认后我再查询 DNS 解析记录 |",
		"请提供完整域名，确认后我再查询 DNS 解析记录。",
		"请补充变更单号，请确认是否继续。",
		// Soft cue with no information request: cue tier is gone from chat,
		// so this renders as plain text too (agents on ask-capable channels
		// declare confirmations via `multica chat ask` instead).
		"是否确认继续？请确认后我再执行。",
	}
	for _, content := range contents {
		if chatReplyNeedsConfirmationAction(content) {
			t.Errorf("content must not trigger a chat confirmation card: %q", content)
		}
	}
}

// TestChatReplyExplicitConfirmationWinsOverInformationRequest keeps the
// explicit tiers intact: when the agent literally instructs a 确认X reply
// (quoted or standalone), the buttons stay even if the message also asks for
// extra input somewhere else.
func TestChatReplyExplicitConfirmationWinsOverInformationRequest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		content string
		want    string
	}{
		{"请回复“确认执行”，我再触发。\n如需改动请提供变更单号。", "确认执行"},
		{"项目：`测试`\n如有疑问请提供截图。\n\n确认发布", "确认发布"},
	} {
		got, ok := chatConfirmationReplyMessage(tc.content)
		if !ok || got != tc.want {
			t.Errorf("chatConfirmationReplyMessage(%q) = %q, %v; want %q, true", tc.content, got, ok, tc.want)
		}
	}
}

// TestInboxConfirmationDetectionKeepsPromptCueTier pins the issue inbox
// flow's behavior: it still uses the full three-tier detection (the inbox
// confirmation cards have no structured-ask replacement yet), including the
// guarded prompt-cue tier — retiring the cue tier from the CHAT path must
// not change inbox detection.
func TestInboxConfirmationDetectionKeepsPromptCueTier(t *testing.T) {
	t.Parallel()
	if got, ok := confirmationReplyMessage("是否确认发布？请确认后我再执行。"); !ok || got != "确认发布" {
		t.Fatalf("inbox detection lost the prompt-cue tier: %q, %v", got, ok)
	}
	if _, ok := confirmationReplyMessage("请提供完整域名，确认后我再查询。"); ok {
		t.Fatal("inbox prompt-cue tier must keep the information-request guard")
	}
}

// TestRenderConfirmationCardEmbedsContentInButtonValue: the resolved-card
// ACK can only echo the original prompt if the button value carries it, so
// renderConfirmationCard must embed a (truncated) copy in both actions.
func TestRenderConfirmationCardEmbedsContentInButtonValue(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("域", maxConfirmationCardContentRunes+50)
	cardJSON, err := renderConfirmationCard(long, ChatSessionBinding{ChannelChatID: "oc_1", ChatType: "p2p"}, "task-1", "ou_req", time.Unix(1720000000, 0))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var doc any
	if err := json.Unmarshal([]byte(cardJSON), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var buttons []map[string]any
	collectCardElementsByTag(doc, "button", &buttons)
	checked := 0
	for _, button := range buttons {
		behaviors, _ := button["behaviors"].([]any)
		for _, raw := range behaviors {
			behavior, _ := raw.(map[string]any)
			encoded, err := json.Marshal(behavior["value"])
			if err != nil {
				t.Fatalf("marshal behavior value: %v", err)
			}
			var value confirmationCardValue
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatalf("decode behavior value: %v", err)
			}
			checked++
			runes := []rune(value.Content)
			if len(runes) != maxConfirmationCardContentRunes+1 || runes[len(runes)-1] != '…' {
				t.Fatalf("button value content not truncated to %d runes + ellipsis: len=%d", maxConfirmationCardContentRunes, len(runes))
			}
		}
	}
	if checked != 2 {
		t.Fatalf("expected 2 buttons carrying content, got %d", checked)
	}
}

// TestRenderChatConfirmationCardActionResponse: clicking a chat confirmation
// button must yield a card.action.trigger response that replaces the card —
// original prompt kept, buttons gone, outcome line visible.
func TestRenderChatConfirmationCardActionResponse(t *testing.T) {
	t.Parallel()
	respJSON, err := renderChatConfirmationCardActionResponse(confirmationCardValue{
		Action:  confirmationActionConfirm,
		Message: "确认执行",
		Content: "是否触发流水线？",
	})
	if err != nil {
		t.Fatalf("render response: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\n%s", err, respJSON)
	}
	card, _ := resp["card"].(map[string]any)
	if card == nil || card["type"] != "raw" {
		t.Fatalf("callback response must use raw card payload: %v", resp)
	}
	if containsCardTag(card["data"], "action") || strings.Contains(respJSON, `"tag":"button"`) {
		t.Fatalf("resolved chat card must drop the buttons: %s", respJSON)
	}
	for _, want := range []string{"是否触发流水线？", "已确认", "确认执行"} {
		if !strings.Contains(respJSON, want) {
			t.Fatalf("resolved chat card missing %q: %s", want, respJSON)
		}
	}

	// Cancel path + legacy cards without embedded content still resolve.
	respJSON, err = renderChatConfirmationCardActionResponse(confirmationCardValue{
		Action:  confirmationActionCancel,
		Message: "取消执行",
	})
	if err != nil {
		t.Fatalf("render cancel response: %v", err)
	}
	for _, want := range []string{"已取消", "取消执行"} {
		if !strings.Contains(respJSON, want) {
			t.Fatalf("resolved cancel card missing %q: %s", want, respJSON)
		}
	}
}

// TestConfirmationReceiptMatchesItsCardSchema pins the pairing that made the
// receipt worth keeping per flow. A card.action.trigger response replaces the
// clicked card wholesale, so a receipt in a different schema than the card it
// overwrites would be asking Lark to swap a live card's schema mid-flight —
// an assumption about the platform we have no reason to make and no way to
// verify from here. The chat flow is 2.0 on both sides; the issue inbox flow
// is 1.0 on both.
func TestConfirmationReceiptMatchesItsCardSchema(t *testing.T) {
	t.Parallel()
	chatCard, err := renderConfirmationCard(
		"是否触发流水线？【确认执行】",
		ChatSessionBinding{ChannelChatID: "oc_1", ChatType: "p2p"},
		"task-1", "ou_req", time.Unix(1720000000, 0),
	)
	if err != nil {
		t.Fatalf("render chat card: %v", err)
	}
	chatReceipt, err := renderChatConfirmationResolvedCard("是否触发流水线？", confirmationActionConfirm, "确认执行")
	if err != nil {
		t.Fatalf("render chat receipt: %v", err)
	}
	if got := cardSchemaOf(t, chatCard); got != "2.0" {
		t.Errorf("chat confirmation card schema=%q want 2.0", got)
	}
	if got := cardSchemaOf(t, chatReceipt); got != "2.0" {
		t.Errorf("chat receipt schema=%q must match the card it replaces", got)
	}

	// The issue inbox confirmation card is schema 1.0, which carries no schema
	// key and puts its components at the top level rather than under body.
	issueReceipt, err := RenderIssueConfirmationResolvedCard("是否确认发布？", IssueConfirmationCardAction{
		Action: confirmationActionConfirm, Message: "确认执行",
	})
	if err != nil {
		t.Fatalf("render issue receipt: %v", err)
	}
	if got := cardSchemaOf(t, issueReceipt); got != "" {
		t.Errorf("issue receipt schema=%q must stay 1.0 to match its card", got)
	}
	var issueDoc map[string]any
	if err := json.Unmarshal([]byte(issueReceipt), &issueDoc); err != nil {
		t.Fatalf("unmarshal issue receipt: %v", err)
	}
	if _, ok := issueDoc["elements"]; !ok {
		t.Errorf("schema 1.0 receipt must keep top-level elements: %s", issueReceipt)
	}
}

func cardSchemaOf(t *testing.T, cardJSON string) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &doc); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	schema, _ := doc["schema"].(string)
	return schema
}

func containsCardTag(v any, tag string) bool {
	switch x := v.(type) {
	case map[string]any:
		if x["tag"] == tag {
			return true
		}
		for _, child := range x {
			if containsCardTag(child, tag) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if containsCardTag(child, tag) {
				return true
			}
		}
	}
	return false
}

func collectCardElementsByTag(v any, tag string, out *[]map[string]any) {
	switch x := v.(type) {
	case map[string]any:
		if x["tag"] == tag {
			*out = append(*out, x)
		}
		for _, child := range x {
			collectCardElementsByTag(child, tag, out)
		}
	case []any:
		for _, child := range x {
			collectCardElementsByTag(child, tag, out)
		}
	}
}
