CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_node_instance_status
    ON workflow_node_instance (workflow_instance_id, status, display_order);
