-- =====================
-- Perforce Project Strategy (per-project webhook handling)
-- =====================
--
-- Rows here are backend configuration data: which capability a project (Swarm
-- URL) routes to, and the default agent it assigns. They are seeded directly as
-- data (no product API surface) and read by the webhook at dispatch time.

-- name: GetPerforceProjectStrategyBySwarmURL :one
-- Webhook routing: find the enabled strategy configured for a project's Swarm
-- URL. Match ignores case and a trailing slash, mirroring perforce_connection.
-- The unique index on the normalised URL guarantees at most one row.
SELECT * FROM perforce_project_strategy
WHERE lower(rtrim(swarm_url, '/')) = lower(rtrim(sqlc.arg('swarm_url')::text, '/'))
  AND enabled = TRUE;

-- =====================
-- Create-per-event idempotency ledger
-- =====================

-- name: GetPerforceStrategyCreatedIssue :one
-- Fast-path duplicate check for a webhook retry before opening a transaction.
SELECT * FROM perforce_strategy_created_issue
WHERE strategy_id = $1 AND review_id = $2 AND review_updated_at = $3;

-- name: RecordPerforceStrategyCreatedIssue :one
-- Records that (strategy, review_id, review_updated_at) produced an issue.
-- ON CONFLICT DO NOTHING closes the race between two concurrent identical
-- deliveries: the loser gets no row back (pgx.ErrNoRows) and rolls back its
-- freshly-created issue.
INSERT INTO perforce_strategy_created_issue (
    strategy_id, review_id, review_updated_at, issue_id
) VALUES (
    $1, $2, $3, $4
)
ON CONFLICT (strategy_id, review_id, review_updated_at) DO NOTHING
RETURNING *;
