ALTER TABLE workflow_version
    ADD COLUMN status TEXT NOT NULL DEFAULT 'draft';

UPDATE workflow_version SET status = 'published';

ALTER TABLE workflow_version
    ADD CONSTRAINT workflow_template_version_status_check
    CHECK (status IN ('draft', 'published', 'abandoned'));

ALTER TABLE workflow
    ALTER COLUMN status SET DEFAULT 'draft';

ALTER TABLE workflow
    DROP CONSTRAINT workflow_template_status_check;

ALTER TABLE workflow
    ADD CONSTRAINT workflow_template_status_check
    CHECK (status IN ('draft', 'published', 'archived'));
