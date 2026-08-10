-- The workspace/status list index went with the status column too. The list
-- orders by updated_at within a workspace and no longer filters by status, so
-- the replacement drops the middle column rather than restoring it.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_workspace_updated
    ON workflow (workspace_id, updated_at DESC);
