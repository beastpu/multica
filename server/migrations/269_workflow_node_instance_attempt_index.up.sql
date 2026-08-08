CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_node_instance_attempt
    ON workflow_node_instance (workflow_instance_id, node_key, attempt);
