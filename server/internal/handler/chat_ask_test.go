package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/lark"
)

// taskActorPost builds a POST request as the Auth middleware would leave it
// for a mat_ task token (see taskActorReq).
func taskActorPost(target string, taskID string, body any) *http.Request {
	req := newRequest("POST", target, body)
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Task-ID", taskID)
	return req
}

func postChatAsk(t *testing.T, taskID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	testHandler.PostChatAsk(rec, taskActorPost("/api/chat/ask", taskID, body))
	return rec
}

func cleanupChatAsks(t *testing.T, taskID string) {
	t.Helper()
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_ask WHERE task_id = $1`, taskID)
	})
}

// TestPostChatAskRequiresTaskActor: same fail-closed boundary as chat
// history — a member JWT/PAT with a forged X-Task-ID must not create asks.
func TestPostChatAskRequiresTaskActor(t *testing.T) {
	taskID := newChatHistoryTask(t, true)
	rec := httptest.NewRecorder()
	req := newRequest("POST", "/api/chat/ask", map[string]any{"type": "input", "message": "m"})
	req.Header.Set("X-Task-ID", taskID) // forged: no task_token actor
	testHandler.PostChatAsk(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestPostChatAskRejectsNonChatTask(t *testing.T) {
	taskID := newChatHistoryTask(t, false)
	rec := postChatAsk(t, taskID, map[string]any{"type": "input", "message": "m"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestPostChatAskValidation: the malformed-parameter matrix from the spec's
// P0 acceptance list — every row must 400 without creating a half-baked ask.
func TestPostChatAskValidation(t *testing.T) {
	taskID := newChatHistoryTask(t, true)
	cleanupChatAsks(t, taskID)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"unknown type", map[string]any{"type": "form", "message": "m"}},
		{"empty message", map[string]any{"type": "input", "message": "  "}},
		{"oversize message", map[string]any{"type": "input", "message": strings.Repeat("长", maxChatAskMessageRunes+1)}},
		{"confirm without action", map[string]any{"type": "confirm", "message": "是否执行？"}},
		{"confirm with options", map[string]any{"type": "confirm", "message": "m", "action": "a", "options": []string{"x", "y"}}},
		{"choice with one option", map[string]any{"type": "choice", "message": "m", "options": []string{"only"}}},
		{"choice with seven options", map[string]any{"type": "choice", "message": "m", "options": []string{"1", "2", "3", "4", "5", "6", "7"}}},
		{"choice with blank option", map[string]any{"type": "choice", "message": "m", "options": []string{"a", "  "}}},
		{"choice with action", map[string]any{"type": "choice", "message": "m", "action": "a", "options": []string{"a", "b"}}},
		{"input with options", map[string]any{"type": "input", "message": "m", "options": []string{"a", "b"}}},
		{"input with action", map[string]any{"type": "input", "message": "m", "action": "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postChatAsk(t, taskID, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
			}
		})
	}
	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM chat_ask WHERE task_id = $1`, taskID).Scan(&n); err != nil {
		t.Fatalf("count asks: %v", err)
	}
	if n != 0 {
		t.Fatalf("invalid requests must not create asks, found %d", n)
	}
}

func TestPostChatAskConfirmHappyPath(t *testing.T) {
	taskID := newChatHistoryTask(t, true)
	cleanupChatAsks(t, taskID)
	rec := postChatAsk(t, taskID, map[string]any{
		"type":    "confirm",
		"message": "是否触发流水线？",
		"action":  "触发流水线 私服更新重启-main",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Ask struct {
			ID        string    `json:"id"`
			Type      string    `json:"type"`
			Status    string    `json:"status"`
			Action    string    `json:"action"`
			ExpiresAt time.Time `json:"expires_at"`
		} `json:"ask"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	if resp.Ask.ID == "" || resp.Ask.Type != "confirm" || resp.Ask.Status != "pending" || resp.Ask.Action == "" {
		t.Fatalf("unexpected ask payload: %+v", resp.Ask)
	}
	ttl := time.Until(resp.Ask.ExpiresAt)
	if ttl < 25*time.Minute || ttl > 35*time.Minute {
		t.Fatalf("expires_at not ~30m out: %v", resp.Ask.ExpiresAt)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM chat_ask WHERE id = $1`, resp.Ask.ID).Scan(&status); err != nil {
		t.Fatalf("load ask row: %v", err)
	}
	if status != "pending" {
		t.Fatalf("stored status = %q, want pending", status)
	}
}

// TestPostChatAskSupersedesPending: a second ask in the same session retires
// the first (spec: at most one pending ask per session).
func TestPostChatAskSupersedesPending(t *testing.T) {
	taskID := newChatHistoryTask(t, true)
	cleanupChatAsks(t, taskID)
	first := postChatAsk(t, taskID, map[string]any{"type": "input", "message": "请提供完整域名"})
	if first.Code != http.StatusCreated {
		t.Fatalf("first ask status = %d: %s", first.Code, first.Body.String())
	}
	second := postChatAsk(t, taskID, map[string]any{
		"type": "choice", "message": "选哪条流水线？", "options": []string{"主干", "预发"},
	})
	if second.Code != http.StatusCreated {
		t.Fatalf("second ask status = %d: %s", second.Code, second.Body.String())
	}
	rows, err := testPool.Query(context.Background(),
		`SELECT status FROM chat_ask WHERE task_id = $1 ORDER BY created_at`, taskID)
	if err != nil {
		t.Fatalf("query asks: %v", err)
	}
	defer rows.Close()
	var statuses []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		statuses = append(statuses, s)
	}
	if len(statuses) != 2 || statuses[0] != "superseded" || statuses[1] != "pending" {
		t.Fatalf("statuses = %v, want [superseded pending]", statuses)
	}
}

