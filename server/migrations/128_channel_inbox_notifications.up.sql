-- Channel-aware inbox notification state for Feishu/Lark outbound DMs.
--
-- The generic Feishu integration moved installation/user-binding state from
-- lark_* to channel_* in migration 124. The inbox DM notifier also needs its
-- own channel-backed delivery/idempotency tables; otherwise new installations
-- that exist only in channel_* cannot claim or merge outbound inbox cards.

CREATE TABLE channel_inbox_notification_delivery (
    inbox_item_id   UUID NOT NULL,
    installation_id UUID NOT NULL,
    channel_type    TEXT NOT NULL,
    channel_user_id TEXT NOT NULL,
    claimed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (inbox_item_id, installation_id, channel_type, channel_user_id)
);

INSERT INTO channel_inbox_notification_delivery (
    inbox_item_id,
    installation_id,
    channel_type,
    channel_user_id,
    claimed_at
)
SELECT
    inbox_item_id,
    installation_id,
    'feishu',
    lark_open_id,
    claimed_at
FROM lark_inbox_notification_delivery
ON CONFLICT DO NOTHING;

CREATE TABLE channel_inbox_issue_card (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id             UUID NOT NULL,
    recipient_id             UUID NOT NULL,
    issue_id                 UUID NOT NULL,
    installation_id          UUID NOT NULL,
    channel_type             TEXT NOT NULL,
    channel_user_id          TEXT NOT NULL,
    channel_card_message_id  TEXT NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (
        workspace_id,
        recipient_id,
        issue_id,
        installation_id,
        channel_type,
        channel_user_id
    )
);

CREATE INDEX idx_channel_inbox_issue_card_issue
    ON channel_inbox_issue_card(workspace_id, issue_id, updated_at DESC);

INSERT INTO channel_inbox_issue_card (
    id,
    workspace_id,
    recipient_id,
    issue_id,
    installation_id,
    channel_type,
    channel_user_id,
    channel_card_message_id,
    created_at,
    updated_at
)
SELECT
    id,
    workspace_id,
    recipient_id,
    issue_id,
    installation_id,
    'feishu',
    lark_open_id,
    lark_card_message_id,
    created_at,
    updated_at
FROM lark_inbox_issue_card
ON CONFLICT DO NOTHING;
