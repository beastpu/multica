CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_task_workflow_node_task
    ON agent_task_queue (workflow_node_task_id, created_at DESC)
    WHERE workflow_node_task_id IS NOT NULL;
