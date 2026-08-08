-- At most one live artifact per (node instance, key). The partial predicate is
-- what makes the append-only table safe to read: superseded rows are excluded,
-- so a concurrent replace cannot leave two rows claiming to be current.
CREATE UNIQUE INDEX CONCURRENTLY workflow_artifact_current_idx
    ON workflow_artifact (workflow_node_instance_id, artifact_key)
    WHERE superseded_at IS NULL;
