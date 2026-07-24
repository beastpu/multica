CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_template_one_draft
    ON workflow_template_version (template_id) WHERE status = 'draft';
