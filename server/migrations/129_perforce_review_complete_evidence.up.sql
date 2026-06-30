-- IF NOT EXISTS keeps this re-runnable: a dev DB that already applied the
-- pre-renumber copy of this migration (when it was numbered 128) records that
-- old version string, so after the renumber to 129 the runner re-applies it
-- once. Constant defaults keep all six ADDs on Postgres' fast metadata-only
-- path (no table rewrite) even on a large perforce_review.
ALTER TABLE perforce_review
    ADD COLUMN IF NOT EXISTS changes INTEGER[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS commits INTEGER[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS swarm_branch TEXT,
    ADD COLUMN IF NOT EXISTS event_type TEXT,
    ADD COLUMN IF NOT EXISTS sent_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb;
