-- Perforce / Helix Swarm integration: per-workspace connection (poll scope),
-- mirrored Swarm review state, and the link table joining issues ↔ reviews.
--
-- Unlike the GitHub integration (webhook push), Swarm review state is pulled
-- by a multi-replica-safe poller. review_id is the stable spine: a Swarm
-- review keeps its id across shelve iterations and through submit, while the
-- changelist is renumbered on submit — so we track the shelved changelist and
-- the (later) committed changelist separately.

CREATE TABLE perforce_connection (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    swarm_url       TEXT NOT NULL,
    -- Swarm API account, per-workspace. swarm_ticket_encrypted is the sealed
    -- P4/Swarm ticket (secretbox), write-only from the UI — never returned to
    -- clients. Nullable so a scope-only edit can preserve the stored secret;
    -- the handler enforces a credential exists before the connection is usable.
    swarm_user      TEXT NOT NULL,
    swarm_ticket_encrypted BYTEA,
    connected_by_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    -- Discovery cursor: highest Swarm review id already scanned. Swarm lists
    -- reviews by id descending, so each tick only pages new reviews (id greater
    -- than this) rather than re-scanning history. In-flight reviews already
    -- stored are advanced by re-fetching them by id, independent of this cursor.
    last_seen_review_id BIGINT NOT NULL DEFAULT 0,
    last_polled_at  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One Swarm connection per workspace (one-owner relationship).
    UNIQUE (workspace_id)
);

CREATE TABLE perforce_review (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    review_id       BIGINT NOT NULL,
    title           TEXT NOT NULL,
    -- Raw Swarm review state. Kept verbatim so a future Swarm value renders a
    -- generic fallback in the UI rather than failing a CHECK.
    state           TEXT NOT NULL
        CHECK (state IN ('needsReview', 'needsRevision', 'approved', 'rejected', 'archived')),
    html_url        TEXT NOT NULL,
    author          TEXT,
    -- The shelved changelist currently under review (pending, pre-submit).
    shelved_cl      INTEGER,
    -- The submitted changelist once the review is committed. NULL until then.
    -- Renumbered from shelved_cl on submit; this is the value issue history
    -- should reference.
    committed_cl    INTEGER,
    review_created_at TIMESTAMPTZ NOT NULL,
    review_updated_at TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, review_id)
);

CREATE INDEX idx_perforce_review_workspace ON perforce_review(workspace_id);

CREATE TABLE issue_perforce_review (
    issue_id           UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    perforce_review_id UUID NOT NULL REFERENCES perforce_review(id) ON DELETE CASCADE,
    linked_by_type     TEXT,
    linked_by_id       UUID,
    close_intent       BOOLEAN NOT NULL DEFAULT FALSE,
    linked_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, perforce_review_id)
);

CREATE INDEX idx_issue_perforce_review_review ON issue_perforce_review(perforce_review_id);
