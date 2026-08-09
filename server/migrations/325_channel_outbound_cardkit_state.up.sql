-- CardKit streaming state for the outbound reply card.
--
-- The card is now a CardKit entity that streams its text, rather than a message
-- patched in place. That buys the native typewriter render, no "edited" badge,
-- and 10 ops/sec per card instead of the message endpoint's 50/min — at the
-- cost of the state below.
--
-- operation_sequence is the reason most of this exists: every CardKit call
-- against one entity (element content, settings, full update) shares a single
-- counter that must strictly increase. With more than one server replica that
-- counter has to be handed out by the database, and the lease columns are what
-- stop two replicas from painting the same card out of order.
ALTER TABLE channel_outbound_card_message
    -- The CardKit entity id, distinct from the IM message that carries it.
    ADD COLUMN channel_card_id TEXT NOT NULL DEFAULT '',
    -- 'legacy' is the fallback for apps that lack the cardkit:card:write scope.
    ADD COLUMN transport TEXT NOT NULL DEFAULT 'cardkit'
        CHECK (transport IN ('cardkit', 'legacy')),
    ADD COLUMN operation_sequence INTEGER NOT NULL DEFAULT 0,
    -- The text last put on the card, so a paint that would change nothing is
    -- skipped and the terminal render knows what the user already sees.
    ADD COLUMN visible_text TEXT NOT NULL DEFAULT '',
    -- Streaming mode blocks callback-driven updates, so it must be closed
    -- before a card grows interactive controls. Recorded to keep that
    -- close-once, and because Feishu auto-closes it after ten minutes.
    ADD COLUMN streaming_closed_at TIMESTAMPTZ,
    -- The terminal answer, parked here by the event handler. Closing streaming
    -- and rendering the final card are both sequenced operations, so they have
    -- to run under the worker's lease rather than inline on the bus thread.
    ADD COLUMN terminal_content TEXT NOT NULL DEFAULT '',
    ADD COLUMN lease_token UUID,
    ADD COLUMN lease_expires_at TIMESTAMPTZ,
    ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
