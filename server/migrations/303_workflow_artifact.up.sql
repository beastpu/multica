-- Node artifacts: the formal output a workflow node delivers.
-- Relationships are enforced in application transactions; no foreign keys and
-- no cascading actions, matching the rest of the workflow domain.
--
-- The table is append-only. Replacing an artifact stamps superseded_at on the
-- current row and inserts a new one, so an agent that overwrites a sound
-- document during rework cannot destroy it. Only the row with superseded_at
-- IS NULL is ever read by the product; history exists for support and
-- debugging and is not surfaced as a version concept.
CREATE TABLE workflow_artifact (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    workflow_instance_id UUID NOT NULL,
    workflow_node_instance_id UUID NOT NULL,

    -- Matches the artifact requirement key declared on the node definition.
    artifact_key TEXT NOT NULL CHECK (length(artifact_key) BETWEEN 1 AND 100),
    -- The node attempt this content was delivered in. Lets support tell "this
    -- is what the second rework produced" without making attempts a user
    -- concept.
    attempt INTEGER NOT NULL CHECK (attempt > 0),

    kind TEXT NOT NULL CHECK (kind IN ('document', 'attachment', 'link')),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description TEXT NOT NULL DEFAULT '',

    -- Exactly one carrier is populated, per kind. Documents live inline
    -- because they are small, searchable and diffable in one query; large
    -- binaries go through the existing attachment entity; links carry no body.
    content TEXT NOT NULL DEFAULT '',
    attachment_id UUID,
    url TEXT NOT NULL DEFAULT '',
    CONSTRAINT workflow_artifact_carrier_check CHECK (
        (kind = 'document' AND attachment_id IS NULL AND url = ''
            AND octet_length(content) <= 1048576)
        OR (kind = 'attachment' AND attachment_id IS NOT NULL AND content = '' AND url = '')
        OR (kind = 'link' AND attachment_id IS NULL AND content = '' AND url <> '')
    ),

    -- Overwriting an approved artifact returns it to 'submitted', which is how
    -- an edit invalidates the approval it was granted under.
    review_status TEXT NOT NULL DEFAULT 'submitted'
        CHECK (review_status IN ('submitted', 'approved', 'rejected')),
    review_comment TEXT NOT NULL DEFAULT '',
    reviewed_by UUID,
    reviewed_at TIMESTAMPTZ,

    submitted_by_type TEXT NOT NULL
        CHECK (submitted_by_type IN ('member', 'agent', 'squad', 'system')),
    submitted_by_id UUID,

    -- NULL marks the row the product reads; non-NULL marks retained history.
    superseded_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
