CREATE INDEX CONCURRENTLY idx_channel_outbound_card_due
    ON channel_outbound_card_message(next_attempt_at, created_at)
    WHERE delivery_failed_at IS NULL
      AND (next_attempt_at IS NOT NULL OR status IN ('pending', 'streaming'));
