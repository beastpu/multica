ALTER TABLE workflow_template
    ADD CONSTRAINT workflow_template_pkey
    PRIMARY KEY USING INDEX workflow_template_pkey_idx;

ALTER TABLE workflow_template_version
    ADD CONSTRAINT workflow_template_version_pkey
    PRIMARY KEY USING INDEX workflow_template_version_pkey_idx;

ALTER TABLE workflow_instance
    ADD CONSTRAINT workflow_instance_pkey
    PRIMARY KEY USING INDEX workflow_instance_pkey_idx;

ALTER TABLE workflow_instance_role_assignment
    ADD CONSTRAINT workflow_instance_role_assignment_pkey
    PRIMARY KEY USING INDEX workflow_instance_role_assignment_pkey_idx;

ALTER TABLE workflow_node_instance
    ADD CONSTRAINT workflow_node_instance_pkey
    PRIMARY KEY USING INDEX workflow_node_instance_pkey_idx;

ALTER TABLE workflow_node_participant
    ADD CONSTRAINT workflow_node_participant_pkey
    PRIMARY KEY USING INDEX workflow_node_participant_pkey_idx;

ALTER TABLE workflow_executor_resolution
    ADD CONSTRAINT workflow_executor_resolution_pkey
    PRIMARY KEY USING INDEX workflow_executor_resolution_pkey_idx;

ALTER TABLE workflow_node_task
    ADD CONSTRAINT workflow_node_task_pkey
    PRIMARY KEY USING INDEX workflow_node_task_pkey_idx;

ALTER TABLE workflow_node_submission
    ADD CONSTRAINT workflow_node_submission_pkey
    PRIMARY KEY USING INDEX workflow_node_submission_pkey_idx;

ALTER TABLE workflow_node_verdict
    ADD CONSTRAINT workflow_node_verdict_pkey
    PRIMARY KEY USING INDEX workflow_node_verdict_pkey_idx;

ALTER TABLE workflow_node_confirmation
    ADD CONSTRAINT workflow_node_confirmation_pkey
    PRIMARY KEY USING INDEX workflow_node_confirmation_pkey_idx;

ALTER TABLE workflow_acceptance
    ADD CONSTRAINT workflow_acceptance_pkey
    PRIMARY KEY USING INDEX workflow_acceptance_pkey_idx;

ALTER TABLE workflow_event
    ADD CONSTRAINT workflow_event_pkey
    PRIMARY KEY USING INDEX workflow_event_pkey_idx;
