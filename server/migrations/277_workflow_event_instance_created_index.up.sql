CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_event_instance_created
    ON workflow_event (workflow_instance_id, created_at DESC);
