ALTER TABLE feishu_project_issue_binding
  ADD COLUMN external_fields JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE feishu_project_issue_binding
  ADD CONSTRAINT feishu_project_issue_binding_external_fields_object
  CHECK (jsonb_typeof(external_fields) = 'object');
