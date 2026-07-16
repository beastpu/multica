-- name: UpsertWorkspaceCloudRuntimeEnv :exec
INSERT INTO workspace_cloud_runtime_env (workspace_id, env_sealed, updated_by, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (workspace_id)
DO UPDATE SET env_sealed = EXCLUDED.env_sealed, updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: GetWorkspaceCloudRuntimeEnv :one
SELECT * FROM workspace_cloud_runtime_env
WHERE workspace_id = $1;

-- name: DeleteWorkspaceCloudRuntimeEnv :exec
DELETE FROM workspace_cloud_runtime_env
WHERE workspace_id = $1;
