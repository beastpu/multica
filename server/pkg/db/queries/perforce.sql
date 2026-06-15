-- =====================
-- Perforce Connection
-- =====================

-- name: GetPerforceConnectionByWorkspace :one
SELECT * FROM perforce_connection
WHERE workspace_id = $1;

-- name: ListPerforceConnectionsBySwarmURL :many
-- Webhook routing: one Swarm URL can map to several workspaces (a shared
-- Swarm). Match ignores case and a trailing slash. The issue identifier in the
-- review description disambiguates among the returned candidates.
SELECT * FROM perforce_connection
WHERE lower(rtrim(swarm_url, '/')) = lower(rtrim(sqlc.arg('swarm_url')::text, '/'))
ORDER BY created_at ASC;

-- name: UpsertPerforceConnection :one
-- A connection only records the Swarm URL the webhook routes on; master never
-- calls Swarm, so no credentials are stored.
INSERT INTO perforce_connection (
    workspace_id, swarm_url, connected_by_id
) VALUES (
    $1, $2, sqlc.narg('connected_by_id')
)
ON CONFLICT (workspace_id) DO UPDATE SET
    swarm_url = EXCLUDED.swarm_url,
    connected_by_id = EXCLUDED.connected_by_id,
    updated_at = now()
RETURNING *;

-- =====================
-- Perforce Review
-- =====================

-- name: UpsertPerforceReview :one
-- review_id is the stable spine. committed_cl is preserved on UPDATE when the
-- incoming payload omits it (sqlc.narg null) — a metadata-only poll of an
-- already-committed review must not erase the recorded submitted changelist.
INSERT INTO perforce_review (
    workspace_id, review_id, title, state, html_url, author,
    shelved_cl, committed_cl, review_created_at, review_updated_at
) VALUES (
    $1, $2, $3, $4, $5, sqlc.narg('author'),
    sqlc.narg('shelved_cl'), sqlc.narg('committed_cl'), $6, $7
)
ON CONFLICT (workspace_id, review_id) DO UPDATE SET
    title = EXCLUDED.title,
    state = EXCLUDED.state,
    html_url = EXCLUDED.html_url,
    author = EXCLUDED.author,
    shelved_cl = EXCLUDED.shelved_cl,
    committed_cl = COALESCE(EXCLUDED.committed_cl, perforce_review.committed_cl),
    review_updated_at = EXCLUDED.review_updated_at,
    updated_at = now()
RETURNING *;

-- name: GetPerforceReviewByWorkspaceReviewID :one
SELECT * FROM perforce_review
WHERE workspace_id = $1 AND review_id = $2;

-- name: ListReviewsByIssue :many
SELECT pr.* FROM perforce_review pr
JOIN issue_perforce_review ipr ON ipr.perforce_review_id = pr.id
WHERE ipr.issue_id = $1
ORDER BY pr.review_created_at DESC;

-- name: ListIssueIDsForReview :many
SELECT issue_id FROM issue_perforce_review
WHERE perforce_review_id = $1;

-- name: GetIssuePerforceReviewCloseAggregate :one
-- Gates auto-advance, mirroring the GitHub aggregate. A review is "in flight"
-- until it is committed, rejected, or archived. The issue auto-advances when
-- no linked review is in flight AND at least one committed review declared
-- closing intent.
SELECT
    COALESCE(SUM(CASE
        WHEN pr.committed_cl IS NULL AND pr.state IN ('needsReview', 'needsRevision', 'approved')
        THEN 1 ELSE 0 END), 0)::bigint AS open_count,
    COALESCE(SUM(CASE
        WHEN pr.committed_cl IS NOT NULL AND ipr.close_intent
        THEN 1 ELSE 0 END), 0)::bigint AS committed_with_close_intent_count
FROM perforce_review pr
JOIN issue_perforce_review ipr ON ipr.perforce_review_id = pr.id
WHERE ipr.issue_id = $1;

-- =====================
-- Issue ↔ Review link
-- =====================

-- name: LinkIssueToPerforceReview :exec
INSERT INTO issue_perforce_review (
    issue_id, perforce_review_id, linked_by_type, linked_by_id, close_intent
) VALUES (
    $1, $2, sqlc.narg('linked_by_type'), sqlc.narg('linked_by_id'), $3
)
ON CONFLICT (issue_id, perforce_review_id) DO UPDATE SET
    close_intent = CASE
        WHEN sqlc.arg('preserve_close_intent') THEN issue_perforce_review.close_intent
        ELSE EXCLUDED.close_intent
    END;

-- name: UnlinkIssueFromPerforceReview :exec
DELETE FROM issue_perforce_review
WHERE issue_id = $1 AND perforce_review_id = $2;
