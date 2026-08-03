ALTER TABLE workflow_node_task
    DROP CONSTRAINT workflow_node_task_source_check;

ALTER TABLE workflow_node_task
    ADD CONSTRAINT workflow_node_task_source_check
    CHECK (source IN ('template', 'dynamic', 'execution')) NOT VALID;
