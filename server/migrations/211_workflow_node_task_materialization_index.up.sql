CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_node_task_materialization
    ON workflow_node_task (materialization_status, created_at);
