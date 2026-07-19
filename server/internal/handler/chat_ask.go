package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Chat ask: the structured "the agent needs the user" signal
// (docs/chat-ask-structured-signal-spec.md). The agent declares the
// interaction type via `multica chat ask`; channels render UI from the stored
// row instead of guessing intent from the reply text.
const (
	chatAskTTL = 30 * time.Minute

	maxChatAskMessageRunes = 2000
	maxChatAskActionRunes  = 600
	maxChatAskHintRunes    = 600
	maxChatAskOptionRunes  = 120
	minChatAskOptions      = 2
	maxChatAskOptions      = 6
)

type chatAskRequest struct {
	Type    string   `json:"type"`
	Message string   `json:"message"`
	Action  string   `json:"action"`
	Options []string `json:"options"`
	Hint    string   `json:"hint"`
}

type chatAskResponse struct {
	Ask chatAskPayload `json:"ask"`
}

type chatAskPayload struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	Action    string    `json:"action,omitempty"`
	Options   []string  `json:"options,omitempty"`
	Hint      string    `json:"hint,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PostChatAsk serves `multica chat ask`: validates the declared interaction,
// retires any pending ask in the session (at most one live ask — enforced by
// the partial unique index), and stores the new one as pending. After commit
// it publishes EventChatAsk so the session's channel renders the interaction,
// and EventChatAskResolved for each retired ask so its card gets patched.
func (h *Handler) PostChatAsk(w http.ResponseWriter, r *http.Request) {
	session, task, ok := h.taskChatSession(w, r)
	if !ok {
		return
	}
	var req chatAskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	req.Message = strings.TrimSpace(req.Message)
	req.Action = strings.TrimSpace(req.Action)
	req.Hint = strings.TrimSpace(req.Hint)
	for i := range req.Options {
		req.Options[i] = strings.TrimSpace(req.Options[i])
	}
	if msg, invalid := validateChatAsk(req); invalid {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	optionsJSON, err := json.Marshal(req.Options)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid options")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create ask")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	superseded, err := qtx.SupersedePendingChatAsks(r.Context(), session.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create ask")
		return
	}
	ask, err := qtx.CreateChatAsk(r.Context(), db.CreateChatAskParams{
		ChatSessionID: session.ID,
		TaskID:        task.ID,
		Type:          req.Type,
		Message:       req.Message,
		Action:        req.Action,
		Options:       optionsJSON,
		Hint:          req.Hint,
		ExpiresAt:     pgtype.Timestamptz{Time: time.Now().Add(chatAskTTL), Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create ask")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create ask")
		return
	}

	if h.Bus != nil {
		workspaceID := uuidToString(session.WorkspaceID)
		sessionIDStr := uuidToString(session.ID)
		for _, old := range superseded {
			h.Bus.Publish(events.Event{
				Type:          protocol.EventChatAskResolved,
				WorkspaceID:   workspaceID,
				ActorType:     "system",
				TaskID:        uuidToString(old.TaskID),
				ChatSessionID: sessionIDStr,
				Payload:       chatAskResolvedPayload(old),
			})
		}
		h.Bus.Publish(events.Event{
			Type:          protocol.EventChatAsk,
			WorkspaceID:   workspaceID,
			ActorType:     "agent",
			TaskID:        uuidToString(task.ID),
			ChatSessionID: sessionIDStr,
			Payload: protocol.ChatAskPayload{
				AskID:         uuidToString(ask.ID),
				ChatSessionID: sessionIDStr,
				TaskID:        uuidToString(task.ID),
				Type:          ask.Type,
				Message:       ask.Message,
				Action:        ask.Action,
				Options:       req.Options,
				Hint:          ask.Hint,
				ExpiresAtUnix: ask.ExpiresAt.Time.Unix(),
			},
		})
	}

	writeJSON(w, http.StatusCreated, chatAskResponse{Ask: chatAskPayload{
		ID:        uuidToString(ask.ID),
		Type:      ask.Type,
		Message:   ask.Message,
		Action:    ask.Action,
		Options:   req.Options,
		Hint:      ask.Hint,
		Status:    ask.Status,
		CreatedAt: ask.CreatedAt.Time,
		ExpiresAt: ask.ExpiresAt.Time,
	}})
}

// chatAskResolvedPayload shapes the EventChatAskResolved payload from a
// resolved chat_ask row (superseded on replacement, answered on text
// preemption or click).
func chatAskResolvedPayload(ask db.ChatAsk) protocol.ChatAskResolvedPayload {
	answerText := ""
	if len(ask.Answer) > 0 {
		var answer struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(ask.Answer, &answer); err == nil {
			answerText = answer.Text
		}
	}
	return protocol.ChatAskResolvedPayload{
		AskID:            uuidToString(ask.ID),
		ChatSessionID:    uuidToString(ask.ChatSessionID),
		TaskID:           uuidToString(ask.TaskID),
		Status:           ask.Status,
		Message:          ask.Message,
		AnswerText:       answerText,
		ChannelMessageID: ask.ChannelMessageID,
	}
}

// ResolveAskOnUserMessage implements the engine's AskResolver: while an ask
// is pending, the next durably-appended user message in the session resolves
// it regardless of content (one-shot nonce semantics — a stale confirm must
// not stay clickable after the conversation moved on). Click-originated
// answers pass through here too and no-op: their ask is already answered.
func (h *Handler) ResolveAskOnUserMessage(ctx context.Context, sessionID pgtype.UUID, senderUserID pgtype.UUID, text string) {
	answerJSON, err := json.Marshal(chatAskClickAnswer{
		Text: strings.TrimSpace(text),
		Via:  "text",
	})
	if err != nil {
		return
	}
	ask, err := h.Queries.AnswerPendingChatAskBySession(ctx, db.AnswerPendingChatAskBySessionParams{
		ChatSessionID: sessionID,
		Answer:        answerJSON,
		AnsweredBy:    uuidToString(senderUserID),
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("chat ask: text preemption failed",
				"chat_session_id", uuidToString(sessionID), "error", err)
		}
		return
	}
	h.publishChatAskResolved(ctx, ask)
}

// validateChatAsk enforces the per-type contract from the spec. confirm MUST
// carry the action to execute upon approval (an agent that cannot state the
// action is missing information and must use input instead); choice needs a
// bounded option set; input takes free text and never renders buttons.
func validateChatAsk(req chatAskRequest) (string, bool) {
	if req.Message == "" {
		return "message is required", true
	}
	if len([]rune(req.Message)) > maxChatAskMessageRunes {
		return "message is too long", true
	}
	if len([]rune(req.Action)) > maxChatAskActionRunes {
		return "action is too long", true
	}
	if len([]rune(req.Hint)) > maxChatAskHintRunes {
		return "hint is too long", true
	}
	switch req.Type {
	case "confirm":
		if req.Action == "" {
			return "confirm requires the action to execute upon approval; if information is missing, use --type input", true
		}
		if len(req.Options) > 0 {
			return "confirm does not take options", true
		}
	case "choice":
		if req.Action != "" {
			return "choice does not take an action", true
		}
		if len(req.Options) < minChatAskOptions || len(req.Options) > maxChatAskOptions {
			return "choice requires 2-6 options", true
		}
		for _, opt := range req.Options {
			if opt == "" {
				return "options must not be blank", true
			}
			if len([]rune(opt)) > maxChatAskOptionRunes {
				return "option is too long", true
			}
		}
	case "input":
		if req.Action != "" || len(req.Options) > 0 {
			return "input takes neither action nor options", true
		}
	default:
		return "type must be one of: confirm, choice, input", true
	}
	return "", false
}
