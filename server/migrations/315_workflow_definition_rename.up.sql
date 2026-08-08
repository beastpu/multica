-- The "template" layer never existed. A template is a prototype you
-- instantiate into something editable that then diverges from it; the only
-- thing instantiated here is a run, and nothing records where a workflow came
-- from. What the table actually holds is the workflow itself, versioned.
--
-- Renamed rather than left alone: the word promised a relationship the model
-- does not have, and keeping it in the schema while the API and UI say
-- "workflow" is the two-vocabularies problem this refactor keeps deleting.
--
-- Builtin templates keep the word. Those are genuine templates — copied into a
-- workspace, then owned by it, with no link back.
ALTER TABLE workflow_template RENAME TO workflow;
ALTER TABLE workflow_template_version RENAME TO workflow_version;

ALTER TABLE workflow_version RENAME COLUMN template_id TO workflow_id;
ALTER TABLE workflow_instance RENAME COLUMN template_id TO workflow_id;
ALTER TABLE workflow_instance
    RENAME COLUMN template_version_id TO workflow_version_id;

ALTER INDEX idx_workflow_template_workspace_status
    RENAME TO idx_workflow_workspace_status;
ALTER INDEX idx_workflow_template_one_draft RENAME TO idx_workflow_one_draft;
ALTER INDEX idx_workflow_template_version_unique
    RENAME TO idx_workflow_version_unique;
ALTER INDEX workflow_template_live_name_idx RENAME TO workflow_live_name_idx;

ALTER TABLE workflow RENAME CONSTRAINT workflow_template_pkey TO workflow_pkey;
ALTER TABLE workflow_version
    RENAME CONSTRAINT workflow_template_version_pkey TO workflow_version_pkey;

-- applies_to said which issue type a workflow was for. It was never enforced —
-- nothing reads it when a run starts — and it only means anything in a model
-- where runs are born from issues. A run is a top-level object that may
-- optionally attach to one, so the column describes a design we did not build.
ALTER TABLE workflow DROP COLUMN applies_to_kind;
ALTER TABLE workflow DROP COLUMN applies_to_type_key;