func createPendingAsk(t *testing.T, taskID string) (askID string) {
	t.Helper()
	rec := postChatAsk(t, taskID, map[string]any{
		"type":    "confirm",
		"message": "是否触发流水线？",
		"action":  "触发流水线 X",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed ask: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Ask struct {
			ID string `json:"id"`
		} `json:"ask"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode seed ask: %v", err)
	}
	return resp.Ask.ID
}

func chatAskClick(askID, messageID, reply string) lark.InboundMessage {
	return lark.InboundMessage{
		EventID:      "evt-" + messageID,
		AppID:        "cli_app_x",
		ChatID:       "oc_test",
		ChatType:     lark.ChatTypeP2P,
		MessageID:    messageID,
		SenderOpenID: "ou_requester",
		Body:         reply,
		CommandBody:  reply,
		MessageType:  "text",
		CardAction: &lark.InboundCardAction{
			CardMessageID: "om_ask_card",
			ChatAsk: &lark.ChatAskCardAction{
				AskID:         askID,
				Choice:        "approve",
				Reply:         reply,
				AllowedOpenID: "ou_requester",
				ExpiresAtUnix: time.Now().Add(30 * time.Minute).Unix(),
			},
		},
	}
}

// TestHandleLarkChatAskActionLifecycle drives the click state machine:
// fresh click answers + dispatches; the same click redelivered dispatches
// again (downstream dedup absorbs it); a different click on the answered ask
// gets a receipt but must NOT dispatch.
func TestHandleLarkChatAskActionLifecycle(t *testing.T) {
	taskID := newChatHistoryTask(t, true)
	cleanupChatAsks(t, taskID)
	askID := createPendingAsk(t, taskID)
	ctx := context.Background()

	res, err := testHandler.HandleLarkCardAction(ctx, chatAskClick(askID, "chat_ask:click-1", "确认：触发流水线 X"))
	if err != nil {
		t.Fatalf("first click: %v", err)
	}
	if !res.DispatchAsChatText {
		t.Fatal("fresh click must dispatch the answer text")
	}
	for _, want := range []string{"已回复", "是否触发流水线？", "确认：触发流水线 X"} {
		if !strings.Contains(res.CardActionResponseJSON, want) {
			t.Fatalf("receipt missing %q: %s", want, res.CardActionResponseJSON)
		}
	}
	var status, answeredBy string
	var answer []byte
	if err := testPool.QueryRow(ctx, `SELECT status, answered_by, answer FROM chat_ask WHERE id = $1`, askID).Scan(&status, &answeredBy, &answer); err != nil {
		t.Fatalf("load ask: %v", err)
	}
	if status != "answered" || answeredBy != "ou_requester" || !strings.Contains(string(answer), "chat_ask:click-1") {
		t.Fatalf("ask row after click: status=%q by=%q answer=%s", status, answeredBy, answer)
	}

	replay, err := testHandler.HandleLarkCardAction(ctx, chatAskClick(askID, "chat_ask:click-1", "确认：触发流水线 X"))
	if err != nil {
		t.Fatalf("replayed click: %v", err)
	}
	if !replay.DispatchAsChatText {
		t.Fatal("redelivery of the answering click must re-dispatch (dedup handles it downstream)")
	}

	second, err := testHandler.HandleLarkCardAction(ctx, chatAskClick(askID, "chat_ask:click-2", "确认：触发流水线 X"))
	if err != nil {
		t.Fatalf("second click: %v", err)
	}
	if second.DispatchAsChatText {
		t.Fatal("a different click on an answered ask must not dispatch")
	}
	if !strings.Contains(second.CardActionResponseJSON, "已回复") {
		t.Fatalf("stale click should ACK the answered receipt: %s", second.CardActionResponseJSON)
	}
}

// TestHandleLarkChatAskActionExpired: a click past the TTL flips the ask to
// expired, ACKs the expiry receipt, and never dispatches.
func TestHandleLarkChatAskActionExpired(t *testing.T) {
	taskID := newChatHistoryTask(t, true)
	cleanupChatAsks(t, taskID)
	askID := createPendingAsk(t, taskID)
	ctx := context.Background()

	click := chatAskClick(askID, "chat_ask:late-click", "确认：触发流水线 X")
	click.CardAction.ChatAsk.ExpiresAtUnix = time.Now().Add(-time.Minute).Unix()
	res, err := testHandler.HandleLarkCardAction(ctx, click)
	if err != nil {
		t.Fatalf("expired click: %v", err)
	}
	if res.DispatchAsChatText {
		t.Fatal("expired click must not dispatch")
	}
	if !strings.Contains(res.CardActionResponseJSON, "已过期") {
		t.Fatalf("expected expiry receipt: %s", res.CardActionResponseJSON)
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM chat_ask WHERE id = $1`, askID).Scan(&status); err != nil {
		t.Fatalf("load ask: %v", err)
	}
	if status != "expired" {
		t.Fatalf("status = %q, want expired", status)
	}
}

// TestHandleLarkChatAskActionWrongOperator: only the requester may act; other
// operators are dropped silently and the ask stays pending.
func TestHandleLarkChatAskActionWrongOperator(t *testing.T) {
	taskID := newChatHistoryTask(t, true)
	cleanupChatAsks(t, taskID)
	askID := createPendingAsk(t, taskID)
	ctx := context.Background()

	click := chatAskClick(askID, "chat_ask:foreign-click", "确认：触发流水线 X")
	click.SenderOpenID = "ou_other"
	res, err := testHandler.HandleLarkCardAction(ctx, click)
	if err != nil {
		t.Fatalf("foreign click: %v", err)
	}
	if res.DispatchAsChatText || res.CardActionResponseJSON != "" {
		t.Fatalf("foreign click must be a no-op: %+v", res)
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM chat_ask WHERE id = $1`, askID).Scan(&status); err != nil {
		t.Fatalf("load ask: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}
}

// TestResolveAskOnUserMessageTextPreemption: any user text entering the
// session resolves the pending ask (one-shot nonce), and a second call
// no-ops. Content is deliberately NOT matched.
func TestResolveAskOnUserMessageTextPreemption(t *testing.T) {
	taskID := newChatHistoryTask(t, true)
	cleanupChatAsks(t, taskID)
	askID := createPendingAsk(t, taskID)
	ctx := context.Background()

	var sessionID string
	if err := testPool.QueryRow(ctx, `SELECT chat_session_id FROM chat_ask WHERE id = $1`, askID).Scan(&sessionID); err != nil {
		t.Fatalf("load session id: %v", err)
	}
	var sessionUUID, senderUUID = parseUUID(sessionID), parseUUID(testUserID)

	// A reply that is NOT an answer to the buttons still retires the ask.
	testHandler.ResolveAskOnUserMessage(ctx, sessionUUID, senderUUID, "先等等，这条流水线上次跑是什么时候？")

	var status, answeredBy string
	var answer []byte
	if err := testPool.QueryRow(ctx, `SELECT status, answered_by, answer FROM chat_ask WHERE id = $1`, askID).Scan(&status, &answeredBy, &answer); err != nil {
		t.Fatalf("load ask: %v", err)
	}
	if status != "answered" || answeredBy != testUserID {
		t.Fatalf("ask after text preemption: status=%q by=%q", status, answeredBy)
	}
	var recorded chatAskClickAnswer
	if err := json.Unmarshal(answer, &recorded); err != nil || recorded.Via != "text" {
		t.Fatalf("answer should record text provenance: %s (err=%v)", answer, err)
	}

	// No pending ask left: the hook is a silent no-op for ordinary messages.
	testHandler.ResolveAskOnUserMessage(ctx, sessionUUID, senderUUID, "随便再说一句")
}

// TestClaimTaskChatAskSupportedGates: the ask contract needs BOTH ends —
// a Feishu-backed session AND a daemon that self-reports the capability on
// the claim body. An old daemon (empty body) or a web-only session must not
// get chat_ask_supported, otherwise the prompt teaches a command that either
// doesn't exist in the CLI or asks into a channel with no renderer.
func TestClaimTaskChatAskSupportedGates(t *testing.T) {
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "ChatAskClaimAgent", []byte("[]"))
	runtimeID := handlerTestRuntimeID(t)
	sessionID := createHandlerTestChatSession(t, agentID)

	const appID = "cli_chat_ask_claim"
	const channelChatID = "oc_chat_ask_claim"
	cleanChannel := func() {
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM channel_chat_session_binding WHERE channel_chat_id = $1`, channelChatID)
		_, _ = testPool.Exec(context.Background(),
			`DELETE FROM channel_installation WHERE channel_type = 'feishu' AND config->>'app_id' = $1`, appID)
	}
	cleanChannel()
	t.Cleanup(cleanChannel)

	var installID string
	if err := testPool.QueryRow(ctx, `
INSERT INTO channel_installation (workspace_id, agent_id, channel_type, config, installer_user_id)
VALUES ($1, $2, 'feishu', jsonb_build_object('app_id', $3::text), $4)
RETURNING id
`, testWorkspaceID, agentID, appID, testUserID).Scan(&installID); err != nil {
		t.Fatalf("insert channel_installation: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
INSERT INTO channel_chat_session_binding (chat_session_id, installation_id, channel_type, channel_chat_id, chat_type)
VALUES ($1, $2, 'feishu', $3, 'p2p')
`, sessionID, installID, channelChatID); err != nil {
		t.Fatalf("insert channel_chat_session_binding: %v", err)
	}

	claim := func(t *testing.T, body any) (supported bool) {
		t.Helper()
		var taskID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, chat_session_id)
			VALUES ($1, $2, 'queued', 0, $3)
			RETURNING id
		`, agentID, runtimeID, sessionID).Scan(&taskID); err != nil {
			t.Fatalf("insert claimable chat task: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
		})
		w := httptest.NewRecorder()
		req := newDaemonTokenRequest("POST", "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", body,
			testWorkspaceID, "claim-chat-ask")
		req = withURLParam(req, "runtimeId", runtimeID)
		testHandler.ClaimTaskByRuntime(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("claim: %d %s", w.Code, w.Body.String())
		}
		var resp struct {
			Task *struct {
				ID               string `json:"id"`
				ChatAskSupported bool   `json:"chat_ask_supported"`
			} `json:"task"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode claim: %v", err)
		}
		if resp.Task == nil {
			t.Fatal("expected a claimed task")
		}
		// Free the runtime for the next subtest claim (one active task per
		// runtime).
		if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'completed' WHERE id = $1`, resp.Task.ID); err != nil {
			t.Fatalf("complete claimed task: %v", err)
		}
		return resp.Task.ChatAskSupported
	}

	if !claim(t, map[string]any{"supports_chat_ask": true}) {
		t.Fatal("capable daemon + feishu session must get chat_ask_supported")
	}
	if claim(t, nil) {
		t.Fatal("old daemon (empty claim body) must not get chat_ask_supported")
	}
}

// TestChatAskPendingUniqueIndex is a structural test of the DB invariant:
// two pending asks in one session must be impossible regardless of handler
// bugs, because supersede+insert races would otherwise leave two live cards.
func TestChatAskPendingUniqueIndex(t *testing.T) {
	taskID := newChatHistoryTask(t, true)
	cleanupChatAsks(t, taskID)
	rec := postChatAsk(t, taskID, map[string]any{"type": "input", "message": "m"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed ask status = %d", rec.Code)
	}
	var sessionID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT chat_session_id FROM chat_ask WHERE task_id = $1`, taskID).Scan(&sessionID); err != nil {
		t.Fatalf("load session id: %v", err)
	}
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO chat_ask (chat_session_id, task_id, type, message, expires_at)
		VALUES ($1, $2, 'input', 'dup', now() + interval '30 minutes')
	`, sessionID, taskID)
	if err == nil || !strings.Contains(err.Error(), "idx_chat_ask_pending_per_session") {
		t.Fatalf("second pending insert must violate the partial unique index, got err=%v", err)
	}
}
