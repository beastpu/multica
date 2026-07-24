ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_origin_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_origin_type_check
    CHECK (origin_type IN ('autopilot', 'quick_create', 'lark_chat', 'slack_chat', 'agent_create'));

DROP TABLE IF EXISTS workflow_event;
DROP TABLE IF EXISTS workflow_acceptance;
DROP TABLE IF EXISTS workflow_node_confirmation;
DROP TABLE IF EXISTS workflow_node_verdict;
DROP TABLE IF EXISTS workflow_node_submission;
DROP TABLE IF EXISTS workflow_node_task;
DROP TABLE IF EXISTS workflow_executor_resolution;
DROP TABLE IF EXISTS workflow_node_participant;
DROP TABLE IF EXISTS workflow_node_instance;
DROP TABLE IF EXISTS workflow_instance_role_assignment;
DROP TABLE IF EXISTS workflow_instance;
DROP TABLE IF EXISTS workflow_template_version;
DROP TABLE IF EXISTS workflow_template;
