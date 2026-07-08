package lark

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func chatAskEvent(t *testing.T, q *fakePatcherQueries, payload protocol.ChatAskPayload) events.Event {
	t.Helper()
	return events.Event{
		Type:          protocol.EventChatAsk,
		TaskID:        payload.TaskID,
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       payload,
	}
}

func withAskRequester(t *testing.T, q *fakePatcherQueries) (taskID string) {
	t.Helper()
	task := uuidFromString(t, "eeee0000-eeee-eeee-eeee-eeeeeeeeeeee")
	requester := uuidFromString(t, "aaaa0000-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	q.task = db.AgentTaskQueue{ID: task, InitiatorUserID: requester}
	q.bindings = []InboxNotificationBinding{{
		UserBinding: UserBinding{
			MulticaUserID:  requester,
			InstallationID: q.installation.ID,
			ChannelUserID:  "ou_requester",
		},
		Installation: q.installation,
	}}
	return uuidString(task)
}

// TestPatcherRendersConfirmAskCard: EventChatAsk type=confirm renders a card
// with approve/decline buttons whose values carry the multica.chat.ask kind,
// and the card message id is written back onto the ask row.
func TestPatcherRendersConfirmAskCard(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := withAskRequester(t, q)

	p.handleEvent(chatAskEvent(t, q, protocol.ChatAskPayload{
		AskID:         "0f0f0f0f-0f0f-0f0f-0f0f-0f0f0f0f0f0f",
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		TaskID:        taskID,
		Type:          "confirm",
		Message:       "是否触发流水线？",
		Action:        "触发流水线 私服更新重启-main",
		ExpiresAtUnix: time.Now().Add(30 * time.Minute).Unix(),
	}))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("expected one ask card; got %d (text=%d)", len(api.sent), len(api.textSent))
	}
	cardJSON := api.sent[0].CardJSON
	for _, want := range []string{chatAskCardActionKind, "是否触发流水线？", "触发流水线 私服更新重启-main", "ou_requester", chatAskChoiceApprove, chatAskChoiceDecline, "确认：触发流水线 私服更新重启-main"} {
		if !strings.Contains(cardJSON, want) {
			t.Errorf("ask card missing %q: %s", want, cardJSON)
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.askMessageUpdates) != 1 || q.askMessageUpdates[0].ChannelMessageID != "lark_card_msg_1" {
		t.Fatalf("ask card message id not persisted: %+v", q.askMessageUpdates)
	}
	if uuidString(q.askMessageUpdates[0].ID) != "0f0f0f0f-0f0f-0f0f-0f0f-0f0f0f0f0f0f" {
		t.Fatalf("ask id mismatch in write-back: %+v", q.askMessageUpdates[0])
	}
}

// TestPatcherAskCardCarriesTopicRouting: for a per-topic session the button
// value must embed the REAL chat id target and the thread id, so the click's
// synthetic answer re-derives the same per-topic session key (the composite
// binding key is not a valid Lark chat id and the callback has no thread).
func TestPatcherAskCardCarriesTopicRouting(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := withAskRequester(t, q)
	q.binding.ChannelChatID = "oc_real:omt_topic1"
	q.binding.Config = []byte(`{"chat_id":"oc_real"}`)
	q.binding.LastThreadID = pgtype.Text{String: "omt_topic1", Valid: true}
	q.binding.LastMessageID = pgtype.Text{String: "om_trigger", Valid: true}

	p.handleEvent(chatAskEvent(t, q, protocol.ChatAskPayload{
		AskID:         "0f0f0f0f-1111-0f0f-0f0f-0f0f0f0f0f0f",
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		TaskID:        taskID,
		Type:          "confirm",
		Message:       "是否执行？",
		Action:        "执行发布",
		ExpiresAtUnix: time.Now().Add(30 * time.Minute).Unix(),
	}))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("expected one ask card; got %d", len(api.sent))
	}
	if got := string(api.sent[0].ChatID); got != "oc_real" {
		t.Fatalf("ask card must target the real chat id, got %q", got)
	}
	if !strings.Contains(api.sent[0].CardJSON, "omt_topic1") {
		t.Fatalf("ask card value must carry the thread id: %s", api.sent[0].CardJSON)
	}
	if strings.Contains(api.sent[0].CardJSON, "oc_real:omt_topic1") {
		t.Fatalf("ask card value must not leak the composite binding key: %s", api.sent[0].CardJSON)
	}
}

// TestPatcherRendersChoiceAskCard: options become one button each, values
// carry the option label as both choice and reply.
func TestPatcherRendersChoiceAskCard(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := withAskRequester(t, q)

	p.handleEvent(chatAskEvent(t, q, protocol.ChatAskPayload{
		AskID:         "0e0e0e0e-0e0e-0e0e-0e0e-0e0e0e0e0e0e",
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		TaskID:        taskID,
		Type:          "choice",
		Message:       "选哪条流水线？",
		Options:       []string{"主干", "预发"},
		ExpiresAtUnix: time.Now().Add(30 * time.Minute).Unix(),
	}))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("expected one choice card; got %d", len(api.sent))
	}
	var card struct {
		Elements []struct {
			Tag     string `json:"tag"`
			Actions []struct {
				Value chatAskCardValue `json:"value"`
			} `json:"actions"`
		} `json:"elements"`
	}
	if err := json.Unmarshal([]byte(api.sent[0].CardJSON), &card); err != nil {
		t.Fatalf("decode card: %v", err)
	}
	var values []chatAskCardValue
	for _, el := range card.Elements {
		if el.Tag != "action" {
			continue
		}
		for _, a := range el.Actions {
			values = append(values, a.Value)
		}
	}
	if len(values) != 2 || values[0].Choice != "主干" || values[0].Reply != "主干" || values[1].Choice != "预发" {
		t.Fatalf("choice button values wrong: %+v", values)
	}
	if !strings.Contains(api.sent[0].CardJSON, "选项不合适可直接回复") {
		t.Fatalf("choice card missing type-instead footer: %s", api.sent[0].CardJSON)
	}
}

