package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const inboxNotifyTimeout = 10 * time.Second

type InboxNotifierQueries interface {
	GetIssue(ctx context.Context, id pgtype.UUID) (db.Issue, error)
	GetWorkspace(ctx context.Context, id pgtype.UUID) (db.Workspace, error)
	ClaimLarkInboxNotificationDelivery(ctx context.Context, arg db.ClaimLarkInboxNotificationDeliveryParams) (bool, error)
	GetLarkInboxIssueCard(ctx context.Context, arg db.GetLarkInboxIssueCardParams) (db.LarkInboxIssueCard, error)
	ListActiveLarkUserBindingsByMember(ctx context.Context, arg db.ListActiveLarkUserBindingsByMemberParams) ([]db.ListActiveLarkUserBindingsByMemberRow, error)
	ListLarkInboxIssueCardItems(ctx context.Context, arg db.ListLarkInboxIssueCardItemsParams) ([]db.InboxItem, error)
	TouchLarkInboxIssueCard(ctx context.Context, id pgtype.UUID) error
	UpsertLarkInboxIssueCard(ctx context.Context, arg db.UpsertLarkInboxIssueCardParams) (db.LarkInboxIssueCard, error)
}

type InboxNotifier struct {
	queries     InboxNotifierQueries
	credentials CredentialsResolver
	client      APIClient
	log         *slog.Logger
	publicURL   string
}

type InboxNotifierConfig struct {
	Logger    *slog.Logger
	PublicURL string
}

func NewInboxNotifier(queries InboxNotifierQueries, credentials CredentialsResolver, client APIClient, cfg InboxNotifierConfig) *InboxNotifier {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &InboxNotifier{
		queries:     queries,
		credentials: credentials,
		client:      client,
		log:         log,
		publicURL:   strings.TrimRight(strings.TrimSpace(cfg.PublicURL), "/"),
	}
}

func (n *InboxNotifier) Register(bus *events.Bus) {
	if bus == nil {
		return
	}
	bus.Subscribe(protocol.EventInboxNew, func(e events.Event) {
		go n.handleEvent(e)
	})
}

func (n *InboxNotifier) handleEvent(e events.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), inboxNotifyTimeout)
	defer cancel()
	if err := n.notify(ctx, e.Payload); err != nil {
		n.log.Warn("lark inbox notifier: notify failed", "err", err.Error())
	}
}

