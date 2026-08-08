-- Collapse the node model's nine ways of naming people into three fields.
--
-- Stored definitions carry the old shape and the definition parser rejects
-- unknown fields, so every existing template version would fail to load under
-- the new schema. Workflows have only ever run in the test environment, so the
-- rows are cleared rather than translated; on any deployment that never used
-- workflows these deletes are a no-op.
DELETE FROM workflow_acceptance;
DELETE FROM workflow_node_confirmation;
DELETE FROM workflow_node_verdict;
DELETE FROM workflow_node_submission;
DELETE FROM workflow_executor_resolution;
DELETE FROM workflow_node_participant;
DELETE FROM workflow_node_task;
DELETE FROM workflow_node_instance;
DELETE FROM workflow_instance_role_assignment;
DELETE FROM workflow_event;
DELETE FROM workflow_instance;
DELETE FROM workflow_template_version;
DELETE FROM workflow_template;

-- Confirmation was a second way to say "the owner has to sign off", which is
-- now reviewer.kind = 'owner' recording an ordinary verdict.
DROP TABLE workflow_node_confirmation;

-- A node whose executor has delivered but whose reviewer has not ruled is
-- neither active nor blocked; the flow is stopped on the reviewer.
ALTER TABLE workflow_node_instance
    DROP CONSTRAINT workflow_node_instance_status_check;

ALTER TABLE workflow_node_instance
    ADD CONSTRAINT workflow_node_instance_status_check
    CHECK (status IN (
        'pending',
        'ready',
        'active',
        'in_review',
        'waiting',
        'completed',
        'blocked',
        'failed',
        'skipped',
        'superseded',
        'cancelled'
    ));

-- participant_roles is gone, so no node seats a plain participant. The
-- approver slot went with the acceptance node. The reviewer is seated so the
-- canvas can answer who a node in review is waiting on.
ALTER TABLE workflow_node_participant
    DROP CONSTRAINT workflow_node_participant_role_check;

ALTER TABLE workflow_node_participant
    ADD CONSTRAINT workflow_node_participant_role_check
    CHECK (role IN ('owner', 'reviewer'));

-- Acceptance judges the run, not a node, and there is no acceptance node left
-- to point at.
ALTER TABLE workflow_acceptance
    ALTER COLUMN workflow_node_instance_id DROP NOT NULL;
