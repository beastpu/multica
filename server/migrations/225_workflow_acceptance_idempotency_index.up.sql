CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_acceptance_idempotency
    ON workflow_acceptance (workflow_instance_id, idempotency_key);