func (n *InboxNotifier) notify(ctx context.Context, payload any) error {
	if n.queries == nil || n.credentials == nil || n.client == nil {
		return errors.New("notifier not configured")
	}
	item, ok := inboxNotificationItemFromPayload(payload)
	if !ok {
		return errors.New("missing inbox item payload")
	}
	if item.RecipientType != "member" {
		return nil
	}
	itemID, err := scanUUID(item.ID)
	if err != nil {
		return fmt.Errorf("parse inbox item id: %w", err)
	}
	workspaceID, err := scanUUID(item.WorkspaceID)
	if err != nil {
		return fmt.Errorf("parse workspace_id: %w", err)
	}
	recipientID, err := scanUUID(item.RecipientID)
	if err != nil {
		return fmt.Errorf("parse recipient_id: %w", err)
	}
	rows, err := n.queries.ListActiveLarkUserBindingsByMember(ctx, db.ListActiveLarkUserBindingsByMemberParams{
		WorkspaceID:   workspaceID,
		MulticaUserID: recipientID,
	})
	if err != nil {
		return fmt.Errorf("lookup lark binding: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	row, ok := selectInboxNotificationBinding(ctx, n.queries, rows, item)
	if !ok {
		return nil
	}
	claimed, err := n.queries.ClaimLarkInboxNotificationDelivery(ctx, db.ClaimLarkInboxNotificationDeliveryParams{
		InboxItemID:    itemID,
		InstallationID: row.LarkInstallation.ID,
		LarkOpenID:     row.LarkUserBinding.LarkOpenID,
	})
	if err != nil {
		return fmt.Errorf("claim lark inbox notification delivery: %w", err)
	}
	if !claimed {
		return nil
	}
	creds, err := n.installationCredentials(row.LarkInstallation)
	if err != nil {
		return err
	}
	if isMergeableLarkInboxNotification(item) {
		return n.sendOrPatchInboxIssueCard(ctx, creds, row, workspaceID, recipientID, item)
	}
	cardJSON, err := n.renderInboxNotificationCard(ctx, workspaceID, item)
	if err != nil {
		return fmt.Errorf("render inbox card: %w", err)
	}
	if _, err := n.client.SendDirectInteractiveCard(ctx, SendDirectCardParams{
		InstallationID: creds,
		OpenID:         OpenID(row.LarkUserBinding.LarkOpenID),
		CardJSON:       cardJSON,
	}); err != nil {
		return fmt.Errorf("send inbox dm: %w", err)
	}
	return nil
}

func (n *InboxNotifier) installationCredentials(inst db.LarkInstallation) (InstallationCredentials, error) {
	secret, err := n.credentials.DecryptAppSecret(inst)
	if err != nil {
		return InstallationCredentials{}, fmt.Errorf("decrypt app_secret: %w", err)
	}
	creds := InstallationCredentials{
		AppID:     inst.AppID,
		AppSecret: secret,
	}
	if inst.TenantKey.Valid {
		creds.TenantKey = inst.TenantKey.String
	}
	return creds, nil
}

type inboxNotificationItem struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	RecipientType string          `json:"recipient_type"`
	RecipientID   string          `json:"recipient_id"`
	Type          string          `json:"type"`
	Severity      string          `json:"severity"`
	IssueID       *string         `json:"issue_id"`
	Title         string          `json:"title"`
	Body          *string         `json:"body"`
	ActorType     *string         `json:"actor_type"`
	ActorID       *string         `json:"actor_id"`
	Details       json.RawMessage `json:"details"`
}

func inboxNotificationItemFromPayload(payload any) (inboxNotificationItem, bool) {
	m, ok := payload.(map[string]any)
	if !ok {
		return inboxNotificationItem{}, false
	}
	raw, ok := m["item"]
	if !ok {
		return inboxNotificationItem{}, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return inboxNotificationItem{}, false
	}
	var item inboxNotificationItem
	if err := json.Unmarshal(b, &item); err != nil {
		return inboxNotificationItem{}, false
	}
	return item, item.WorkspaceID != "" && item.RecipientID != ""
}

func selectInboxNotificationBinding(ctx context.Context, queries InboxNotifierQueries, rows []db.ListActiveLarkUserBindingsByMemberRow, item inboxNotificationItem) (db.ListActiveLarkUserBindingsByMemberRow, bool) {
	if item.ActorType != nil && *item.ActorType == "agent" && item.ActorID != nil {
		if actorID, err := scanUUID(*item.ActorID); err == nil {
			if row, ok := selectInboxNotificationBindingByAgent(rows, actorID); ok {
				return row, true
			}
		}
	}
	if item.IssueID != nil && queries != nil {
		if issueID, err := scanUUID(*item.IssueID); err == nil {
			if issue, err := queries.GetIssue(ctx, issueID); err == nil &&
				issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
				if row, ok := selectInboxNotificationBindingByAgent(rows, issue.AssigneeID); ok {
					return row, true
				}
			}
		}
	}
	return db.ListActiveLarkUserBindingsByMemberRow{}, false
}

func isMergeableLarkInboxNotification(item inboxNotificationItem) bool {
	if item.IssueID == nil || *item.IssueID == "" {
		return false
	}
	switch item.Type {
	case "quick_create_done", "status_changed":
		return true
	default:
		return false
	}
}

func selectInboxNotificationBindingByAgent(rows []db.ListActiveLarkUserBindingsByMemberRow, agentID pgtype.UUID) (db.ListActiveLarkUserBindingsByMemberRow, bool) {
	for _, row := range rows {
		if row.LarkInstallation.AgentID == agentID {
			return row, true
		}
	}
	return db.ListActiveLarkUserBindingsByMemberRow{}, false
}

