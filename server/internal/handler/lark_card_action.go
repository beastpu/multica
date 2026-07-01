package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) HandleLarkCardAction(ctx context.Context, msg lark.InboundMessage) error {
	if msg.CardAction == nil || msg.CardAction.IssueConfirmation == nil {
		return nil
	}
	return h.handleLarkIssueConfirmationAction(ctx, msg, *msg.CardAction.IssueConfirmation)
}

func (h *Handler) handleLarkIssueConfirmationAction(ctx context.Context, msg lark.InboundMessage, action lark.IssueConfirmationCardAction) error {
	content := strings.TrimSpace(action.Message)
	if content == "" {
		return nil
	}
	workspaceID, err := util.ParseUUID(action.WorkspaceID)
	if err != nil {
		return nil
	}
	issueID, err := util.ParseUUID(action.IssueID)
	if err != nil {
		return nil
	}
	parentCommentID, err := util.ParseUUID(action.ParentCommentID)
	if err != nil {
		return nil
	}
	recipientID, err := util.ParseUUID(action.RecipientID)
	if err != nil {
		return nil
	}
	if msg.AppID == "" || msg.MessageID == "" || msg.SenderOpenID == "" || action.AllowedOpenID == "" {
		return nil
	}
	if string(msg.SenderOpenID) != action.AllowedOpenID {
		return nil
	}

	store := lark.NewChannelStore(h.Queries)
	inst, err := store.GetLarkInstallationByAppID(ctx, msg.AppID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if inst.Status != string(lark.InstallationActive) || uuidToString(inst.WorkspaceID) != uuidToString(workspaceID) {
		return nil
	}
	binding, err := store.GetLarkUserBindingByOpenID(ctx, lark.GetUserBindingByOpenIDParams{
		InstallationID: inst.ID,
		ChannelUserID:  string(msg.SenderOpenID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if uuidToString(binding.WorkspaceID) != uuidToString(workspaceID) ||
		uuidToString(binding.MulticaUserID) != uuidToString(recipientID) {
		return nil
	}
	isMember, err := store.IsWorkspaceMember(ctx, inst.WorkspaceID, binding.MulticaUserID)
	if err != nil {
		return err
	}
	if !isMember {
		return nil
	}
	if h.TxStarter == nil {
		return errors.New("lark card action: tx starter not configured")
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	qtx := h.Queries.WithTx(tx)
	ltx := lark.NewChannelStore(qtx)
	claim, err := ltx.ClaimLarkInboundDedup(ctx, lark.ClaimInboundDedupParams{
		InstallationID: inst.ID,
		MessageID:      msg.MessageID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	markProcessed := func() error {
		rows, err := ltx.MarkLarkInboundDedupProcessed(ctx, lark.MarkInboundDedupProcessedParams{
			InstallationID: inst.ID,
			MessageID:      msg.MessageID,
			ClaimToken:     claim.ClaimToken,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return fmt.Errorf("lark card action: lost dedup claim for %s", msg.MessageID)
		}
		return nil
	}
	markAndCommit := func() error {
		if err := markProcessed(); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		committed = true
		return nil
	}

	issue, err := qtx.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID:          issueID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return markAndCommit()
		}
		return err
	}
	parentComment, err := qtx.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
		ID:          parentCommentID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return markAndCommit()
		}
		return err
	}
	if uuidToString(parentComment.IssueID) != uuidToString(issue.ID) || parentComment.AuthorType != "agent" {
		return markAndCommit()
	}

	var rootComment *db.Comment
	if root, err := qtx.GetThreadRoot(ctx, db.GetThreadRootParams{
		CommentID:   parentCommentID,
		WorkspaceID: workspaceID,
	}); err == nil {
		rootComment = &root
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	comment, err := qtx.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "member",
		AuthorID:    binding.MulticaUserID,
		Content:     content,
		Type:        "comment",
		ParentID:    parentCommentID,
	})
	if err != nil {
		return err
	}
	if err := markAndCommit(); err != nil {
		return err
	}

	h.patchLarkIssueConfirmationCard(ctx, inst, binding, issue, msg.CardAction, action, parentComment.Content)

	resp := commentToResponse(comment, nil, nil)
	actorID := uuidToString(binding.MulticaUserID)
	if h.Bus != nil {
		h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", actorID, map[string]any{
			"comment":             resp,
			"issue_title":         issue.Title,
			"issue_assignee_type": textToPtr(issue.AssigneeType),
			"issue_assignee_id":   uuidToPtr(issue.AssigneeID),
			"issue_status":        issue.Status,
		})
	}
	if h.TaskService != nil {
		h.TaskService.AutoUnresolveThreadOnReply(ctx, rootComment, uuidToString(issue.WorkspaceID), "member", actorID)
		h.triggerTasksForComment(ctx, issue, comment, &parentComment, "member", actorID, nil)
	}
	slog.Info("lark card action: issue confirmation created comment",
		"workspace_id", uuidToString(issue.WorkspaceID),
		"issue_id", uuidToString(issue.ID),
		"parent_comment_id", uuidToString(parentComment.ID),
		"comment_id", uuidToString(comment.ID),
		"lark_message_id", msg.MessageID)
	return nil
}

func (h *Handler) patchLarkIssueConfirmationCard(ctx context.Context, inst lark.Installation, binding lark.UserBinding, issue db.Issue, cardAction *lark.InboundCardAction, action lark.IssueConfirmationCardAction, prompt string) {
	if h.LarkAPIClient == nil || h.LarkInstallations == nil || !h.LarkAPIClient.IsConfigured() {
		return
	}
	cardMessageID := ""
	if cardAction != nil {
		cardMessageID = strings.TrimSpace(cardAction.CardMessageID)
	}
	if cardMessageID == "" {
		card, err := lark.NewChannelStore(h.Queries).GetLarkInboxIssueCard(ctx, lark.GetInboxIssueCardParams{
			WorkspaceID:    issue.WorkspaceID,
			RecipientID:    binding.MulticaUserID,
			IssueID:        issue.ID,
			InstallationID: inst.ID,
			ChannelUserID:  binding.ChannelUserID,
		})
		if err == nil {
			cardMessageID = strings.TrimSpace(card.ChannelCardMessageID)
			if cardMessageID != "" {
				slog.Info("lark card action: using recorded issue card message id",
					"workspace_id", uuidToString(issue.WorkspaceID),
					"issue_id", uuidToString(issue.ID),
					"installation_id", uuidToString(inst.ID))
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("lark card action: lookup issue card for patch failed",
				"workspace_id", uuidToString(issue.WorkspaceID),
				"issue_id", uuidToString(issue.ID),
				"installation_id", uuidToString(inst.ID),
				"error", err)
		}
	}
	if cardMessageID == "" {
		slog.Warn("lark card action: skip card patch because card message id is empty",
			"workspace_id", uuidToString(issue.WorkspaceID),
			"issue_id", uuidToString(issue.ID),
			"installation_id", uuidToString(inst.ID),
			"parent_comment_id", action.ParentCommentID)
		return
	}
	secret, err := h.LarkInstallations.DecryptAppSecret(inst)
	if err != nil {
		slog.Warn("lark card action: decrypt app_secret for card patch failed",
			"installation_id", uuidToString(inst.ID),
			"error", err)
		return
	}
	creds := lark.InstallationCredentials{
		AppID:     inst.AppID,
		AppSecret: secret,
		Region:    lark.RegionOrDefault(inst.Region),
	}
	if inst.TenantKey.Valid {
		creds.TenantKey = inst.TenantKey.String
	}
	cardJSON, err := lark.RenderIssueConfirmationResolvedCard(prompt, action)
	if err != nil {
		slog.Warn("lark card action: render resolved card failed",
			"installation_id", uuidToString(inst.ID),
			"card_message_id", cardMessageID,
			"error", err)
		return
	}
	if err := h.LarkAPIClient.PatchInteractiveCard(ctx, lark.PatchCardParams{
		InstallationID:    creds,
		LarkCardMessageID: cardMessageID,
		CardJSON:          cardJSON,
	}); err != nil {
		slog.Warn("lark card action: patch resolved card failed",
			"installation_id", uuidToString(inst.ID),
			"card_message_id", cardMessageID,
			"error", err)
		return
	}
	slog.Info("lark card action: patched resolved card",
		"workspace_id", uuidToString(issue.WorkspaceID),
		"issue_id", uuidToString(issue.ID),
		"installation_id", uuidToString(inst.ID),
		"card_message_id", cardMessageID)
}

var _ lark.CardActionHandler = (*Handler)(nil)
