package lark

import "github.com/jackc/pgx/v5/pgtype"

// Domain parameter types for the channel-backed Feishu store. They replace the
// retired db.*LarkParams shapes generated from queries/lark.sql, using the same
// channel-neutral field names as the domain entities in store.go. The store
// (channel_store.go) maps them onto the channel_* writes, folding the
// feishu-specific identifiers into the JSONB config at the DB boundary.

// GetInstallationInWorkspaceParams scopes an installation lookup to a workspace.
type GetInstallationInWorkspaceParams struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
}

// UpsertInstallationParams carries the flat feishu installation fields for an
// install / re-install.
type UpsertInstallationParams struct {
	WorkspaceID        pgtype.UUID
	AgentID            pgtype.UUID
	AppID              string
	AppSecretEncrypted []byte
	BotOpenID          string
	InstallerUserID    pgtype.UUID
	TenantKey          pgtype.Text
	BotUnionID         pgtype.Text
	Region             string
}

// SetInstallationStatusParams flips an installation's status (active/revoked).
type SetInstallationStatusParams struct {
	ID     pgtype.UUID
	Status string
}

// SetInstallationBotUnionIDParams records the bot's union_id (backfill).
type SetInstallationBotUnionIDParams struct {
	ID         pgtype.UUID
	BotUnionID pgtype.Text
}

// AcquireWSLeaseParams fences the WS supervisor lease for an installation.
type AcquireWSLeaseParams struct {
	NewToken     pgtype.Text
	NewExpiresAt pgtype.Timestamptz
	ID           pgtype.UUID
}

// ReleaseWSLeaseParams releases a WS supervisor lease the caller still holds.
type ReleaseWSLeaseParams struct {
	ID           pgtype.UUID
	CurrentToken pgtype.Text
}

// GetUserBindingByOpenIDParams looks up a binding by its channel-native user id.
type GetUserBindingByOpenIDParams struct {
	InstallationID pgtype.UUID
	ChannelUserID  string
}

// CreateUserBindingParams binds a workspace member to a channel-native user id.
type CreateUserBindingParams struct {
	WorkspaceID    pgtype.UUID
	MulticaUserID  pgtype.UUID
	InstallationID pgtype.UUID
	ChannelUserID  string
	UnionID        pgtype.Text
}

// ListInboxNotificationBindingsParams looks up all active Feishu bindings for
// a Multica member recipient in one workspace.
type ListInboxNotificationBindingsParams struct {
	WorkspaceID   pgtype.UUID
	MulticaUserID pgtype.UUID
}

// ClaimInboxNotificationDeliveryParams claims one direct inbox notification
// delivery for a concrete Feishu installation + user.
type ClaimInboxNotificationDeliveryParams struct {
	InboxItemID    pgtype.UUID
	InstallationID pgtype.UUID
	ChannelUserID  string
}

// GetInboxIssueCardParams locates the merged issue card for a recipient.
type GetInboxIssueCardParams struct {
	WorkspaceID    pgtype.UUID
	RecipientID    pgtype.UUID
	IssueID        pgtype.UUID
	InstallationID pgtype.UUID
	ChannelUserID  string
}

// UpsertInboxIssueCardParams records the latest merged issue card message id.
type UpsertInboxIssueCardParams struct {
	WorkspaceID          pgtype.UUID
	RecipientID          pgtype.UUID
	IssueID              pgtype.UUID
	InstallationID       pgtype.UUID
	ChannelUserID        string
	ChannelCardMessageID string
}

// ListInboxIssueCardItemsParams loads mergeable inbox items for one issue card.
type ListInboxIssueCardItemsParams struct {
	WorkspaceID pgtype.UUID
	RecipientID pgtype.UUID
	IssueID     pgtype.UUID
	Types       []string
}

// GetChatSessionBindingParams looks up a chat binding by its channel chat id.
type GetChatSessionBindingParams struct {
	InstallationID pgtype.UUID
	ChannelChatID  string
}

// UpdateChatSessionBindingReplyTargetParams records the latest inbound trigger
// message + thread so the outbound patcher can thread its reply.
type UpdateChatSessionBindingReplyTargetParams struct {
	ChatSessionID pgtype.UUID
	LastMessageID pgtype.Text
	LastThreadID  pgtype.Text
}

// ClaimInboundDedupParams claims the two-phase idempotency row for a message.
type ClaimInboundDedupParams struct {
	InstallationID pgtype.UUID
	MessageID      string
}

// MarkInboundDedupProcessedParams marks a claimed message processed (fenced).
type MarkInboundDedupProcessedParams struct {
	InstallationID pgtype.UUID
	MessageID      string
	ClaimToken     pgtype.UUID
}

// ReleaseInboundDedupParams releases a claim on processing failure (fenced).
type ReleaseInboundDedupParams struct {
	InstallationID pgtype.UUID
	MessageID      string
	ClaimToken     pgtype.UUID
}

// RecordInboundDropParams writes a non-content drop audit row.
type RecordInboundDropParams struct {
	EventType        string
	DropReason       string
	InstallationID   pgtype.UUID
	ChannelChatID    pgtype.Text
	ChannelEventID   pgtype.Text
	ChannelMessageID pgtype.Text
}

// CreateBindingTokenParams mints a short-lived channel binding token.
type CreateBindingTokenParams struct {
	TokenHash      string
	WorkspaceID    pgtype.UUID
	InstallationID pgtype.UUID
	ChannelUserID  string
	ExpiresAt      pgtype.Timestamptz
}

// CreateOutboundCardMessageParams records an outbound card for a task/session.
type CreateOutboundCardMessageParams struct {
	ChatSessionID        pgtype.UUID
	ChannelChatID        string
	ChannelCardMessageID string
	Status               string
	TaskID               pgtype.UUID
	StartDelaySeconds    float64
}

type ProjectOutboundTaskMessageParams struct {
	TaskID             pgtype.UUID
	Seq                int32
	VisibleTextAppend  string
	CurrentStage       string
	FilesReadDelta     int32
	FilesEditedDelta   int32
	SearchesDelta      int32
	CommandsDelta      int32
	MinIntervalSeconds float64
}

type ScheduleOutboundTaskMessageParams struct {
	TaskID             pgtype.UUID
	MinIntervalSeconds float64
}

type SetOutboundTerminalDesiredParams struct {
	TaskID          pgtype.UUID
	Status          string
	TerminalContent string
}

type ClaimOutboundCardDeliveryParams struct {
	TaskID       pgtype.UUID
	LeaseToken   pgtype.UUID
	LeaseSeconds float64
}

type SetOutboundInflightPayloadParams struct {
	ID              pgtype.UUID
	LeaseToken      pgtype.UUID
	DesiredRevision int64
	CardJSON        string
}

type SetOutboundCardEntityIDParams struct {
	ID            pgtype.UUID
	LeaseToken    pgtype.UUID
	ChannelCardID string
}

type SetOutboundCardMessageIDParams struct {
	ID                   pgtype.UUID
	LeaseToken           pgtype.UUID
	ChannelCardMessageID string
}

type OutboundDeliveryLeaseParams struct {
	ID         pgtype.UUID
	LeaseToken pgtype.UUID
}

type FailOutboundCardDeliveryParams struct {
	ID           pgtype.UUID
	LeaseToken   pgtype.UUID
	LastError    string
	RetrySeconds float64
}

type AbandonOutboundCardDeliveryParams struct {
	ID         pgtype.UUID
	LeaseToken pgtype.UUID
	LastError  string
}
