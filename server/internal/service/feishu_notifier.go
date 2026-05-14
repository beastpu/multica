package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	feishuDefaultBaseURL       = "https://open.feishu.cn"
	feishuDefaultReceiveIDType = "email"
)

type FeishuInboxNotification struct {
	RecipientEmail string
	WorkspaceName  string
	WorkspaceSlug  string
	IssueID        string
	Type           string
	Title          string
	Body           string
}

type FeishuNotifier struct {
	client         *http.Client
	baseURL        string
	appID          string
	appSecret      string
	receiveIDType  string
	frontendOrigin string

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

func NewFeishuNotifierFromEnv() *FeishuNotifier {
	if !envBool("FEISHU_NOTIFICATIONS_ENABLED") {
		return nil
	}

	appID := strings.TrimSpace(os.Getenv("FEISHU_NOTIFICATIONS_APP_ID"))
	appSecret := strings.TrimSpace(os.Getenv("FEISHU_NOTIFICATIONS_APP_SECRET"))
	if appID == "" || appSecret == "" {
		slog.Warn("Feishu notifications enabled but FEISHU_NOTIFICATIONS_APP_ID or FEISHU_NOTIFICATIONS_APP_SECRET is missing")
		return nil
	}

	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("FEISHU_API_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = feishuDefaultBaseURL
	}

	receiveIDType := strings.TrimSpace(os.Getenv("FEISHU_MESSAGE_RECEIVE_ID_TYPE"))
	if receiveIDType == "" {
		receiveIDType = feishuDefaultReceiveIDType
	}

	return &FeishuNotifier{
		client:         &http.Client{Timeout: 5 * time.Second},
		baseURL:        baseURL,
		appID:          appID,
		appSecret:      appSecret,
		receiveIDType:  receiveIDType,
		frontendOrigin: strings.TrimRight(strings.TrimSpace(os.Getenv("FRONTEND_ORIGIN")), "/"),
	}
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (n *FeishuNotifier) SendInboxNotification(ctx context.Context, notif FeishuInboxNotification) error {
	if n == nil {
		return nil
	}
	recipient := strings.TrimSpace(notif.RecipientEmail)
	if recipient == "" {
		return nil
	}

	token, err := n.tenantAccessToken(ctx)
	if err != nil {
		return err
	}

	card, err := json.Marshal(n.renderCard(notif))
	if err != nil {
		return err
	}

	body, err := json.Marshal(map[string]string{
		"receive_id": recipient,
		"msg_type":   "interactive",
		"content":    string(card),
	})
	if err != nil {
		return err
	}

	endpoint := n.baseURL + "/open-apis/im/v1/messages?receive_id_type=" + url.QueryEscape(n.receiveIDType)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode Feishu message response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || parsed.Code != 0 {
		return fmt.Errorf("Feishu message send failed: status=%d code=%d msg=%s", resp.StatusCode, parsed.Code, parsed.Msg)
	}
	return nil
}

func (n *FeishuNotifier) renderCard(notif FeishuInboxNotification) map[string]any {
	elements := []map[string]any{
		{
			"tag": "div",
			"text": map[string]string{
				"tag":     "lark_md",
				"content": n.renderMarkdown(notif),
			},
		},
	}
	if link := n.issueURL(notif); link != "" {
		elements = append(elements,
			map[string]any{"tag": "hr"},
			map[string]any{
				"tag": "action",
				"actions": []map[string]any{
					{
						"tag":  "button",
						"text": map[string]string{"tag": "plain_text", "content": "在 Multica 中查看"},
						"url":  link,
						"type": "primary",
					},
				},
			},
		)
	}

	return map[string]any{
		"config": map[string]bool{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"template": "wathet",
			"title": map[string]string{
				"tag":     "plain_text",
				"content": n.cardTitle(notif),
			},
		},
		"elements": elements,
	}
}

func (n *FeishuNotifier) cardTitle(notif FeishuInboxNotification) string {
	if notif.IssueID != "" {
		return notif.Title
	}
	return "Multica 通知"
}

func (n *FeishuNotifier) renderMarkdown(notif FeishuInboxNotification) string {
	var b strings.Builder
	if notif.WorkspaceName != "" {
		b.WriteString("**工作区**：")
		b.WriteString(notif.WorkspaceName)
		b.WriteString("\n\n")
	}
	if notif.Title != "" {
		b.WriteString("**Issue**：")
		b.WriteString(notif.Title)
		b.WriteString("\n\n")
	}
	if label := notificationTypeLabel(notif.Type); label != "" {
		b.WriteString("**事件**：")
		b.WriteString(label)
		b.WriteString("\n\n")
	}
	b.WriteString("**通知内容**：")
	if notif.Body != "" {
		b.WriteString(notif.Body)
	} else {
		b.WriteString(notif.Title)
	}
	return b.String()
}

func notificationTypeLabel(notificationType string) string {
	switch notificationType {
	case "issue_assigned":
		return "分配"
	case "status_changed":
		return "状态变更"
	case "new_comment":
		return "新评论"
	case "mentioned":
		return "提及"
	case "priority_changed":
		return "优先级变更"
	case "due_date_changed":
		return "截止日期变更"
	case "task_completed":
		return "智能体任务完成"
	case "task_failed":
		return "智能体任务失败"
	default:
		return ""
	}
}

func (n *FeishuNotifier) issueURL(notif FeishuInboxNotification) string {
	if n.frontendOrigin == "" || notif.WorkspaceSlug == "" || notif.IssueID == "" {
		return ""
	}
	return n.frontendOrigin + "/" + url.PathEscape(notif.WorkspaceSlug) + "/issues/" + url.PathEscape(notif.IssueID)
}

func (n *FeishuNotifier) tenantAccessToken(ctx context.Context) (string, error) {
	n.mu.Lock()
	if n.token != "" && time.Now().Before(n.tokenExpiry) {
		token := n.token
		n.mu.Unlock()
		return token, nil
	}
	n.mu.Unlock()

	body, err := json.Marshal(map[string]string{
		"app_id":     n.appID,
		"app_secret": n.appSecret,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.baseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var parsed struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int64  `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode Feishu token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || parsed.Code != 0 || parsed.TenantAccessToken == "" {
		return "", fmt.Errorf("Feishu tenant token failed: status=%d code=%d msg=%s", resp.StatusCode, parsed.Code, parsed.Msg)
	}

	expiresIn := time.Duration(parsed.Expire) * time.Second
	if expiresIn <= 0 {
		expiresIn = time.Hour
	}

	n.mu.Lock()
	n.token = parsed.TenantAccessToken
	n.tokenExpiry = time.Now().Add(expiresIn - time.Minute)
	token := n.token
	n.mu.Unlock()

	return token, nil
}
