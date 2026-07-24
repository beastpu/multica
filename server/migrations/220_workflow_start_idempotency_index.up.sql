CREATE UNIQUE INDEX CONCURRENTLY workflow_event_workspace_start_idempotency_uidx
    ON workflow_event (workspace_id, idempotency_key)
    WHERE event_type = 'workflow.started';
