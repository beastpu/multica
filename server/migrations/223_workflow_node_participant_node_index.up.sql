CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_node_participant_node
    ON workflow_node_participant (workflow_node_instance_id, role, created_at);
