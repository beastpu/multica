-- Supports the paint claim, which filters channel_type + status and takes the
-- oldest by last_patched_at. Migration 324 dropped the previous version of this
-- index when the progress card was removed; the CardKit streaming card reads
-- the same shape, plus the terminal rows that are due until they are drawn.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_channel_outbound_card_due
    ON channel_outbound_card_message(channel_type, last_patched_at)
    WHERE status IN ('pending', 'streaming', 'final', 'error');
