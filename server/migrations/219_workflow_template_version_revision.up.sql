ALTER TABLE workflow_template_version
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);
