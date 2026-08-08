-- The outbound card is now one message that is sent, repainted with elapsed
-- time, and replaced in place by the final answer. Delivery is idempotent —
-- patching the same message id with the same body is the same work twice — so
-- the revision ledger, delivery lease, retry ledger and CardKit transport state
-- that migration 251 added no longer have anything to protect.
ALTER TABLE channel_outbound_card_message
    DROP COLUMN IF EXISTS channel_card_id,
    DROP COLUMN IF EXISTS transport,
    DROP COLUMN IF EXISTS desired_revision,
    DROP COLUMN IF EXISTS applied_revision,
    DROP COLUMN IF EXISTS inflight_revision,
    DROP COLUMN IF EXISTS inflight_sequence,
    DROP COLUMN IF EXISTS inflight_card_json,
    DROP COLUMN IF EXISTS operation_sequence,
    DROP COLUMN IF EXISTS projected_seq,
    DROP COLUMN IF EXISTS visible_text,
    DROP COLUMN IF EXISTS current_stage,
    DROP COLUMN IF EXISTS files_read_count,
    DROP COLUMN IF EXISTS files_edited_count,
    DROP COLUMN IF EXISTS searches_count,
    DROP COLUMN IF EXISTS commands_count,
    DROP COLUMN IF EXISTS terminal_content,
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS lease_token,
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS attempt_count,
    DROP COLUMN IF EXISTS delivery_failed_at,
    DROP COLUMN IF EXISTS last_error;
