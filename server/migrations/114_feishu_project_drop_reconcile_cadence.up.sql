-- The periodic 6h full-sync reconcile was removed: the incremental sync keys
-- off Feishu's own updated_at watermark (clamped to now on write), which makes
-- the reconcile backstop redundant. last_reconciled_at is no longer read or
-- written. last_seen_updated_at_ms (the incremental watermark) is kept.
ALTER TABLE feishu_project_integration
    DROP COLUMN IF EXISTS last_reconciled_at;