func (n *InboxNotifier) sendOrPatchInboxIssueCard(
	ctx context.Context,
	creds InstallationCredentials,
	row db.ListActiveLarkUserBindingsByMemberRow,
	workspaceID pgtype.UUID,
	recipientID pgtype.UUID,
	item inboxNotificationItem,
) error {
	issueID, err := scanUUID(*item.IssueID)
	if err != nil {
		return fmt.Errorf("parse issue_id: %w", err)
	}
	cardJSON, err := n.renderInboxIssueCard(ctx, workspaceID, recipientID, issueID, item)
	if err != nil {
		return fmt.Errorf("render inbox issue card: %w", err)
	}
	card, err := n.queries.GetLarkInboxIssueCard(ctx, db.GetLarkInboxIssueCardParams{
		WorkspaceID:    workspaceID,
		RecipientID:    recipientID,
		IssueID:        issueID,
		InstallationID: row.LarkInstallation.ID,
		LarkOpenID:     row.LarkUserBinding.LarkOpenID,
	})
	if err == nil && card.LarkCardMessageID != "" {
		if err := n.client.PatchInteractiveCard(ctx, PatchCardParams{
			InstallationID:    creds,
			LarkCardMessageID: card.LarkCardMessageID,
			CardJSON:          cardJSON,
		}); err != nil {
			return fmt.Errorf("patch inbox issue card: %w", err)
		}
		if err := n.queries.TouchLarkInboxIssueCard(ctx, card.ID); err != nil {
			return fmt.Errorf("touch inbox issue card: %w", err)
		}
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lookup inbox issue card: %w", err)
	}
	messageID, err := n.client.SendDirectInteractiveCard(ctx, SendDirectCardParams{
		InstallationID: creds,
		OpenID:         OpenID(row.LarkUserBinding.LarkOpenID),
		CardJSON:       cardJSON,
	})
	if err != nil {
		return fmt.Errorf("send inbox issue card: %w", err)
	}
	if _, err := n.queries.UpsertLarkInboxIssueCard(ctx, db.UpsertLarkInboxIssueCardParams{
		WorkspaceID:       workspaceID,
		RecipientID:       recipientID,
		IssueID:           issueID,
		InstallationID:    row.LarkInstallation.ID,
		LarkOpenID:        row.LarkUserBinding.LarkOpenID,
		LarkCardMessageID: messageID,
	}); err != nil {
		return fmt.Errorf("record inbox issue card: %w", err)
	}
	return nil
}

