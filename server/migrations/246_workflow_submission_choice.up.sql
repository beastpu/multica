-- The explicit branch a node picked on completion.
--
-- Branch decisions previously came from user-defined submission fields, which
-- the design removes. A single system-defined column replaces them: the value
-- is the key of an outgoing node, so the range is fixed by the graph rather
-- than by whatever a template author happened to name a field.
ALTER TABLE workflow_node_submission
    ADD COLUMN choice TEXT NOT NULL DEFAULT '';
