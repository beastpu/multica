-- chat_ask: a structured "the agent needs the user" request inside a chat
-- session (docs/chat-ask-structured-signal-spec.md). Replaces text-heuristic
-- confirmation-card detection: the agent declares the interaction type via
-- `multica chat ask`, channels render UI from this row, never from prose.
CREATE TABLE chat_ask (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_session_id UUID NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    task_id UUID NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('confirm', 'choice', 'input')),
    message TEXT NOT NULL,
    -- action: what will be executed upon approval. Required for type=confirm
    -- (enforced in the handler; the CHECK keeps the invariant honest at the
    -- storage layer too).
    action TEXT NOT NULL DEFAULT '',
    options JSONB NOT NULL DEFAULT '[]'::jsonb,
    hint TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'answered', 'expired', 'superseded')),
    answer JSONB,
    answered_by TEXT NOT NULL DEFAULT '',
    answered_at TIMESTAMPTZ,
    -- channel_message_id: the IM message hosting the rendered card, persisted
    -- so supersede/expire can PATCH the card into a resolved state.
    channel_message_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT chat_ask_confirm_requires_action CHECK (type <> 'confirm' OR action <> '')
);

-- At most one pending ask per session: a new ask must supersede the old one
-- in the same transaction. DB-enforced so concurrent creates cannot race
-- into two live button cards.
CREATE UNIQUE INDEX idx_chat_ask_pending_per_session ON chat_ask(chat_session_id) WHERE status = 'pending';

CREATE INDEX idx_chat_ask_session ON chat_ask(chat_session_id, created_at);
