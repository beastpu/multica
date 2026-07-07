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
