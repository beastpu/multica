-- The progress card is gone, and with it the due-scan that read this index.
-- What remains of channel_outbound_card_message is a one-reply-per-task ledger,
-- reached only through the partial unique index on (task_id).
DROP INDEX CONCURRENTLY IF EXISTS idx_channel_outbound_card_live;
