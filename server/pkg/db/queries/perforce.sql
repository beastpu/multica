-- =====================
-- Perforce Connection
-- =====================

-- name: GetPerforceConnectionByWorkspace :one
SELECT * FROM perforce_connection
WHERE workspace_id = $1;

-- name: ListPerforceConnectionsForPolling :many
SELECT * FROM perforce_connection
ORDER BY created_at ASC;

-- name: UpsertPerforceConnection :one
-- swarm_ticket_encrypted preserves the stored secret on a scope-only edit:
-- when the caller omits the ticket (sqlc.narg null) the existing ciphertext is
-- kept, so the UI never has to round-trip the secret back to re-save scope.
INSERT INTO perforce_connection (
    workspace_id, swarm_url, swarm_user, swarm_ticket_encrypted, connected_by_id
) VALUES (
    $1, $2, $3, sqlc.narg('swarm_ticket_encrypted'), sqlc.narg('connected_by_id')
)
ON CONFLICT (workspace_id) DO UPDATE SET
    swarm_url = EXCLUDED.swarm_url,
    swarm_user = EXCLUDED.swarm_user,
    swarm_ticket_encrypted = COALESCE(EXCLUDED.swarm_ticket_encrypted, perforce_connection.swarm_ticket_encrypted),
    connected_by_id = EXCLUDED.connected_by_id,
    updated_at = now()
RETURNING *;

-- name: UpdatePerforceConnectionPollCursor :exec
-- Advances the discovery cursor after a successful tick. last_seen_review_id is
-- monotonic: GREATEST guards against a late tick rewinding the cursor.
UPDATE perforce_connection SET
    last_seen_review_id = GREATEST(last_seen_review_id, $2),
    last_polled_at = now(),
    updated_at = now()
WHERE id = $1;

-- name: DeletePerforceConnection :exec
DELETE FROM perforce_connection WHERE id = $1 AND workspace_id = $2;

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

-- name: ListInFlightPerforceReviews :many
-- Reviews the poller must re-fetch by id to catch progress (approval, submit).
-- In flight = not yet committed and not in a terminal Swarm state. Bounded so a
-- single tick cannot fan out unboundedly.
SELECT * FROM perforce_review
WHERE workspace_id = $1
  AND committed_cl IS NULL
  AND state IN ('needsReview', 'needsRevision', 'approved')
ORDER BY review_updated_at ASC
LIMIT $2;

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