func (n *InboxNotifier) renderInboxIssueCard(ctx context.Context, workspaceID, recipientID, issueID pgtype.UUID, current inboxNotificationItem) (string, error) {
	rows, err := n.queries.ListLarkInboxIssueCardItems(ctx, db.ListLarkInboxIssueCardItemsParams{
		WorkspaceID: workspaceID,
		RecipientID: recipientID,
		IssueID:     issueID,
		Types:       mergeableLarkInboxNotificationTypes(),
	})
	if err != nil {
		return "", err
	}
	items := make([]inboxNotificationItem, 0, len(rows)+1)
	seen := map[string]bool{}
	for _, row := range rows {
		item := inboxNotificationItemFromInboxItem(row)
		items = append(items, item)
		seen[item.ID] = true
	}
	if !seen[current.ID] {
		items = append(items, current)
	}

	issue, workspace := n.inboxNotificationContext(ctx, workspaceID, current)
	identifier := inboxIssueIdentifier(issue, workspace)
	title := inboxNotificationHeaderTitle(identifier, current.Title)
	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"template": inboxIssueCardTemplate(items),
			"title": map[string]any{
				"tag":     "plain_text",
				"content": title,
			},
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": inboxIssueCardMarkdown(items),
				},
			},
		},
	}
	if issueURL := n.issueURL(workspace, current); issueURL != "" {
		card["elements"] = append(card["elements"].([]any),
			map[string]any{"tag": "hr"},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"text": map[string]any{"tag": "plain_text", "content": "在 Multica 中查看"},
						"url":  issueURL,
						"type": "primary",
					},
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

func (n *InboxNotifier) renderInboxNotificationCard(ctx context.Context, workspaceID pgtype.UUID, item inboxNotificationItem) (string, error) {
	issue, workspace := n.inboxNotificationContext(ctx, workspaceID, item)
	identifier := inboxIssueIdentifier(issue, workspace)
	headerTitle := inboxNotificationHeaderTitle(identifier, item.Title)
	bodyMD := inboxNotificationMarkdown(item)
	if bodyMD == "" {
		bodyMD = headerTitle
	}
	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": inboxNotificationTemplate(item),
			"title": map[string]any{
				"tag":     "plain_text",
				"content": headerTitle,
			},
		},
		"elements": []any{
			map[string]any{
				"tag": "div",
				"text": map[string]any{
					"tag":     "lark_md",
					"content": bodyMD,
				},
			},
		},
	}
	if issueURL := n.issueURL(workspace, item); issueURL != "" {
		card["elements"] = append(card["elements"].([]any),
			map[string]any{"tag": "hr"},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"text": map[string]any{"tag": "plain_text", "content": "在 Multica 中查看"},
						"url":  issueURL,
						"type": "primary",
					},
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

func mergeableLarkInboxNotificationTypes() []string {
	return []string{"quick_create_done", "status_changed"}
}

func inboxNotificationItemFromInboxItem(row db.InboxItem) inboxNotificationItem {
	item := inboxNotificationItem{
		ID:            uuidString(row.ID),
		WorkspaceID:   uuidString(row.WorkspaceID),
		RecipientType: row.RecipientType,
		RecipientID:   uuidString(row.RecipientID),
		Type:          row.Type,
		Severity:      row.Severity,
		Title:         row.Title,
		Details:       json.RawMessage(row.Details),
	}
	if row.IssueID.Valid {
		issueID := uuidString(row.IssueID)
		item.IssueID = &issueID
	}
	if row.Body.Valid {
		body := row.Body.String
		item.Body = &body
	}
	if row.ActorType.Valid {
		actorType := row.ActorType.String
		item.ActorType = &actorType
	}
	if row.ActorID.Valid {
		actorID := uuidString(row.ActorID)
		item.ActorID = &actorID
	}
	return item
}

func inboxIssueCardMarkdown(items []inboxNotificationItem) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		md := inboxIssueCardItemMarkdown(item)
		if md != "" {
			parts = append(parts, md)
		}
	}
	if len(parts) == 0 {
		return "Issue 有新动态"
	}
	return strings.Join(parts, "\n\n---\n\n")
}

func inboxIssueCardItemMarkdown(item inboxNotificationItem) string {
	body := ""
	if item.Body != nil {
		body = strings.TrimSpace(*item.Body)
	}
	switch item.Type {
	case "quick_create_done":
		return "**✅ Issue 已创建**"
	case "status_changed":
		from, to := inboxStatusChange(item)
		if from != "" || to != "" {
			return fmt.Sprintf("**🔄 状态已变更**\n\n`%s` → `%s`", statusLabelForInbox(from), statusLabelForInbox(to))
		}
		return "**🔄 状态已变更**"
	case "new_comment":
		if body == "" {
			return fmt.Sprintf("**💬 %s 评论**", inboxActorLabel(item))
		}
		return fmt.Sprintf("**💬 %s 评论**\n\n%s", inboxActorLabel(item), truncateRunes(body, 700))
	default:
		if body != "" {
			return truncateRunes(body, 700)
		}
		return strings.TrimSpace(item.Title)
	}
}

func inboxIssueCardTemplate(items []inboxNotificationItem) string {
	template := "blue"
	for _, item := range items {
		switch inboxNotificationTemplate(item) {
		case "red":
			return "red"
		case "yellow":
			template = "yellow"
		case "green":
			if template == "blue" {
				template = "green"
			}
		case "wathet":
			if template == "blue" {
				template = "wathet"
			}
		}
	}
	return template
}

