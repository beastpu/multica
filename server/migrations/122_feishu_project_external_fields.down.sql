ALTER TABLE feishu_project_issue_binding
  DROP CONSTRAINT IF EXISTS feishu_project_issue_binding_external_fields_object;

ALTER TABLE feishu_project_issue_binding
  DROP COLUMN IF EXISTS external_fields;
