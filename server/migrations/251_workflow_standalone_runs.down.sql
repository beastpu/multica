-- Standalone runs cannot be represented by the previous schema. Remove their
-- dependent records explicitly (the workflow domain intentionally has no FKs).
DELETE FROM workflow_event
WHERE workflow_instance_id IN (
    SELECT id FROM workflow_instance WHERE host_issue_id IS NULL
);
DELETE FROM workflow_acceptance
WHERE workflow_instance_id IN (
    SELECT id FROM workflow_instance WHERE host_issue_id IS NULL
);
DELETE FROM workflow_node_verdict
WHERE workflow_instance_id IN (
    SELECT id FROM workflow_instance WHERE host_issue_id IS NULL
);
DELETE FROM workflow_node_submission
WHERE workflow_instance_id IN (
    SELECT id FROM workflow_instance WHERE host_issue_id IS NULL
);
DELETE FROM workflow_artifact
WHERE workflow_instance_id IN (
    SELECT id FROM workflow_instance WHERE host_issue_id IS NULL
);
DELETE FROM workflow_executor_resolution
WHERE workflow_instance_id IN (
    SELECT id FROM workflow_instance WHERE host_issue_id IS NULL
);
DELETE FROM workflow_node_task
WHERE workflow_instance_id IN (
    SELECT id FROM workflow_instance WHERE host_issue_id IS NULL
);
DELETE FROM workflow_node_participant
WHERE workflow_node_instance_id IN (
    SELECT node.id
    FROM workflow_node_instance node
    JOIN workflow_instance instance ON instance.id = node.workflow_instance_id
    WHERE instance.host_issue_id IS NULL
);
DELETE FROM workflow_node_instance
WHERE workflow_instance_id IN (
    SELECT id FROM workflow_instance WHERE host_issue_id IS NULL
);
DELETE FROM workflow_instance_role_assignment
WHERE workflow_instance_id IN (
    SELECT id FROM workflow_instance WHERE host_issue_id IS NULL
);
DELETE FROM workflow_instance WHERE host_issue_id IS NULL;

ALTER TABLE workflow_instance DROP COLUMN title;
ALTER TABLE workflow_instance ALTER COLUMN host_issue_id SET NOT NULL;
