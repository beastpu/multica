CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_issue_workflow_origin
    ON issue (origin_type, origin_id) WHERE origin_type = 'workflow';
