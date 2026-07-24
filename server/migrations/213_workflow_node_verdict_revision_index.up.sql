CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_node_verdict_revision
    ON workflow_node_verdict (workflow_node_instance_id, revision);
