-- Per-project Perforce webhook strategy. The *capability* (how to process a
-- review event) lives in code as a named strategy; the *choice* of which
-- capability a given project (identified by its Swarm URL) uses is data, stored
-- here. This is intentionally independent of perforce_connection /
-- perforce_review / issue_perforce_review — those tables are not modified, and
-- a project without a strategy row keeps the default link-to-existing-issue
-- behaviour unchanged.

CREATE TABLE perforce_project_strategy (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- Routing key: the Swarm base URL of the project whose webhooks this rule
    -- handles. Matched case-insensitively with a trailing slash ignored (see
    -- the unique index below), mirroring perforce_connection routing.
    swarm_url           TEXT NOT NULL,
    -- Names a capability registered in code (e.g. 'create_per_event'). A value
    -- outside the registry is ignored at dispatch time, never a crash — enum
    -- drift downgrades, it does not 500.
    strategy            TEXT NOT NULL,
    -- The agent every issue created by this rule is assigned to. RESTRICT so an
    -- agent backing an active rule cannot be deleted out from under it without
    -- the operator first removing/repointing the rule.
    default_assignee_id UUID NOT NULL REFERENCES agent(id) ON DELETE RESTRICT,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_by_id       UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One rule per project. The normalised key (lower-cased, trailing slash
-- stripped) makes "https://swarm/" and "https://swarm" collide, so a project
-- can never accidentally have two conflicting strategies.
CREATE UNIQUE INDEX uq_perforce_project_strategy_swarm
    ON perforce_project_strategy (lower(rtrim(swarm_url, '/')));

-- Idempotency ledger + provenance for the create-per-event capability: one
-- issue per distinct (review_id, review_updated_at). A webhook retry carries an
-- identical review_updated_at and is rejected by the unique constraint, so
-- retries never double-create an issue. A genuinely new event (Swarm bumps
-- review.updated) has a fresh key and does create another issue. Independent of
-- perforce_review — the create-per-event path never touches that table.
CREATE TABLE perforce_strategy_created_issue (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id       UUID NOT NULL REFERENCES perforce_project_strategy(id) ON DELETE CASCADE,
    review_id         BIGINT NOT NULL,
    review_updated_at TIMESTAMPTZ NOT NULL,
    issue_id          UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (strategy_id, review_id, review_updated_at)
);

CREATE INDEX idx_perforce_strategy_created_issue_strategy
    ON perforce_strategy_created_issue (strategy_id);
