package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeishuNotifierSendsInboxNotificationByEmail(t *testing.T) {
	var sawTokenRequest bool
	var sawMessageRequest bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			sawTokenRequest = true
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode token request: %v", err)
			}
			if body["app_id"] != "cli_test" || body["app_secret"] != "secret_test" {
				t.Fatalf("unexpected token credentials: %#v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"tenant-token","expire":7200}`))
		case "/open-apis/im/v1/messages":
			sawMessageRequest = true
			if got := r.URL.Query().Get("receive_id_type"); got != "email" {
				t.Fatalf("receive_id_type = %q, want email", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer tenant-token" {
				t.Fatalf("Authorization = %q", got)
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode message request: %v", err)
			}
			if body["receive_id"] != "user@example.com" {
				t.Fatalf("receive_id = %q", body["receive_id"])
			}
			if body["msg_type"] != "interactive" {
				t.Fatalf("msg_type = %q", body["msg_type"])
			}
			if !strings.Contains(body["content"], "Issue title") {
				t.Fatalf("content does not contain title: %s", body["content"])
			}
			if !strings.Contains(body["content"], "新评论") {
				t.Fatalf("content does not contain event label: %s", body["content"])
			}
			if !strings.Contains(body["content"], "Issue body") {
				t.Fatalf("content does not contain notification body: %s", body["content"])
			}
			if !strings.Contains(body["content"], "在 Multica 中查看") {
				t.Fatalf("content does not contain button text: %s", body["content"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"success"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	notifier := &FeishuNotifier{
		client:         server.Client(),
		baseURL:        server.URL,
		appID:          "cli_test",
		appSecret:      "secret_test",
		receiveIDType:  "email",
		frontendOrigin: "https://multica.example.com",
	}

	err := notifier.SendInboxNotification(context.Background(), FeishuInboxNotification{
		RecipientEmail: "user@example.com",
		WorkspaceName:  "Workspace",
		WorkspaceSlug:  "workspace",
		IssueID:        "issue-id",
		Type:           "new_comment",
		Title:          "Issue title",
		Body:           "Issue body",
	})
	if err != nil {
		t.Fatalf("SendInboxNotification: %v", err)
	}
	if !sawTokenRequest || !sawMessageRequest {
		t.Fatalf("expected token and message requests, saw token=%v message=%v", sawTokenRequest, sawMessageRequest)
	}
}

func TestNewFeishuNotifierFromEnvRequiresExplicitEnable(t *testing.T) {
	t.Setenv("FEISHU_NOTIFICATIONS_APP_ID", "cli_test")
	t.Setenv("FEISHU_NOTIFICATIONS_APP_SECRET", "secret_test")

	if notifier := NewFeishuNotifierFromEnv(); notifier != nil {
		t.Fatal("expected nil notifier when FEISHU_NOTIFICATIONS_ENABLED is not true")
	}
}
