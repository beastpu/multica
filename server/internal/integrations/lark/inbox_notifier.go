package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const inboxNotifyTimeout = 10 * time.Second

type InboxNotifierQueries interface {
	GetIssue(ctx context.Context, id pgtype.UUID) (db.Issue, error)
	ListActiveLarkUserBindingsByMember(ctx context.Context, arg db.ListActiveLarkUserBindingsByMemberParams) ([]db.ListActiveLarkUserBindingsByMemberRow, error)
}

type InboxNotifier struct {
	queries     InboxNotifierQueries
	credentials CredentialsResolver
	client      APIClient
	log         *slog.Logger
}

type InboxNotifierConfig struct {
	Logger *slog.Logger
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
	creds, err := n.installationCredentials(row.LarkInstallation)
	if err != nil {
		return err
	}
	if _, err := n.client.SendDirectTextMessage(ctx, SendDirectTextParams{
		InstallationID: creds,
		OpenID:         OpenID(row.LarkUserBinding.LarkOpenID),
		Text:           inboxNotificationText(item),
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

func selectInboxNotificationBindingByAgent(rows []db.ListActiveLarkUserBindingsByMemberRow, agentID pgtype.UUID) (db.ListActiveLarkUserBindingsByMemberRow, bool) {
	for _, row := range rows {
		if row.LarkInstallation.AgentID == agentID {
			return row, true
		}
	}
	return db.ListActiveLarkUserBindingsByMemberRow{}, false
}

func inboxNotificationText(item inboxNotificationItem) string {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "New inbox item"
	}
	var b strings.Builder
	b.WriteString("Inbox: ")
	b.WriteString(title)
	if item.Body != nil {
		body := strings.TrimSpace(*item.Body)
		if body != "" {
			b.WriteString("\n")
			b.WriteString(body)
		}
	}
	b.WriteString("\n\nReply here to continue with the agent.")
	return truncateRunes(b.String(), 2000)
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
