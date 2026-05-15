package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const externalNotificationCoalesceDelay = 5 * time.Second

func registerExternalNotificationListeners(bus *events.Bus, queries *db.Queries, notifiers ...externalInboxNotifier) {
	if len(notifiers) == 0 {
		return
	}

	bus.Subscribe(protocol.EventInboxNew, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		item, ok := payload["item"].(map[string]any)
		if !ok {
			return
		}

		recipientType := eventString(item["recipient_type"])
		if recipientType != "member" {
			return
		}
		recipientID := eventString(item["recipient_id"])
		workspaceID := eventString(item["workspace_id"])
		if recipientID == "" || workspaceID == "" {
			return
		}

		itemID := eventString(item["id"])
		notificationType := eventString(item["type"])
		title := eventString(item["title"])
		body := eventString(item["body"])
		issueID := eventString(item["issue_id"])
		if itemID == "" {
			return
		}

		dispatchExternalInboxEvent(queries, notifiers, service.FeishuInboxNotification{
			IssueID: issueID,
			Type:    notificationType,
			Title:   title,
			Body:    body,
		}, workspaceID, recipientID, itemID)
	})
}

func eventString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case *string:
		if v == nil {
			return ""
		}
		return *v
	default:
		return ""
	}
}

func dispatchExternalInboxEvent(
	queries *db.Queries,
	notifiers []externalInboxNotifier,
	notif service.FeishuInboxNotification,
	workspaceID string,
	recipientID string,
	inboxItemID string,
) {
	go func() {
		time.Sleep(externalNotificationCoalesceDelay)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if !isLatestVisibleInboxItem(ctx, queries, workspaceID, recipientID, inboxItemID, notif.IssueID) {
			return
		}

		prefs := loadUserPrefs(ctx, queries, workspaceID, []string{recipientID})
		if prefs[recipientID]["feishu_notifications"] != "all" {
			return
		}

		userID := parseUUID(recipientID)
		user, err := queries.GetUser(ctx, userID)
		if err != nil {
			slog.Warn("external notification: failed to load recipient user", "recipient_id", recipientID, "error", err)
			return
		}
		workspaceUUID := parseUUID(workspaceID)
		workspace, err := queries.GetWorkspace(ctx, workspaceUUID)
		if err != nil {
			slog.Warn("external notification: failed to load workspace", "workspace_id", workspaceID, "error", err)
			return
		}

		notif.RecipientEmail = user.Email
		notif.WorkspaceName = workspace.Name
		notif.WorkspaceSlug = workspace.Slug

		for _, notifier := range notifiers {
			if notifier == nil {
				continue
			}
			if err := notifier.SendInboxNotification(ctx, notif); err != nil {
				slog.Warn("external notification send failed", "provider", "feishu", "recipient_id", recipientID, "error", err)
			}
		}
	}()
}

func isLatestVisibleInboxItem(ctx context.Context, queries *db.Queries, workspaceID, recipientID, inboxItemID, issueID string) bool {
	items, err := queries.ListInboxItems(ctx, db.ListInboxItemsParams{
		WorkspaceID:   parseUUID(workspaceID),
		RecipientType: "member",
		RecipientID:   parseUUID(recipientID),
	})
	if err != nil {
		slog.Warn("external notification: failed to list inbox items for coalescing", "recipient_id", recipientID, "error", err)
		return true
	}

	groupKey := issueID
	if groupKey == "" {
		groupKey = inboxItemID
	}
	for _, item := range items {
		itemID := util.UUIDToString(item.ID)
		itemGroupKey := util.UUIDToString(item.IssueID)
		if itemGroupKey == "" {
			itemGroupKey = itemID
		}
		if itemGroupKey == groupKey {
			return itemID == inboxItemID
		}
	}
	return true
}

func dispatchExternalInboxNotification(queries *db.Queries, notifiers []externalInboxNotifier, item db.InboxItem) {
	if len(notifiers) == 0 || item.RecipientType != "member" {
		return
	}

	dispatchExternalInboxEvent(queries, notifiers, service.FeishuInboxNotification{
		IssueID: util.UUIDToString(item.IssueID),
		Type:    item.Type,
		Title:   item.Title,
		Body:    item.Body.String,
	}, util.UUIDToString(item.WorkspaceID), util.UUIDToString(item.RecipientID), util.UUIDToString(item.ID))
}
