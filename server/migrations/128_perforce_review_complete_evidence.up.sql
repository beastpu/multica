ALTER TABLE perforce_review
    ADD COLUMN changes INTEGER[] NOT NULL DEFAULT '{}',
    ADD COLUMN commits INTEGER[] NOT NULL DEFAULT '{}',
    ADD COLUMN swarm_branch TEXT,
    ADD COLUMN event_type TEXT,
    ADD COLUMN sent_at TIMESTAMPTZ,
    ADD COLUMN raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb;
