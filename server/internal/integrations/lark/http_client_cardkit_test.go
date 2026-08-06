package lark

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestHTTPClientCardKitLifecycleWireShape(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok_cardkit", 7200)

	var createBody map[string]any
	var sendContent string
	var updateBody map[string]any
	fake.mux.HandleFunc("/open-apis/cardkit/v1/cards", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("create method=%s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"card_id": "7371713483664506900"}})
	})
	fake.mux.HandleFunc("/open-apis/im/v1/messages/om_parent/reply", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode send: %v", err)
		}
		sendContent, _ = body["content"].(string)
		if body["uuid"] != "stream-task-1" || body["reply_in_thread"] != true {
			t.Fatalf("send body=%v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_cardkit"}})
	})
	fake.mux.HandleFunc("/open-apis/cardkit/v1/cards/7371713483664506900", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("update method=%s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&updateBody); err != nil {
			t.Fatalf("decode update: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	})

	client := newTestClient(fake, time.Now)
	cardJSON := `{"schema":"2.0","body":{"elements":[{"tag":"markdown","element_id":"agent_progress","content":"working"}]}}`
	cardID, err := client.CreateCardKitCard(context.Background(), CreateCardKitCardParams{InstallationID: testCreds(), CardJSON: cardJSON})
	if err != nil || cardID != "7371713483664506900" {
		t.Fatalf("create id=%q err=%v", cardID, err)
	}
	messageID, err := client.SendCardKitCard(context.Background(), SendCardKitCardParams{
		InstallationID: testCreds(), ChatID: "oc_test", CardID: cardID,
		IdempotencyKey: "stream-task-1", ReplyTarget: ReplyTarget{MessageID: "om_parent", InThread: true},
	})
	if err != nil || messageID != "om_cardkit" {
		t.Fatalf("send id=%q err=%v", messageID, err)
	}
	if err := client.UpdateCardKitCard(context.Background(), UpdateCardKitCardParams{
		InstallationID: testCreds(), CardID: cardID, CardJSON: cardJSON,
		IdempotencyKey: "task-1-rev-2", Sequence: 2,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if createBody["type"] != "card_json" || createBody["data"] != cardJSON {
		t.Fatalf("create body=%v", createBody)
	}
	if sendContent != `{"data":{"card_id":"7371713483664506900"},"type":"card"}` {
		t.Fatalf("send content=%q", sendContent)
	}
	if updateBody["uuid"] != "task-1-rev-2" || updateBody["sequence"] != float64(2) {
		t.Fatalf("update body=%v", updateBody)
	}
	card, ok := updateBody["card"].(map[string]any)
	if !ok || card["schema"] != "2.0" {
		t.Fatalf("update card=%v", updateBody["card"])
	}
}
