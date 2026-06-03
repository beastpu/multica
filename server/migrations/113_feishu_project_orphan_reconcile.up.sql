-- Cadence stamp for the orphan reconcile pass: a periodic sweep that lists the
-- integration's bindings, asks Feishu Project whether each work item still
-- exists, and hard-deletes the Multica issue when its work item is gone
-- (deleted or archived). Separate from last_reconciled_at because the orphan
-- pass runs on its own (sparser) schedule. Nullable so existing integrations
-- run their first orphan pass on the next tick after this migration lands.
ALTER TABLE feishu_project_integration
    ADD COLUMN last_orphan_reconciled_at TIMESTAMPTZ;
