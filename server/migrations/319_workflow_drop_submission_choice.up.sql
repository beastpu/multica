-- The branch a node "picked" is gone as a concept: an executor now delivers
-- structured output fields (validated against the node's declared schema and
-- stored in payload), and gateway cases route on those fields. Nothing reads
-- choice anymore.
ALTER TABLE workflow_node_submission
    DROP COLUMN choice;
