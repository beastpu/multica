CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_workflow_template_version_unique
    ON workflow_template_version (template_id, version);
