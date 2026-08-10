-- Restores the columns, not the data: which workflows were archived is gone
-- with the column, and the rollback cannot tell a resurrected one from a
-- workflow that was published all along.
ALTER TABLE workflow
    ADD COLUMN status TEXT NOT NULL DEFAULT 'published';

ALTER TABLE workflow
    ADD CONSTRAINT workflow_template_status_check
    CHECK (status IN ('published', 'archived'));

ALTER TABLE workflow
    ADD COLUMN archived_at TIMESTAMPTZ;
