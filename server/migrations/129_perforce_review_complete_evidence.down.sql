ALTER TABLE perforce_review
    DROP COLUMN IF EXISTS raw_payload,
    DROP COLUMN IF EXISTS sent_at,
    DROP COLUMN IF EXISTS event_type,
    DROP COLUMN IF EXISTS swarm_branch,
    DROP COLUMN IF EXISTS commits,
    DROP COLUMN IF EXISTS changes;
