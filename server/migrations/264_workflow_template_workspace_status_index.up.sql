CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_template_workspace_status
    ON workflow_template (workspace_id, status, updated_at DESC);
