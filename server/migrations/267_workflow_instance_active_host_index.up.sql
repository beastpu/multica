CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_instance_active_host
    ON workflow_instance (host_issue_id)
    WHERE status IN ('needs_setup', 'running', 'paused');
