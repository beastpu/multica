CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_node_task_issue
    ON workflow_node_task (issue_id)
    WHERE issue_id IS NOT NULL;
