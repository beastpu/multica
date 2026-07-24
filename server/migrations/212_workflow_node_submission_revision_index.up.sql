CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_node_submission_revision
    ON workflow_node_submission (workflow_node_instance_id, revision);
