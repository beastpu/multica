CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_event_idempotency
    ON workflow_event (workflow_instance_id, idempotency_key);
