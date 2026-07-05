-- Workspace capability roles (plan C-1). One row per (workspace, capability)
-- names the agent that executes that capability's derived work. The joined
-- agent/runtime columns exist so callers (config API validation, assessment
-- Trigger) can check agent health without a second round-trip.

-- name: GetWorkspaceAgentCapability :one
SELECT
  c.workspace_id,
  c.capability,
  c.agent_id,
  c.project_id,
  c.max_concurrent_tasks,
  c.created_at,
  a.name AS agent_name,
  a.workspace_id AS agent_workspace_id,
  a.archived_at AS agent_archived_at,
  a.runtime_id AS agent_runtime_id,
  COALESCE(r.runtime_mode, '') AS agent_runtime_mode
FROM workspace_agent_capability c
JOIN agent a ON a.id = c.agent_id
LEFT JOIN agent_runtime r ON r.id = a.runtime_id
WHERE c.workspace_id = $1 AND c.capability = $2;

-- name: UpsertWorkspaceAgentCapability :one
-- Multi-replica safe: the composite PK is the idempotency key; concurrent
-- writers converge on last-write-wins for the payload columns.
INSERT INTO workspace_agent_capability (
  workspace_id, capability, agent_id, project_id, max_concurrent_tasks
) VALUES (
  $1, $2, $3, sqlc.narg(project_id), $4
)
ON CONFLICT (workspace_id, capability) DO UPDATE SET
  agent_id = EXCLUDED.agent_id,
  project_id = EXCLUDED.project_id,
  max_concurrent_tasks = EXCLUDED.max_concurrent_tasks
RETURNING *;

-- name: DeleteWorkspaceAgentCapability :execrows
DELETE FROM workspace_agent_capability
WHERE workspace_id = $1 AND capability = $2;
