package lark

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// The fakes below reject a request the way the Open Platform does rather than
// accepting whatever the client happens to send. The previous version of this
// test asserted only what our own code produced, so a wrong envelope on the
// full-update call passed CI and then 400'd in production with 99992402
// ("card.type is required"), which dropped every reply that needed an update.
//
// Contracts, from the CardKit v1 reference:
//
//	create   POST  /cards                              {"type":"card_json","data":"<json string>"}
//	update   PUT   /cards/:id                          {"card":{"type","data"},"sequence"}
//	stream   PUT   /cards/:id/elements/:eid/content    {"content":"<full text>","sequence"}
//	settings PATCH /cards/:id/settings                 {"settings":"<json string>","sequence"}
func rejectUnlessValid(t *testing.T, w http.ResponseWriter, ok bool, field string) bool {
	t.Helper()
	if ok {
		return true
	}
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code": 99992402, "msg": "field validation failed: " + field + " is required",
	})
	return false
}

func TestHTTPClientCardKitWireContracts(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok_cardkit", 7200)

	const cardID = "7371713483664506900"
	cardJSON := `{"schema":"2.0","body":{"elements":[{"tag":"markdown","element_id":"agent_reply","content":"working"}]}}`

	var createBody, updateBody, streamBody, settingsBody map[string]any
	var sendContent string

	fake.mux.HandleFunc("/open-apis/cardkit/v1/cards", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("create method=%s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&createBody)
		if !rejectUnlessValid(t, w, createBody["type"] == "card_json", "type") {
			return
		}
		if _, isString := createBody["data"].(string); !rejectUnlessValid(t, w, isString, "data") {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"card_id": cardID}})
	})
	fake.mux.HandleFunc("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sendContent, _ = body["content"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_cardkit"}})
	})
	fake.mux.HandleFunc("/open-apis/cardkit/v1/cards/"+cardID, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("update method=%s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&updateBody)
		card, _ := updateBody["card"].(map[string]any)
		if !rejectUnlessValid(t, w, card != nil && card["type"] == "card_json", "card.type") {
			return
		}
		if _, isString := card["data"].(string); !rejectUnlessValid(t, w, isString, "card.data") {
			return
		}
		if !rejectUnlessValid(t, w, updateBody["sequence"] != nil, "sequence") {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	})
	fake.mux.HandleFunc("/open-apis/cardkit/v1/cards/"+cardID+"/elements/agent_reply/content", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("stream method=%s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&streamBody)
		if _, isString := streamBody["content"].(string); !rejectUnlessValid(t, w, isString, "content") {
			return
		}
		if !rejectUnlessValid(t, w, streamBody["sequence"] != nil, "sequence") {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	})
	fake.mux.HandleFunc("/open-apis/cardkit/v1/cards/"+cardID+"/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("settings method=%s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&settingsBody)
		raw, isString := settingsBody["settings"].(string)
		if !rejectUnlessValid(t, w, isString, "settings") {
			return
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			t.Fatalf("settings must be a JSON string, got %q", raw)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	})

	var client CardKitAPIClient = newTestClient(fake, time.Now)
	ctx := context.Background()

	gotCardID, err := client.CreateCardKitCard(ctx, CreateCardKitCardParams{InstallationID: testCreds(), CardJSON: cardJSON})
	if err != nil || gotCardID != cardID {
		t.Fatalf("create id=%q err=%v", gotCardID, err)
	}
	if createBody["data"] != cardJSON {
		t.Errorf("create must carry the document as a string: %v", createBody["data"])
	}

	messageID, err := client.SendCardKitCard(ctx, SendCardKitCardParams{
		InstallationID: testCreds(), ChatID: "oc_test", CardID: cardID, IdempotencyKey: "stream-task-1",
	})
	if err != nil || messageID != "om_cardkit" {
		t.Fatalf("send id=%q err=%v", messageID, err)
	}
	if sendContent != `{"data":{"card_id":"7371713483664506900"},"type":"card"}` {
		t.Errorf("send content=%q", sendContent)
	}

	if err := client.StreamCardKitText(ctx, StreamCardKitTextParams{
		InstallationID: testCreds(), CardID: cardID, ElementID: "agent_reply",
		Content: "hello world", Sequence: 1, IdempotencyKey: "task-1-seq-1",
	}); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if streamBody["content"] != "hello world" || streamBody["sequence"] != float64(1) {
		t.Errorf("stream body=%v", streamBody)
	}

	if err := client.CloseCardKitStreaming(ctx, CloseCardKitStreamingParams{
		InstallationID: testCreds(), CardID: cardID, Sequence: 2, IdempotencyKey: "task-1-close",
	}); err != nil {
		t.Fatalf("close streaming: %v", err)
	}
	var settings struct {
		Config struct {
			StreamingMode *bool `json:"streaming_mode"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(settingsBody["settings"].(string)), &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if settings.Config.StreamingMode == nil || *settings.Config.StreamingMode {
		t.Errorf("close must set streaming_mode=false, got %v", settingsBody["settings"])
	}

	if err := client.UpdateCardKitCard(ctx, UpdateCardKitCardParams{
		InstallationID: testCreds(), CardID: cardID, CardJSON: cardJSON,
		Sequence: 3, IdempotencyKey: "task-1-seq-3",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	card, _ := updateBody["card"].(map[string]any)
	if card["type"] != "card_json" || card["data"] != cardJSON {
		t.Errorf("update card=%v", updateBody["card"])
	}
	if updateBody["sequence"] != float64(3) {
		t.Errorf("update sequence=%v want 3", updateBody["sequence"])
	}
}

// TestHTTPClientCardKitRejectsUnorderedSequence pins the guard that keeps a
// zero or negative sequence from reaching Feishu, where it would be rejected
// and — being a 4xx — treated as permanent.
func TestHTTPClientCardKitRejectsUnorderedSequence(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tok_seq", 7200)
	var client CardKitAPIClient = newTestClient(fake, time.Now)
	ctx := context.Background()

	if err := client.StreamCardKitText(ctx, StreamCardKitTextParams{
		InstallationID: testCreds(), CardID: "c1", ElementID: "e1", Content: "x", Sequence: 0,
	}); err == nil {
		t.Error("stream with sequence 0 must be rejected locally")
	}
	if err := client.UpdateCardKitCard(ctx, UpdateCardKitCardParams{
		InstallationID: testCreds(), CardID: "c1", CardJSON: `{"schema":"2.0"}`, Sequence: 0,
	}); err == nil {
		t.Error("update with sequence 0 must be rejected locally")
	}
	if err := client.CloseCardKitStreaming(ctx, CloseCardKitStreamingParams{
		InstallationID: testCreds(), CardID: "c1", Sequence: 0,
	}); err == nil {
		t.Error("close with sequence 0 must be rejected locally")
	}
}
