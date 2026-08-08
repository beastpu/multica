CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_channel_outbound_card_live
    ON channel_outbound_card_message(channel_type, last_patched_at)
    WHERE status IN ('pending', 'streaming');
