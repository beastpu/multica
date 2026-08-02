ALTER TABLE workflow
    ADD COLUMN applies_to_kind TEXT NOT NULL DEFAULT 'issue'
    CHECK (applies_to_kind IN ('issue'));
ALTER TABLE workflow
    ADD COLUMN applies_to_type_key TEXT NOT NULL DEFAULT '';

ALTER TABLE workflow_version
    RENAME CONSTRAINT workflow_version_pkey TO workflow_template_version_pkey;
ALTER TABLE workflow RENAME CONSTRAINT workflow_pkey TO workflow_template_pkey;

ALTER INDEX workflow_live_name_idx RENAME TO workflow_template_live_name_idx;
ALTER INDEX idx_workflow_version_unique
    RENAME TO idx_workflow_template_version_unique;
ALTER INDEX idx_workflow_one_draft RENAME TO idx_workflow_template_one_draft;
ALTER INDEX idx_workflow_workspace_status
    RENAME TO idx_workflow_template_workspace_status;

ALTER TABLE workflow_instance
    RENAME COLUMN workflow_version_id TO template_version_id;
ALTER TABLE workflow_instance RENAME COLUMN workflow_id TO template_id;
ALTER TABLE workflow_version RENAME COLUMN workflow_id TO template_id;

ALTER TABLE workflow_version RENAME TO workflow_template_version;
ALTER TABLE workflow RENAME TO workflow_template;
