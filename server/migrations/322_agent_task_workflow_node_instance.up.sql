-- Let an agent task point at the node it belongs to.
--
-- A review had no such link, so it was given a row in workflow_node_task to
-- point at instead — a table whose rows mean "a unit of work with an executor".
-- A review is not that: its actor is the node's reviewer, resolved from a role
-- binding when the review is dispatched, so the executor column stayed null for
-- the life of the row.
--
-- Six places then had to remember that one kind of row is not what the table
-- says. Two of them forgot: the sweeper told every reviewed node that "a
-- workflow task needs a manual executor", and the workbench offered an assign
-- control that resolved nothing. Both were fixed by adding a seventh exception.
--
-- With this column a review points at its node and stops being a task at all.
ALTER TABLE agent_task_queue
    ADD COLUMN IF NOT EXISTS workflow_node_instance_id UUID;
