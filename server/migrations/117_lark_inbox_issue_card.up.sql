CREATE TABLE lark_inbox_issue_card (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id          UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    recipient_id          UUID NOT NULL,
    issue_id              UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    installation_id       UUID NOT NULL REFERENCES lark_installation(id) ON DELETE CASCADE,
    lark_open_id          TEXT NOT NULL,
    lark_card_message_id  TEXT NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, recipient_id, issue_id, installation_id, lark_open_id)
);

CREATE INDEX idx_lark_inbox_issue_card_issue
    ON lark_inbox_issue_card(workspace_id, issue_id, updated_at DESC);
