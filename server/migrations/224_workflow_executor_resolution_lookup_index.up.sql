CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_executor_resolution_lookup
    ON workflow_executor_resolution (
        workflow_node_instance_id,
        workflow_node_task_id,
        status,
        created_at DESC
    );
