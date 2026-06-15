ALTER TABLE perforce_connection
    ADD COLUMN swarm_user TEXT NOT NULL DEFAULT '',
    ADD COLUMN swarm_ticket_encrypted BYTEA,
    ADD COLUMN last_seen_review_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN last_polled_at TIMESTAMPTZ;
