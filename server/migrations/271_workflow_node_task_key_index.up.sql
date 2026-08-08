CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_node_task_key
    ON workflow_node_task (workflow_node_instance_id, task_key);
