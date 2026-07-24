CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_confirmation_member
ON workflow_node_confirmation (workflow_node_instance_id, member_id);
