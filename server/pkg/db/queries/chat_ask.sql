-- name: SupersedePendingChatAsks :many
-- A new ask replaces whatever is pending in the session. Run in the same
-- transaction as CreateChatAsk so the pending-per-session unique index can
-- never trip under concurrency. Returns the retired rows so the caller can
-- emit EventChatAskResolved and the channel can patch their cards.
UPDATE chat_ask SET status = 'superseded'
WHERE chat_session_id = $1 AND status = 'pending'
RETURNING *;

-- name: CreateChatAsk :one
INSERT INTO chat_ask (chat_session_id, task_id, type, message, action, options, hint, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetPendingChatAsk :one
SELECT * FROM chat_ask
WHERE chat_session_id = $1 AND status = 'pending';

-- name: GetChatAsk :one
SELECT * FROM chat_ask
WHERE id = $1;

-- name: UpdateChatAskChannelMessage :exec
-- Records the IM message that hosts the rendered card so supersede / answer
-- can patch that card into its receipt form later.
UPDATE chat_ask SET channel_message_id = $2
WHERE id = $1;

-- name: AnswerChatAsk :one
-- Conditional transition pending -> answered: repeated clicks / replayed
-- callbacks match zero rows and change nothing (idempotency lives here, not
-- in the caller).
UPDATE chat_ask
SET status = 'answered', answer = $2, answered_by = $3, answered_at = now()
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: ExpireChatAsk :one
-- Lazy expiry: flipped on the first interaction with an ask whose TTL has
-- passed (no background sweeper). Conditional so a concurrent answer wins.
UPDATE chat_ask
SET status = 'expired'
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: AnswerPendingChatAskBySession :one
-- Text preemption: while an ask is pending, the next user message in the
-- session resolves it regardless of content (the pending ask is a one-shot
-- nonce — any later utterance invalidates the stale authorization).
UPDATE chat_ask
SET status = 'answered', answer = $2, answered_by = $3, answered_at = now()
WHERE chat_session_id = $1 AND status = 'pending'
RETURNING *;
