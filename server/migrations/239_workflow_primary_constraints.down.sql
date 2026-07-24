ALTER TABLE workflow_event
    DROP CONSTRAINT IF EXISTS workflow_event_pkey;

ALTER TABLE workflow_acceptance
    DROP CONSTRAINT IF EXISTS workflow_acceptance_pkey;

ALTER TABLE workflow_node_confirmation
    DROP CONSTRAINT IF EXISTS workflow_node_confirmation_pkey;

ALTER TABLE workflow_node_verdict
    DROP CONSTRAINT IF EXISTS workflow_node_verdict_pkey;

ALTER TABLE workflow_node_submission
    DROP CONSTRAINT IF EXISTS workflow_node_submission_pkey;

ALTER TABLE workflow_node_task
    DROP CONSTRAINT IF EXISTS workflow_node_task_pkey;

ALTER TABLE workflow_executor_resolution
    DROP CONSTRAINT IF EXISTS workflow_executor_resolution_pkey;

ALTER TABLE workflow_node_participant
    DROP CONSTRAINT IF EXISTS workflow_node_participant_pkey;

ALTER TABLE workflow_node_instance
    DROP CONSTRAINT IF EXISTS workflow_node_instance_pkey;

ALTER TABLE workflow_instance_role_assignment
    DROP CONSTRAINT IF EXISTS workflow_instance_role_assignment_pkey;

ALTER TABLE workflow_instance
    DROP CONSTRAINT IF EXISTS workflow_instance_pkey;

ALTER TABLE workflow_template_version
    DROP CONSTRAINT IF EXISTS workflow_template_version_pkey;

ALTER TABLE workflow_template
    DROP CONSTRAINT IF EXISTS workflow_template_pkey;
