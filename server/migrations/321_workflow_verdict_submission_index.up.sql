-- Answers "what did this revision's review conclude?" — the question asked
-- whenever a rework loop has to be explained after the fact.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_node_verdict_submission
    ON workflow_node_verdict (workspace_id, submission_id, revision DESC);
