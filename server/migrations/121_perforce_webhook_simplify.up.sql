-- v1 Perforce integration is webhook-push only: master never calls Swarm, so the
-- per-connection Swarm credentials (user/ticket) and the poller's discovery
-- cursor are unused. A connection now only records the Swarm URL to route on.
ALTER TABLE perforce_connection
    DROP COLUMN IF EXISTS swarm_user,
    DROP COLUMN IF EXISTS swarm_ticket_encrypted,
    DROP COLUMN IF EXISTS last_seen_review_id,
    DROP COLUMN IF EXISTS last_polled_at;
