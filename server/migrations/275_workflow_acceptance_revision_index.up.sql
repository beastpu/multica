CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_acceptance_revision
    ON workflow_acceptance (workflow_instance_id, revision);
