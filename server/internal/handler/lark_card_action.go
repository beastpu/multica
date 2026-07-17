package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/integrations/lark"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) HandleLarkCardAction(ctx context.Context, msg lark.InboundMessage) (lark.DispatchResult, error) {
	if msg.CardAction == nil {
		return lark.DispatchResult{}, nil
	}
	if msg.CardAction.ChatAsk != nil {
		return h.handleLarkChatAskAction(ctx, msg, *msg.CardAction.ChatAsk)
	}
	if msg.CardAction.IssueConfirmation != nil {
		return h.handleLarkIssueConfirmationAction(ctx, msg, *msg.CardAction.IssueConfirmation)
	}
	return lark.DispatchResult{}, nil
}

// handleLarkChatAskAction validates a structured-ask click against the stored
// chat_ask row and drives its state machine. Verdicts:
//   - pending & fresh  → answered; receipt ACK; dispatch the answer text.
//   - pending & expired → expired; expiry receipt; no dispatch.
//   - answered by THIS click (Lark redelivery) → same receipt; dispatch again
//     (the chat pipeline's dedup absorbs the duplicate).
//   - any other terminal state → state receipt; no dispatch.
func (h *Handler) handleLarkChatAskAction(ctx context.Context, msg lark.InboundMessage, action lark.ChatAskCardAction) (lark.DispatchResult, error) {
	askID, err := util.ParseUUID(action.AskID)
	if err != nil {
		return lark.DispatchResult{}, nil
	}
	if msg.SenderOpenID == "" || action.AllowedOpenID == "" || string(msg.SenderOpenID) != action.AllowedOpenID {
		return lark.DispatchResult{}, nil
	}

	answer := chatAskClickAnswer{
		Text:      strings.TrimSpace(action.Reply),
		Choice:    action.Choice,
		MessageID: msg.MessageID,
		Via:       "click",
	}
	if answer.Text == "" {
		return lark.DispatchResult{}, nil
	}
	answerJSON, err := json.Marshal(answer)
	if err != nil {
		return lark.DispatchResult{}, err
	}

	now := time.Now()
	if now.Unix() > action.ExpiresAtUnix {
		expired, err := h.Queries.ExpireChatAsk(ctx, askID)
		switch {
		case err == nil:
			h.publishChatAskResolved(ctx, expired)
			return chatAskAckResult(expired, ""), nil
		case errors.Is(err, pgx.ErrNoRows):
			return h.chatAskStateReceipt(ctx, askID, msg.MessageID)
		default:
			return lark.DispatchResult{}, err
		}
	}

	answered, err := h.Queries.AnswerChatAsk(ctx, db.AnswerChatAskParams{
		ID:         askID,
		Answer:     answerJSON,
		AnsweredBy: string(msg.SenderOpenID),
	})
	switch {
	case err == nil:
		h.publishChatAskResolved(ctx, answered)
		res := chatAskAckResult(answered, answer.Text)
		res.DispatchAsChatText = true
		return res, nil
	case errors.Is(err, pgx.ErrNoRows):
		// Not pending anymore: a redelivered click that already answered it
		// re-dispatches (idempotent downstream); anything else is stale.
		return h.chatAskStateReceipt(ctx, askID, msg.MessageID)
	default:
		return lark.DispatchResult{}, err
	}
}

