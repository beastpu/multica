-- Link issue-less agent executions to the workflow node task that owns them.
-- Relationships remain application-enforced; no FK is added.
ALTER TABLE agent_task_queue
    ADD COLUMN workflow_node_task_id UUID;

ALTER TABLE workflow_node_task
    DROP CONSTRAINT workflow_node_task_source_check;

ALTER TABLE workflow_node_task
    ADD CONSTRAINT workflow_node_task_source_check
    CHECK (source IN ('template', 'dynamic', 'execution'));
