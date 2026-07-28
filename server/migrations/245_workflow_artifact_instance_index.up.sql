-- Backs "every live artifact in this run", which the workflow-level artifact
-- listing and the downstream prompt index both read.
CREATE INDEX CONCURRENTLY workflow_artifact_instance_idx
    ON workflow_artifact (workspace_id, workflow_instance_id)
    WHERE superseded_at IS NULL;