type chatAskClickAnswer struct {
	Text      string `json:"text"`
	Choice    string `json:"choice,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Via       string `json:"via,omitempty"`
}

// chatAskStateReceipt ACKs a click on an ask that is no longer pending with
// a receipt matching its current state. A Lark redelivery of the very click
// that answered the ask (same synthetic message id) also re-dispatches the
// answer text, in case the first dispatch attempt died before the ACK.
func (h *Handler) chatAskStateReceipt(ctx context.Context, askID pgtype.UUID, clickMessageID string) (lark.DispatchResult, error) {
	ask, err := h.Queries.GetChatAsk(ctx, askID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return lark.DispatchResult{}, nil
		}
		return lark.DispatchResult{}, err
	}
	var answer chatAskClickAnswer
	if len(ask.Answer) > 0 {
		_ = json.Unmarshal(ask.Answer, &answer)
	}
	res := chatAskAckResult(ask, answer.Text)
	if ask.Status == "answered" && answer.MessageID != "" && answer.MessageID == clickMessageID {
		res.DispatchAsChatText = true
	}
	return res, nil
}

// chatAskAckResult shapes the card.action.trigger ACK: the clicked card is
// replaced with the receipt for the ask's (new) state.
func chatAskAckResult(ask db.ChatAsk, answerText string) lark.DispatchResult {
	ackJSON, err := lark.RenderChatAskCardActionResponse(protocol.ChatAskResolvedPayload{
		AskID:         uuidToString(ask.ID),
		ChatSessionID: uuidToString(ask.ChatSessionID),
		TaskID:        uuidToString(ask.TaskID),
		Status:        ask.Status,
		Message:       ask.Message,
		AnswerText:    answerText,
	})
	if err != nil {
		slog.Warn("lark chat ask: render ack receipt failed", "ask_id", uuidToString(ask.ID), "error", err)
		ackJSON = ""
	}
	return lark.DispatchResult{CardActionResponseJSON: ackJSON}
}

// publishChatAskResolved emits EventChatAskResolved so the Patcher also
// PATCHes the standing card (belt to the ACK's suspenders: the ACK only
// reaches the clicking client's view; the PATCH rewrites the message for
// everyone).
func (h *Handler) publishChatAskResolved(ctx context.Context, ask db.ChatAsk) {
	if h.Bus == nil {
		return
	}
	workspaceID := ""
	if session, err := h.Queries.GetChatSession(ctx, ask.ChatSessionID); err == nil {
		workspaceID = uuidToString(session.WorkspaceID)
	}
	h.Bus.Publish(events.Event{
		Type:          protocol.EventChatAskResolved,
		WorkspaceID:   workspaceID,
		ActorType:     "member",
		TaskID:        uuidToString(ask.TaskID),
		ChatSessionID: uuidToString(ask.ChatSessionID),
		Payload:       chatAskResolvedPayload(ask),
	})
}

func (h *Handler) handleLarkIssueConfirmationAction(ctx context.Context, msg lark.InboundMessage, action lark.IssueConfirmationCardAction) (lark.DispatchResult, error) {
	content := strings.TrimSpace(action.Message)
	if content == "" {
		return lark.DispatchResult{}, nil
	}
	workspaceID, err := util.ParseUUID(action.WorkspaceID)
	if err != nil {
		return lark.DispatchResult{}, nil
	}
	issueID, err := util.ParseUUID(action.IssueID)
	if err != nil {
		return lark.DispatchResult{}, nil
	}
	parentCommentID, err := util.ParseUUID(action.ParentCommentID)
	if err != nil {
		return lark.DispatchResult{}, nil
	}
	recipientID, err := util.ParseUUID(action.RecipientID)
	if err != nil {
		return lark.DispatchResult{}, nil
	}
	if msg.AppID == "" || msg.MessageID == "" || msg.SenderOpenID == "" || action.AllowedOpenID == "" {
		return lark.DispatchResult{}, nil
	}
	if string(msg.SenderOpenID) != action.AllowedOpenID {
		return lark.DispatchResult{}, nil
	}

	store := lark.NewChannelStore(h.Queries)
	inst, err := store.GetLarkInstallationByAppID(ctx, msg.AppID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return lark.DispatchResult{}, nil
		}
		return lark.DispatchResult{}, err
	}
	if inst.Status != string(lark.InstallationActive) || uuidToString(inst.WorkspaceID) != uuidToString(workspaceID) {
		return lark.DispatchResult{}, nil
	}
	binding, err := store.GetLarkUserBindingByOpenID(ctx, lark.GetUserBindingByOpenIDParams{
		InstallationID: inst.ID,
		ChannelUserID:  string(msg.SenderOpenID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return lark.DispatchResult{}, nil
		}
		return lark.DispatchResult{}, err
	}
	if uuidToString(binding.WorkspaceID) != uuidToString(workspaceID) ||
		uuidToString(binding.MulticaUserID) != uuidToString(recipientID) {
		return lark.DispatchResult{}, nil
	}
	isMember, err := store.IsWorkspaceMember(ctx, inst.WorkspaceID, binding.MulticaUserID)
	if err != nil {
		return lark.DispatchResult{}, err
	}
	if !isMember {
		return lark.DispatchResult{}, nil
	}
	if h.TxStarter == nil {
		return lark.DispatchResult{}, errors.New("lark card action: tx starter not configured")
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return lark.DispatchResult{}, err
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
			return lark.DispatchResult{}, nil
		}
		return lark.DispatchResult{}, err
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
			return lark.DispatchResult{}, markAndCommit()
		}
		return lark.DispatchResult{}, err
	}
	parentComment, err := qtx.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
		ID:          parentCommentID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return lark.DispatchResult{}, markAndCommit()
		}
		return lark.DispatchResult{}, err
	}
	if uuidToString(parentComment.IssueID) != uuidToString(issue.ID) || parentComment.AuthorType != "agent" {
		return lark.DispatchResult{}, markAndCommit()
	}

	var rootComment *db.Comment
	if root, err := qtx.GetThreadRoot(ctx, db.GetThreadRootParams{
		CommentID:   parentCommentID,
		WorkspaceID: workspaceID,
	}); err == nil {
		rootComment = &root
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return lark.DispatchResult{}, err
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
		return lark.DispatchResult{}, err
	}
	if err := markAndCommit(); err != nil {
		return lark.DispatchResult{}, err
	}

	cardActionResponseJSON, err := lark.RenderIssueConfirmationCardActionResponse(parentComment.Content, action)
	if err != nil {
		slog.Warn("lark card action: render callback response failed",
			"workspace_id", uuidToString(issue.WorkspaceID),
			"issue_id", uuidToString(issue.ID),
			"parent_comment_id", uuidToString(parentComment.ID),
			"error", err)
	}

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
		h.triggerTasksForComment(ctx, issue, comment, &parentComment, "member", actorID, actorID, "", nil)
	}
	slog.Info("lark card action: issue confirmation created comment",
		"workspace_id", uuidToString(issue.WorkspaceID),
		"issue_id", uuidToString(issue.ID),
		"parent_comment_id", uuidToString(parentComment.ID),
		"comment_id", uuidToString(comment.ID),
		"lark_message_id", msg.MessageID)
	return lark.DispatchResult{CardActionResponseJSON: cardActionResponseJSON}, nil
}

var _ lark.CardActionHandler = (*Handler)(nil)
