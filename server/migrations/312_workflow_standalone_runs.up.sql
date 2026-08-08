-- A workflow run may be started on its own or bound to an existing Issue.
-- The run owns that optional relationship; Issue remains an ordinary work item.
ALTER TABLE workflow_instance
    ALTER COLUMN host_issue_id DROP NOT NULL;

ALTER TABLE workflow_instance
    ADD COLUMN title TEXT NOT NULL DEFAULT '';

UPDATE workflow_instance instance
SET title = COALESCE(
    NULLIF((
        SELECT host.title
        FROM issue host
        WHERE host.id = instance.host_issue_id
          AND host.workspace_id = instance.workspace_id
    ), ''),
    NULLIF((
        SELECT template.name
        FROM workflow_template template
        WHERE template.id = instance.template_id
          AND template.workspace_id = instance.workspace_id
    ), ''),
    'Workflow run'
)
WHERE instance.title = '';
