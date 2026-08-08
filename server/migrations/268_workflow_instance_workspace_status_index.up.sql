CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_instance_workspace_status
    ON workflow_instance (workspace_id, status, updated_at DESC);