// TestPatcherSendsInputAskAsPlainText: type=input must NOT render buttons —
// the exact regression that motivated the structured signal (a "please
// provide the full domain" prompt once rendered dead confirm buttons).
func TestPatcherSendsInputAskAsPlainText(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := withAskRequester(t, q)

	p.handleEvent(chatAskEvent(t, q, protocol.ChatAskPayload{
		AskID:         "0d0d0d0d-0d0d-0d0d-0d0d-0d0d0d0d0d0d",
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		TaskID:        taskID,
		Type:          "input",
		Message:       "请提供完整域名",
		Hint:          "example.com",
		ExpiresAtUnix: time.Now().Add(30 * time.Minute).Unix(),
	}))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 0 {
		t.Fatalf("input ask must not render a card: %v", api.sent)
	}
	if len(api.textSent) != 1 {
		t.Fatalf("input ask should send one text message; got %d", len(api.textSent))
	}
	text := api.textSent[0].Text
	if !strings.Contains(text, "请提供完整域名") || !strings.Contains(text, "example.com") {
		t.Fatalf("input ask text missing message/hint: %q", text)
	}
}

// TestPatcherAskFallsBackToTextWithoutRequesterBinding mirrors the legacy
// confirmation degradation: no bound requester means no one can click, so
// the question ships as plain text instead of a dead card.
func TestPatcherAskFallsBackToTextWithoutRequesterBinding(t *testing.T) {
	p, q, api := newTestPatcher(t)
	q.task = db.AgentTaskQueue{ID: uuidFromString(t, "eeee0001-eeee-eeee-eeee-eeeeeeeeeeee")}

	p.handleEvent(chatAskEvent(t, q, protocol.ChatAskPayload{
		AskID:         "0c0c0c0c-0c0c-0c0c-0c0c-0c0c0c0c0c0c",
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		TaskID:        uuidString(q.task.ID),
		Type:          "confirm",
		Message:       "是否执行？",
		Action:        "执行发布",
		ExpiresAtUnix: time.Now().Add(30 * time.Minute).Unix(),
	}))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 0 {
		t.Fatalf("no requester binding must not produce a clickable card: %v", api.sent)
	}
	if len(api.textSent) != 1 || api.textSent[0].Text != "是否执行？" {
		t.Fatalf("expected plain text fallback; got %+v", api.textSent)
	}
}

// TestPatcherPatchesResolvedAskCard: EventChatAskResolved rewrites the card
// into its receipt form via PATCH (buttons gone, outcome line present).
func TestPatcherPatchesResolvedAskCard(t *testing.T) {
	p, _, api := newTestPatcher(t)

	p.handleEvent(events.Event{
		Type:          protocol.EventChatAskResolved,
		ChatSessionID: uuidString(uuidFromString(t, "cccccccc-cccc-cccc-cccc-cccccccccccc")),
		Payload: protocol.ChatAskResolvedPayload{
			AskID:            "0b0b0b0b-0b0b-0b0b-0b0b-0b0b0b0b0b0b",
			ChatSessionID:    "cccccccc-cccc-cccc-cccc-cccccccccccc",
			TaskID:           "eeee0002-eeee-eeee-eeee-eeeeeeeeeeee",
			Status:           "superseded",
			Message:          "是否触发流水线？",
			ChannelMessageID: "om_ask_card_1",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 {
		t.Fatalf("expected one card patch; got %d", len(api.patched))
	}
	patch := api.patched[0]
	if patch.LarkCardMessageID != "om_ask_card_1" {
		t.Fatalf("patch targeted wrong message: %+v", patch)
	}
	for _, want := range []string{"已失效", "是否触发流水线？", "已被新的请求取代"} {
		if !strings.Contains(patch.CardJSON, want) {
			t.Errorf("receipt card missing %q: %s", want, patch.CardJSON)
		}
	}
	if strings.Contains(patch.CardJSON, `"tag":"button"`) {
		t.Fatalf("receipt card must not keep buttons: %s", patch.CardJSON)
	}

	// A resolved ask that never reached the channel has nothing to patch.
	p.handleEvent(events.Event{
		Type:          protocol.EventChatAskResolved,
		ChatSessionID: "cccccccc-cccc-cccc-cccc-cccccccccccc",
		Payload: protocol.ChatAskResolvedPayload{
			AskID:         "0a0a0a0a-0a0a-0a0a-0a0a-0a0a0a0a0a0a",
			ChatSessionID: "cccccccc-cccc-cccc-cccc-cccccccccccc",
			Status:        "answered",
		},
	})
	if len(api.patched) != 1 {
		t.Fatalf("resolved ask without channel message must not patch: %d", len(api.patched))
	}
}
