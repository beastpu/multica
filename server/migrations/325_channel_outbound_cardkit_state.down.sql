ALTER TABLE channel_outbound_card_message
    DROP COLUMN IF EXISTS channel_card_id,
    DROP COLUMN IF EXISTS transport,
    DROP COLUMN IF EXISTS operation_sequence,
    DROP COLUMN IF EXISTS visible_text,
    DROP COLUMN IF EXISTS streaming_closed_at,
    DROP COLUMN IF EXISTS terminal_content,
    DROP COLUMN IF EXISTS lease_token,
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS attempt_count,
    DROP COLUMN IF EXISTS last_error;
