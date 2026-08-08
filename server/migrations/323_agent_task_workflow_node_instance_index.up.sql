-- Answers "which agent task is running this node's review?", the lookup that
-- replaces walking from the node to a carrier row to the task.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_task_workflow_node_instance
    ON agent_task_queue (workflow_node_instance_id, created_at DESC)
    WHERE workflow_node_instance_id IS NOT NULL;