func (n *InboxNotifier) inboxNotificationContext(ctx context.Context, workspaceID pgtype.UUID, item inboxNotificationItem) (*db.Issue, *db.Workspace) {
	var issue *db.Issue
	if item.IssueID != nil {
		if issueID, err := scanUUID(*item.IssueID); err == nil {
			if row, err := n.queries.GetIssue(ctx, issueID); err == nil {
				issue = &row
			}
		}
	}
	var workspace *db.Workspace
	if row, err := n.queries.GetWorkspace(ctx, workspaceID); err == nil {
		workspace = &row
	}
	return issue, workspace
}

func inboxIssueIdentifier(issue *db.Issue, workspace *db.Workspace) string {
	if issue == nil || issue.Number == 0 {
		return ""
	}
	if workspace != nil && strings.TrimSpace(workspace.IssuePrefix) != "" {
		return fmt.Sprintf("%s-%d", strings.TrimSpace(workspace.IssuePrefix), issue.Number)
	}
	return fmt.Sprintf("#%d", issue.Number)
}

func inboxNotificationHeaderTitle(identifier, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Inbox"
	}
	if identifier != "" {
		title = fmt.Sprintf("[%s] %s", identifier, title)
	}
	return truncateRunes(title, 80)
}

func inboxNotificationMarkdown(item inboxNotificationItem) string {
	body := ""
	if item.Body != nil {
		body = strings.TrimSpace(*item.Body)
	}
	switch item.Type {
	case "new_comment":
		if body == "" {
			return ""
		}
		return fmt.Sprintf("**💬 %s 评论**\n\n%s", inboxActorLabel(item), truncateRunes(body, 700))
	case "status_changed":
		from, to := inboxStatusChange(item)
		if from != "" || to != "" {
			return fmt.Sprintf("**🔄 状态已变更**\n\n`%s` → `%s`", statusLabelForInbox(from), statusLabelForInbox(to))
		}
		return "**🔄 状态已变更**"
	case "quick_create_failed", "task_failed":
		if body != "" {
			return fmt.Sprintf("**❌ 智能体任务失败**\n\n%s", truncateRunes(body, 700))
		}
		return "**❌ 智能体任务失败**"
	case "quick_create_done":
		return "**✅ Issue 已创建**"
	default:
		if body != "" {
			return truncateRunes(body, 700)
		}
		return ""
	}
}

func inboxNotificationTemplate(item inboxNotificationItem) string {
	switch item.Type {
	case "new_comment":
		return "wathet"
	case "quick_create_failed", "task_failed":
		return "red"
	case "quick_create_done":
		return "green"
	case "status_changed":
		_, to := inboxStatusChange(item)
		switch to {
		case "done":
			return "green"
		case "blocked", "cancelled":
			return "red"
		case "in_review":
			return "yellow"
		case "in_progress":
			return "blue"
		}
	}
	return "blue"
}

func inboxActorLabel(item inboxNotificationItem) string {
	if item.ActorType != nil && *item.ActorType == "agent" {
		return "Agent"
	}
	return "有人"
}

func inboxStatusChange(item inboxNotificationItem) (string, string) {
	var details struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if len(item.Details) == 0 {
		return "", ""
	}
	if err := json.Unmarshal(item.Details, &details); err != nil {
		return "", ""
	}
	return details.From, details.To
}

func statusLabelForInbox(s string) string {
	switch s {
	case "backlog":
		return "待规划"
	case "todo":
		return "待办"
	case "in_progress":
		return "处理中"
	case "in_review":
		return "待 review"
	case "blocked":
		return "阻塞"
	case "done":
		return "完成"
	case "cancelled":
		return "已取消"
	case "":
		return "未知"
	default:
		return s
	}
}

func (n *InboxNotifier) issueURL(workspace *db.Workspace, item inboxNotificationItem) string {
	if n.publicURL == "" || workspace == nil || workspace.Slug == "" || item.IssueID == nil || *item.IssueID == "" {
		return ""
	}
	return n.publicURL + "/" + url.PathEscape(workspace.Slug) + "/issues/" + url.PathEscape(*item.IssueID)
}

func scanUUID(s string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
