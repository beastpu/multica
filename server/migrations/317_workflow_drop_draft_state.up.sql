-- A workflow version is written only once it validates, so the draft state has
-- no way left to occur. Existing drafts are promoted rather than deleted: the
-- common one is a workflow created but never saved, whose definition is the
-- perfectly runnable default. A genuinely broken one surfaces its error the
-- next time someone opens or runs it, which is where the author can fix it.
UPDATE workflow_version
SET status = 'published',
    published_by = COALESCE(published_by, created_by),
    published_at = COALESCE(published_at, created_at),
    updated_at = now()
WHERE status = 'draft';

UPDATE workflow
SET status = 'published',
    updated_at = now()
WHERE status = 'draft';

UPDATE workflow AS target
SET latest_published_version_id = newest.id,
    updated_at = now()
FROM (
    SELECT DISTINCT ON (workflow_id, workspace_id) id, workflow_id, workspace_id
    FROM workflow_version
    WHERE status = 'published'
    ORDER BY workflow_id, workspace_id, version DESC
) AS newest
WHERE target.id = newest.workflow_id
  AND target.workspace_id = newest.workspace_id
  AND target.latest_published_version_id IS NULL;

ALTER TABLE workflow
    DROP CONSTRAINT workflow_template_status_check;

ALTER TABLE workflow
    ADD CONSTRAINT workflow_template_status_check
    CHECK (status IN ('published', 'archived'));

ALTER TABLE workflow
    ALTER COLUMN status SET DEFAULT 'published';

-- With drafts gone every version is published and nothing ever wrote
-- 'abandoned', so the column had exactly one possible value left.
ALTER TABLE workflow_version
    DROP CONSTRAINT workflow_template_version_status_check;

ALTER TABLE workflow_version
    DROP COLUMN status;

-- "At most one draft per workflow" no longer describes anything.
DROP INDEX IF EXISTS idx_workflow_one_draft;
