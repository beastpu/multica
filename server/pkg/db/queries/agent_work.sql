-- Agent derived-work ("agent_work") primitives. A projection issue is a
-- normal issue whose authoritative marker is the reserved metadata key
-- `agent_work` (docs/agent-fix-p4-assessment-issue-design.md, Part 1). Every
-- write here is guarded by that marker + workspace so it can never touch an
-- ordinary user issue.

-- name: GetAgentWorkProject :one
SELECT project_id FROM agent_work_project
WHERE workspace_id = $1 AND kind = $2;

-- name: InsertAgentWorkProject :execrows
-- Multi-replica safe: the composite PK is the idempotency key; a losing
-- replica inserts nothing and re-reads the mapping.
INSERT INTO agent_work_project (workspace_id, kind, project_id)
VALUES ($1, $2, $3)
ON CONFLICT (workspace_id, kind) DO NOTHING;

-- name: LockAgentWorkProjectKind :exec
-- Transaction-scoped advisory lock serializing lazy project creation for one
-- (workspace, kind) across replicas, so a race cannot leave an orphan project
-- behind a lost ON CONFLICT. Same pattern as LockIssueDuplicateKey.
SELECT pg_advisory_xact_lock(hashtextextended('agent_work_project:' || sqlc.arg(workspace_id)::text || ':' || sqlc.arg(kind)::text, 0));

-- name: CreateAgentWorkIssue :one
-- Dedicated insert for projection issues: stamps the reserved agent_work
-- metadata at creation (the ordinary CreateIssue never writes metadata, and
-- the user metadata API rejects the reserved key). Assignee is the workspace's
-- capability agent when one is configured (plan C-1: "指派即派发" — the native
-- task the Trigger creates in the same transaction hangs on this issue);
-- NULL assignee is the legacy env-allowlist path, which C-2 removes.
INSERT INTO issue (
    workspace_id, title, description, status, priority,
    creator_type, creator_id, position, number, project_id, metadata,
    assignee_type, assignee_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, 0, $8, $9, sqlc.arg(metadata)::jsonb,
    sqlc.narg(assignee_type), sqlc.narg(assignee_id)
) RETURNING *;

-- name: UpdateAgentWorkIssueStatus :execrows
-- Server-side status projection. The jsonb_exists guard makes a wrong id a
-- no-op instead of flipping a real issue; callers treat 0 rows as "skip"
-- (historical rows whose run predates the projection have no issue).
UPDATE issue SET
    status = $2,
    updated_at = now()
WHERE id = $1
  AND workspace_id = $3
  AND jsonb_exists(metadata, 'agent_work');
