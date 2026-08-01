package lark

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type fakePatcherQueries struct {
	mu                   sync.Mutex
	now                  func() time.Time
	task                 db.AgentTaskQueue
	taskErr              error
	taskChannelIngested  bool
	binding              ChatSessionBinding
	bindingErr           error
	installation         Installation
	installationErr      error
	agent                db.Agent
	agentErr             error
	bindings             []InboxNotificationBinding
	bindingsErr          error
	card                 OutboundCardMessage
	cardErr              error
	taskMessages         []db.TaskMessage
	taskMessagesErr      error
	created              []CreateOutboundCardMessageParams
	createReturn         OutboundCardMessage
	readyUpdates         []SetOutboundCardMessageIDParams
	claimPatch           bool
	setPayloadErr        error
	failDeliveryCalls    []FailOutboundCardDeliveryParams
	abandonDeliveryCalls []AbandonOutboundCardDeliveryParams
	statusUpdates        []string
	askMessageUpdates    []db.UpdateChatAskChannelMessageParams
}

func (f *fakePatcherQueries) GetAgentTask(ctx context.Context, id pgtype.UUID) (db.AgentTaskQueue, error) {
	return f.task, f.taskErr
}
func (f *fakePatcherQueries) TaskHasChannelIngestedMessages(ctx context.Context, taskID pgtype.UUID) (bool, error) {
	return f.taskChannelIngested, nil
}
func (f *fakePatcherQueries) GetChatSession(ctx context.Context, id pgtype.UUID) (db.ChatSession, error) {
	return db.ChatSession{}, nil
}
func (f *fakePatcherQueries) GetAgent(ctx context.Context, id pgtype.UUID) (db.Agent, error) {
	return f.agent, f.agentErr
}
func (f *fakePatcherQueries) GetLarkInstallation(ctx context.Context, id pgtype.UUID) (Installation, error) {
	return f.installation, f.installationErr
}
func (f *fakePatcherQueries) GetLarkChatSessionBindingBySession(ctx context.Context, sessID pgtype.UUID) (ChatSessionBinding, error) {
	return f.binding, f.bindingErr
}
func (f *fakePatcherQueries) ListActiveLarkUserBindingsByMember(ctx context.Context, arg ListInboxNotificationBindingsParams) ([]InboxNotificationBinding, error) {
	return f.bindings, f.bindingsErr
}
func (f *fakePatcherQueries) GetLarkOutboundCardByTask(ctx context.Context, taskID pgtype.UUID) (OutboundCardMessage, error) {
	return f.card, f.cardErr
}
func (f *fakePatcherQueries) ListTaskMessagesSince(ctx context.Context, arg db.ListTaskMessagesSinceParams) ([]db.TaskMessage, error) {
	if f.taskMessagesErr != nil {
		return nil, f.taskMessagesErr
	}
	var messages []db.TaskMessage
	for _, message := range f.taskMessages {
		if message.Seq > arg.Seq {
			messages = append(messages, message)
		}
	}
	return messages, nil
}
func (f *fakePatcherQueries) CreateLarkOutboundCardMessage(ctx context.Context, arg CreateOutboundCardMessageParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, arg)
	if f.cardErr == nil {
		return OutboundCardMessage{}, pgx.ErrNoRows
	}
	created := f.createReturn
	if !created.ID.Valid {
		created.ID = uuidFromStringNoTest("dddddddd-dddd-dddd-dddd-dddddddddddd")
	}
	created.ChatSessionID = arg.ChatSessionID
	created.TaskID = arg.TaskID
	created.ChannelChatID = arg.ChannelChatID
	created.ChannelCardMessageID = arg.ChannelCardMessageID
	created.Status = arg.Status
	created.Transport = "legacy"
	created.DesiredRevision = 1
	created.NextAttemptAt = pgtype.Timestamptz{Time: time.Now().Add(time.Duration(arg.StartDelaySeconds * float64(time.Second))), Valid: true}
	f.card = created
	f.cardErr = nil
	return created, nil
}

func (f *fakePatcherQueries) ProjectLarkOutboundTaskMessage(ctx context.Context, arg ProjectOutboundTaskMessageParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cardErr != nil || f.card.Status == string(CardStatusFinal) || f.card.Status == string(CardStatusError) || f.card.ProjectedSeq >= arg.Seq {
		return OutboundCardMessage{}, pgx.ErrNoRows
	}
	f.card.ProjectedSeq = arg.Seq
	f.card.VisibleText += arg.VisibleTextAppend
	f.card.CurrentStage = arg.CurrentStage
	f.card.FilesReadCount += arg.FilesReadDelta
	f.card.FilesEditedCount += arg.FilesEditedDelta
	f.card.SearchesCount += arg.SearchesDelta
	f.card.CommandsCount += arg.CommandsDelta
	f.card.DesiredRevision++
	return f.card, nil
}

func (f *fakePatcherQueries) ScheduleLarkOutboundTaskMessage(ctx context.Context, arg ScheduleOutboundTaskMessageParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cardErr != nil || f.card.Status == string(CardStatusFinal) || f.card.Status == string(CardStatusError) {
		return OutboundCardMessage{}, pgx.ErrNoRows
	}
	if f.card.ChannelCardMessageID != "" {
		due := time.Now().Add(time.Duration(arg.MinIntervalSeconds * float64(time.Second)))
		if !f.card.NextAttemptAt.Valid || due.Before(f.card.NextAttemptAt.Time) {
			f.card.NextAttemptAt = pgtype.Timestamptz{Time: due, Valid: true}
		}
	}
	return f.card, nil
}

func (f *fakePatcherQueries) SetLarkOutboundTerminalDesired(ctx context.Context, arg SetOutboundTerminalDesiredParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cardErr != nil || f.card.Status == string(CardStatusFinal) || f.card.Status == string(CardStatusError) {
		return OutboundCardMessage{}, pgx.ErrNoRows
	}
	f.card.Status = arg.Status
	f.card.TerminalContent = arg.TerminalContent
	f.card.DesiredRevision++
	f.statusUpdates = append(f.statusUpdates, arg.Status)
	if f.card.ChannelCardID == "" && f.card.ChannelCardMessageID == "" && !f.card.LeaseToken.Valid {
		f.card.AppliedRevision = f.card.DesiredRevision
	}
	return f.card, nil
}

func (f *fakePatcherQueries) ClaimLarkOutboundCardDelivery(ctx context.Context, arg ClaimOutboundCardDeliveryParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hasUnprojected := false
	for _, message := range f.taskMessages {
		if message.Seq > f.card.ProjectedSeq {
			hasUnprojected = true
			break
		}
	}
	now := time.Now
	if f.now != nil {
		now = f.now
	}
	if !f.claimPatch || f.cardErr != nil || f.card.DeliveryFailedAt.Valid ||
		(f.card.DesiredRevision <= f.card.AppliedRevision && !hasUnprojected) || f.card.LeaseToken.Valid ||
		(f.card.NextAttemptAt.Valid && f.card.NextAttemptAt.Time.After(now())) {
		return OutboundCardMessage{}, pgx.ErrNoRows
	}
	f.card.LeaseToken = arg.LeaseToken
	if !f.card.InflightRevision.Valid {
		f.card.InflightRevision = pgtype.Int8{Int64: f.card.DesiredRevision, Valid: true}
	}
	return f.card, nil
}

func (f *fakePatcherQueries) SetLarkOutboundInflightPayload(ctx context.Context, arg SetOutboundInflightPayloadParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setPayloadErr != nil {
		return OutboundCardMessage{}, f.setPayloadErr
	}
	if f.card.InflightCardJSON == "" {
		f.card.InflightRevision = pgtype.Int8{Int64: arg.DesiredRevision, Valid: true}
		if f.card.Transport == "cardkit" && f.card.ChannelCardMessageID != "" {
			f.card.OperationSequence++
			f.card.InflightSequence = pgtype.Int4{Int32: f.card.OperationSequence, Valid: true}
		}
		f.card.InflightCardJSON = arg.CardJSON
	}
	return f.card, nil
}

func (f *fakePatcherQueries) SetLarkOutboundCardEntityID(ctx context.Context, arg SetOutboundCardEntityIDParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.card.ChannelCardID = arg.ChannelCardID
	return f.card, nil
}

func (f *fakePatcherQueries) SetLarkOutboundCardMessageID(ctx context.Context, arg SetOutboundCardMessageIDParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.card.ChannelCardMessageID = arg.ChannelCardMessageID
	f.readyUpdates = append(f.readyUpdates, arg)
	return f.card, nil
}

func (f *fakePatcherQueries) DowngradeLarkOutboundCardTransport(ctx context.Context, arg OutboundDeliveryLeaseParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.card.Transport = "legacy"
	f.card.ChannelCardID = ""
	return f.card, nil
}

func (f *fakePatcherQueries) CompleteLarkOutboundCardDelivery(ctx context.Context, arg OutboundDeliveryLeaseParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.card.AppliedRevision = f.card.InflightRevision.Int64
	if f.card.Status == string(CardStatusPending) {
		f.card.Status = string(CardStatusStreaming)
	}
	f.card.InflightRevision = pgtype.Int8{}
	f.card.InflightSequence = pgtype.Int4{}
	f.card.InflightCardJSON = ""
	f.card.LeaseToken = pgtype.UUID{}
	f.card.NextAttemptAt = pgtype.Timestamptz{}
	f.card.DeliveryFailedAt = pgtype.Timestamptz{}
	return f.card, nil
}

func (f *fakePatcherQueries) FailLarkOutboundCardDelivery(ctx context.Context, arg FailOutboundCardDeliveryParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failDeliveryCalls = append(f.failDeliveryCalls, arg)
	f.card.LeaseToken = pgtype.UUID{}
	f.card.AttemptCount++
	f.card.LastError = arg.LastError
	now := time.Now
	if f.now != nil {
		now = f.now
	}
	f.card.NextAttemptAt = pgtype.Timestamptz{Time: now().Add(time.Duration(arg.RetrySeconds * float64(time.Second))), Valid: true}
	return f.card, nil
}
func (f *fakePatcherQueries) AbandonLarkOutboundCardDelivery(ctx context.Context, arg AbandonOutboundCardDeliveryParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abandonDeliveryCalls = append(f.abandonDeliveryCalls, arg)
	f.card.LeaseToken = pgtype.UUID{}
	f.card.AttemptCount++
	f.card.LastError = arg.LastError
	f.card.DeliveryFailedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.card.NextAttemptAt = pgtype.Timestamptz{}
	return f.card, nil
}
func (f *fakePatcherQueries) UpdateChatAskChannelMessage(ctx context.Context, arg db.UpdateChatAskChannelMessageParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.askMessageUpdates = append(f.askMessageUpdates, arg)
	return nil
}

type fakeCredentials struct{ secret string }

func (f fakeCredentials) DecryptAppSecret(inst Installation) (string, error) {
	return f.secret, nil
}

type fakeAPIClient struct {
	mu             sync.Mutex
	sent           []SendCardParams
	directSent     []SendDirectCardParams
	patched        []PatchCardParams
	textSent       []SendTextParams
	directTextSent []SendDirectTextParams
	mdCardSent     []SendMarkdownCardParams
	sendReturn     string
	sendErr        error
	patchErr       error
	textSendErr    error
	textSendReturn string
	mdCardErr      error
	mdCardReturn   string
	bindingSent    []BindingPromptParams
	// threadReplyErr, when non-nil, is returned by the three send
	// methods whenever the call carries a thread ReplyTarget, while the
	// attempt is still recorded. Tests inject either a classified
	// *APIError (to exercise the chat-level fallback) or an ambiguous
	// transport error (to assert no fallback happens).
	threadReplyErr error
}

// errThreadReplyClassified is a Lark business error the fallback path
// recognizes (230071 = group does not support reply in thread), so a
// thread send that returns it triggers the chat-level retry.
var errThreadReplyClassified = &APIError{Op: "send text message", Code: 230071, Msg: "group does not support reply in thread"}

// errThreadReplyTransport is an ambiguous, non-classified failure: the
// fallback path must NOT retry it at chat level.
var errThreadReplyTransport = errors.New("fake: transport failure")

func (f *fakeAPIClient) IsConfigured() bool { return true }

func (f *fakeAPIClient) SendInteractiveCard(ctx context.Context, p SendCardParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, p)
	if f.threadReplyErr != nil && p.ReplyTarget.IsSet() {
		return "", f.threadReplyErr
	}
	return f.sendReturn, f.sendErr
}
func (f *fakeAPIClient) SendDirectInteractiveCard(ctx context.Context, p SendDirectCardParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.directSent = append(f.directSent, p)
	return f.sendReturn, f.sendErr
}
func (f *fakeAPIClient) PatchInteractiveCard(ctx context.Context, p PatchCardParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patched = append(f.patched, p)
	return f.patchErr
}
func (f *fakeAPIClient) SendTextMessage(ctx context.Context, p SendTextParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.textSent = append(f.textSent, p)
	if f.threadReplyErr != nil && p.ReplyTarget.IsSet() {
		return "", f.threadReplyErr
	}
	return f.textSendReturn, f.textSendErr
}
func (f *fakeAPIClient) SendDirectTextMessage(ctx context.Context, p SendDirectTextParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.directTextSent = append(f.directTextSent, p)
	return f.textSendReturn, f.textSendErr
}
func (f *fakeAPIClient) SendMarkdownCard(ctx context.Context, p SendMarkdownCardParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mdCardSent = append(f.mdCardSent, p)
	if f.threadReplyErr != nil && p.ReplyTarget.IsSet() {
		return "", f.threadReplyErr
	}
	return f.mdCardReturn, f.mdCardErr
}
func (f *fakeAPIClient) SendBindingPromptCard(ctx context.Context, p BindingPromptParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindingSent = append(f.bindingSent, p)
	return nil
}
func (f *fakeAPIClient) GetBotInfo(ctx context.Context, creds InstallationCredentials) (BotInfo, error) {
	return BotInfo{}, nil
}
func (f *fakeAPIClient) GetMessage(ctx context.Context, creds InstallationCredentials, messageID string) ([]LarkMessage, error) {
	return nil, nil
}
func (f *fakeAPIClient) ListChatMessages(ctx context.Context, creds InstallationCredentials, p ListMessagesParams) ([]LarkMessage, error) {
	return nil, nil
}
func (f *fakeAPIClient) DownloadMessageResource(ctx context.Context, creds InstallationCredentials, p DownloadResourceParams) (DownloadedResource, error) {
	return DownloadedResource{}, nil
}
func (f *fakeAPIClient) BatchGetUsers(ctx context.Context, creds InstallationCredentials, openIDs []string) (map[string]string, error) {
	return nil, nil
}
func (f *fakeAPIClient) AddMessageReaction(ctx context.Context, p AddReactionParams) (string, error) {
	return "fake-reaction-id", nil
}
func (f *fakeAPIClient) DeleteMessageReaction(ctx context.Context, p DeleteReactionParams) error {
	return nil
}

func newTestPatcher(t *testing.T) (*Patcher, *fakePatcherQueries, *fakeAPIClient) {
	t.Helper()
	q := &fakePatcherQueries{
		binding: ChatSessionBinding{
			ChatSessionID:  uuidFromString(t, "cccccccc-cccc-cccc-cccc-cccccccccccc"),
			InstallationID: uuidFromString(t, "1111aaaa-1111-1111-1111-111111111111"),
			ChannelChatID:  "oc_test_chat",
			ChatType:       "p2p",
		},
		installation: Installation{
			ID:                 uuidFromString(t, "1111aaaa-1111-1111-1111-111111111111"),
			WorkspaceID:        uuidFromString(t, "2222aaaa-2222-2222-2222-222222222222"),
			AppID:              "cli_test_app",
			AppSecretEncrypted: []byte("ciphertext"),
			Status:             string(InstallationActive),
			AgentID:            uuidFromString(t, "aaaa1111-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		},
		agent:      db.Agent{Name: "TestAgent"},
		cardErr:    pgx.ErrNoRows,
		claimPatch: true,
	}
	api := &fakeAPIClient{sendReturn: "lark_card_msg_1", textSendReturn: "lark_text_msg_1"}
	p := NewPatcher(q, fakeCredentials{secret: "shh"}, api, PatcherConfig{
		Logger: newDiscardLogger(),
		Now:    time.Now,
	})
	return p, q, api
}

func uuidFromStringNoTest(s string) pgtype.UUID {
	var id pgtype.UUID
	_ = id.Scan(s)
	return id
}

// TestPatcherSendsPlainTextOnChatDone pins the new behaviour Bohan asked
// for: when the agent finishes replying, the Patcher posts the reply as
// a plain Lark IM text message (msg_type=text), not nested inside an
// interactive card. This is the load-bearing UX call — the prior card
// chrome made every reply look like a system notification.
// TestPatcherSendsSealedChannelTaskReply is the other half of the boundary:
// a sealed channel task owns an input batch exactly like a direct task, so
// gating outbound on owner presence alone would silently drop every channel
// reply. Channel provenance must let the reply through.
func TestPatcherSendsSealedChannelTaskReply(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee555555-ee55-ee55-ee55-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ChatInputTaskID: taskID}
	q.taskChannelIngested = true

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{
			TaskID:        uuidString(taskID),
			ChatSessionID: uuidString(q.binding.ChatSessionID),
			Content:       "channel answer",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 || api.textSent[0].Text != "channel answer" {
		t.Fatalf("sealed channel reply must reach Lark; textSent=%+v", api.textSent)
	}
}

func TestPatcherSendsPlainTextOnChatDone(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee333333-ee33-ee33-ee33-eeeeeeeeeeee")

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{
			TaskID:        uuidString(taskID),
			ChatSessionID: uuidString(q.binding.ChatSessionID),
			Content:       "Hello! I'm cc, a coding agent…",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 {
		t.Fatalf("expected one SendTextMessage call on ChatDone; got %d", len(api.textSent))
	}
	got := api.textSent[0]
	if got.Text != "Hello! I'm cc, a coding agent…" {
		t.Errorf("text mismatch: got %q", got.Text)
	}
	if got.ChatID != ChatID(q.binding.ChannelChatID) {
		t.Errorf("chat_id mismatch: got %q want %q", got.ChatID, q.binding.ChannelChatID)
	}
	if got.InstallationID.AppID != "cli_test_app" {
		t.Errorf("expected installation app_id propagated; got %q", got.InstallationID.AppID)
	}
	if len(api.sent) != 0 || len(api.patched) != 0 {
		t.Errorf("ChatDone must NOT send / patch any card; got sent=%d patched=%d",
			len(api.sent), len(api.patched))
	}
}

func TestPatcherDoesNotCreateStreamingCardBeforeDelay(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	p, q, api := newTestPatcher(t)
	p.cfg.Now = func() time.Time { return now }
	taskID := uuidFromString(t, "ee100001-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{
		ID:              taskID,
		ChatSessionID:   q.binding.ChatSessionID,
		ChatInputTaskID: taskID,
		CreatedAt:       pgtype.Timestamptz{Time: now.Add(-6 * time.Second), Valid: true},
	}
	q.taskChannelIngested = true

	p.handleEvent(events.Event{
		Type:   protocol.EventTaskMessage,
		TaskID: uuidString(taskID),
		Payload: protocol.TaskMessagePayload{
			TaskID:  uuidString(taskID),
			Seq:     1,
			Type:    "text",
			Content: "partial",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 0 || len(api.patched) != 0 {
		t.Fatalf("fast task must stay native and card-free; sent=%d patched=%d", len(api.sent), len(api.patched))
	}
	if len(q.created) != 1 || q.created[0].StartDelaySeconds <= 0 {
		t.Fatalf("fast task must persist only a future durable schedule; created=%+v", q.created)
	}
}

func TestPatcherSchedulesCardAtDelayWhenRunGoesQuiet(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	p, q, api := newTestPatcher(t)
	p.cfg.Now = func() time.Time { return now }
	taskID := uuidFromString(t, "ee100013-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{
		ID:              taskID,
		ChatSessionID:   q.binding.ChatSessionID,
		ChatInputTaskID: taskID,
		Status:          "running",
		CreatedAt:       pgtype.Timestamptz{Time: now, Valid: true},
	}
	q.taskChannelIngested = true

	bus := events.New()
	p.Register(bus)
	bus.Publish(events.Event{
		Type:          protocol.EventTaskRunning,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
	})

	if len(q.created) != 1 || q.created[0].StartDelaySeconds != 7 {
		t.Fatalf("durable schedule=%+v, want one row due in 7s", q.created)
	}
	api.mu.Lock()
	if len(api.sent) != 0 {
		api.mu.Unlock()
		t.Fatalf("running event must not show a card before the delay")
	}
	api.mu.Unlock()

	q.card.NextAttemptAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true}
	p.RunOnce(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 || api.sent[0].IdempotencyKey != "stream-"+uuidString(taskID) {
		t.Fatalf("quiet long run must get one delayed card; sent=%+v", api.sent)
	}
}

func TestPatcherScheduledCardRechecksTerminalStatus(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	p, q, api := newTestPatcher(t)
	p.cfg.Now = func() time.Time { return now }
	taskID := uuidFromString(t, "ee100014-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{
		ID:              taskID,
		ChatSessionID:   q.binding.ChatSessionID,
		ChatInputTaskID: taskID,
		Status:          "running",
		CreatedAt:       pgtype.Timestamptz{Time: now, Valid: true},
	}
	q.taskChannelIngested = true

	p.handleEvent(events.Event{Type: protocol.EventTaskRunning, TaskID: uuidString(taskID)})
	q.task.Status = "completed"
	p.handleEvent(events.Event{
		Type: protocol.EventChatDone, TaskID: uuidString(taskID),
		Payload: protocol.ChatDonePayload{TaskID: uuidString(taskID), ChatSessionID: uuidString(q.binding.ChatSessionID), Content: "快速完成"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 0 || len(api.patched) != 0 || len(api.textSent) != 1 {
		t.Fatalf("completed task must keep native final: sent=%d patched=%d text=%d", len(api.sent), len(api.patched), len(api.textSent))
	}
}

func TestPatcherDoesNotResurrectTerminalTaskFromDelayedMessageEvent(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	p, q, api := newTestPatcher(t)
	p.cfg.Now = func() time.Time { return now }
	taskID := uuidFromString(t, "ee100010-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{
		ID:              taskID,
		ChatSessionID:   q.binding.ChatSessionID,
		ChatInputTaskID: taskID,
		Status:          "completed",
		CreatedAt:       pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true},
	}
	q.taskChannelIngested = true

	p.handleEvent(events.Event{Type: protocol.EventTaskMessage, TaskID: uuidString(taskID)})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 0 || len(api.patched) != 0 || len(q.created) != 0 {
		t.Fatalf("delayed message event resurrected terminal task: sent=%d patched=%d created=%d", len(api.sent), len(api.patched), len(q.created))
	}
}

func TestPatcherCreatesOneIdempotentCardForSlowTask(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	p, q, api := newTestPatcher(t)
	p.cfg.Now = func() time.Time { return now }
	taskID := uuidFromString(t, "ee100002-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{
		ID:              taskID,
		ChatSessionID:   q.binding.ChatSessionID,
		ChatInputTaskID: taskID,
		CreatedAt:       pgtype.Timestamptz{Time: now.Add(-11 * time.Second), Valid: true},
	}
	q.taskChannelIngested = true

	event := events.Event{
		Type:   protocol.EventTaskMessage,
		TaskID: uuidString(taskID),
		Payload: protocol.TaskMessagePayload{
			TaskID: uuidString(taskID),
			Seq:    1,
			Type:   "thinking",
		},
	}
	p.handleEvent(event)
	p.handleEvent(event)

	api.mu.Lock()
	if len(api.sent) != 0 || len(api.patched) != 0 {
		api.mu.Unlock()
		t.Fatalf("task-message event performed remote I/O: sent=%d patched=%d", len(api.sent), len(api.patched))
	}
	api.mu.Unlock()

	q.taskMessages = []db.TaskMessage{{Seq: 1, Type: "thinking"}}
	q.card.NextAttemptAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true}
	p.RunOnce(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("duplicate task events must create one card; sent=%d", len(api.sent))
	}
	if got, want := api.sent[0].IdempotencyKey, "stream-"+uuidString(taskID); got != want {
		t.Fatalf("stream card idempotency key=%q want %q", got, want)
	}
	if len(q.created) != 1 || q.created[0].ChannelCardMessageID != "" {
		t.Fatalf("expected one durable placeholder before remote send; created=%+v", q.created)
	}
	if len(q.readyUpdates) != 1 || q.readyUpdates[0].ChannelCardMessageID != api.sendReturn {
		t.Fatalf("expected card message id to be persisted after send; updates=%+v", q.readyUpdates)
	}
}

func TestPatcherRetriesUncertainInitialCardSendWithSameIdempotencyKey(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	p, q, api := newTestPatcher(t)
	p.cfg.Now = func() time.Time { return now }
	q.now = p.cfg.Now
	taskID := uuidFromString(t, "ee100006-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{
		ID:              taskID,
		ChatSessionID:   q.binding.ChatSessionID,
		ChatInputTaskID: taskID,
		CreatedAt:       pgtype.Timestamptz{Time: now.Add(-11 * time.Second), Valid: true},
	}
	q.taskChannelIngested = true
	api.sendErr = errors.New("result uncertain")
	event := events.Event{Type: protocol.EventTaskMessage, TaskID: uuidString(taskID), Payload: protocol.TaskMessagePayload{
		TaskID: uuidString(taskID), Seq: 1, Type: "thinking",
	}}

	p.handleEvent(event)
	q.taskMessages = []db.TaskMessage{{Seq: 1, Type: "thinking"}}
	q.card.NextAttemptAt = pgtype.Timestamptz{Time: now.Add(-time.Second), Valid: true}
	p.RunOnce(context.Background())
	api.sendErr = nil
	now = now.Add(time.Second)
	p.RunOnce(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 2 {
		t.Fatalf("uncertain send must be retried; sent=%d", len(api.sent))
	}
	if api.sent[0].IdempotencyKey == "" || api.sent[0].IdempotencyKey != api.sent[1].IdempotencyKey {
		t.Fatalf("retry must reuse idempotency key; first=%q second=%q", api.sent[0].IdempotencyKey, api.sent[1].IdempotencyKey)
	}
	if api.sent[0].CardJSON != api.sent[1].CardJSON {
		t.Fatalf("idempotent retry must reuse identical content")
	}
	if len(q.created) != 1 || len(q.readyUpdates) != 1 {
		t.Fatalf("retry must reuse placeholder and persist one remote id; created=%d ready=%d", len(q.created), len(q.readyUpdates))
	}
}

func TestPatcherStreamsOnlyVisibleTextWithDatabaseThrottle(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	p, q, api := newTestPatcher(t)
	p.cfg.Now = func() time.Time { return now }
	taskID := uuidFromString(t, "ee100003-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{
		ID:              taskID,
		ChatSessionID:   q.binding.ChatSessionID,
		ChatInputTaskID: taskID,
		CreatedAt:       pgtype.Timestamptz{Time: now.Add(-30 * time.Second), Valid: true},
	}
	q.taskChannelIngested = true
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:                   uuidFromString(t, "dd100003-dd10-dd10-dd10-dddddddddddd"),
		ChatSessionID:        q.binding.ChatSessionID,
		TaskID:               taskID,
		ChannelChatID:        q.binding.ChannelChatID,
		ChannelCardMessageID: "om_streaming",
		Status:               string(CardStatusStreaming),
	}
	q.claimPatch = true
	q.taskMessages = []db.TaskMessage{
		{Seq: 1, Type: "text", Content: pgtype.Text{String: "第一段。", Valid: true}},
		{Seq: 2, Type: "tool_use", Tool: pgtype.Text{String: "exec_command", Valid: true}, Input: []byte(`{"cmd":"secret command"}`)},
		{Seq: 3, Type: "tool_result", Output: pgtype.Text{String: "secret output", Valid: true}},
		{Seq: 4, Type: "text", Content: pgtype.Text{String: "第二段。", Valid: true}},
	}
	if _, err := q.ProjectLarkOutboundTaskMessage(context.Background(), ProjectOutboundTaskMessageParams{
		TaskID: taskID, Seq: 1, VisibleTextAppend: "第一段。", CurrentStage: progressStageResponding,
	}); err != nil {
		t.Fatalf("seed first projection: %v", err)
	}

	p.handleEvent(events.Event{
		Type:   protocol.EventTaskMessage,
		TaskID: uuidString(taskID),
		Payload: protocol.TaskMessagePayload{
			TaskID:  uuidString(taskID),
			Seq:     4,
			Type:    "text",
			Content: "第二段。",
		},
	})
	q.card.NextAttemptAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true}
	p.RunOnce(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 {
		t.Fatalf("expected one throttled stream patch; patched=%d", len(api.patched))
	}
	body := api.patched[0].CardJSON
	for _, want := range []string{"第一段。", "第二段。"} {
		if !strings.Contains(body, want) {
			t.Errorf("stream card missing visible text %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{"secret command", "secret output", "exec_command"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("stream card leaked tool detail %q: %s", forbidden, body)
		}
	}
}

func TestPatcherWorkerRecoversPersistedMessagesMissingFromProjection(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100015-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{
		ID: taskID, ChatSessionID: q.binding.ChatSessionID, ChatInputTaskID: taskID,
		CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	}
	q.taskChannelIngested = true
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:                   uuidFromString(t, "dd100015-dd10-dd10-dd10-dddddddddddd"),
		ChatSessionID:        q.binding.ChatSessionID,
		TaskID:               taskID,
		ChannelChatID:        q.binding.ChannelChatID,
		ChannelCardMessageID: "om_recovery",
		Status:               string(CardStatusStreaming),
		Transport:            "legacy",
		DesiredRevision:      1,
		AppliedRevision:      1,
	}
	q.taskMessages = []db.TaskMessage{
		{Seq: 1, Type: "text", Content: pgtype.Text{String: "第一段。", Valid: true}},
		{Seq: 2, Type: "tool_use", Tool: pgtype.Text{String: "exec_command", Valid: true}},
		{Seq: 3, Type: "text", Content: pgtype.Text{String: "第二段。", Valid: true}},
	}

	p.RunOnce(context.Background())

	if q.card.ProjectedSeq != 3 || q.card.CommandsCount != 1 {
		t.Fatalf("reconciled projection=%+v", q.card)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 {
		t.Fatalf("recovered delivery patches=%d want 1", len(api.patched))
	}
	for _, want := range []string{"第一段。", "第二段。"} {
		if !strings.Contains(api.patched[0].CardJSON, want) {
			t.Errorf("recovered card missing %q: %s", want, api.patched[0].CardJSON)
		}
	}
}

func TestPatcherSkipsStreamPatchWhenAnotherReplicaOwnsThrottle(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	p, q, api := newTestPatcher(t)
	p.cfg.Now = func() time.Time { return now }
	taskID := uuidFromString(t, "ee100004-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{
		ID:              taskID,
		ChatSessionID:   q.binding.ChatSessionID,
		ChatInputTaskID: taskID,
		CreatedAt:       pgtype.Timestamptz{Time: now.Add(-30 * time.Second), Valid: true},
	}
	q.taskChannelIngested = true
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:                   uuidFromString(t, "dd100004-dd10-dd10-dd10-dddddddddddd"),
		ChannelCardMessageID: "om_streaming",
		Status:               string(CardStatusStreaming),
	}
	q.claimPatch = false

	p.handleEvent(events.Event{Type: protocol.EventTaskMessage, TaskID: uuidString(taskID)})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 0 {
		t.Fatalf("unclaimed replica must not patch; patched=%d", len(api.patched))
	}
}

func TestPatcherFinalizesExistingStreamCardWithoutSecondMessage(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100005-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ID: taskID, ChatSessionID: q.binding.ChatSessionID, ChatInputTaskID: taskID}
	q.taskChannelIngested = true
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:                   uuidFromString(t, "dd100005-dd10-dd10-dd10-dddddddddddd"),
		ChatSessionID:        q.binding.ChatSessionID,
		TaskID:               taskID,
		ChannelChatID:        q.binding.ChannelChatID,
		ChannelCardMessageID: "om_streaming",
		Status:               string(CardStatusStreaming),
	}

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{
			TaskID:        uuidString(taskID),
			ChatSessionID: uuidString(q.binding.ChatSessionID),
			Content:       "最终答案。",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 || !strings.Contains(api.patched[0].CardJSON, "最终答案。") {
		t.Fatalf("existing stream card must be finalized in place; patched=%+v", api.patched)
	}
	if len(api.textSent) != 0 || len(api.mdCardSent) != 0 || len(api.sent) != 0 {
		t.Fatalf("slow task final must not create a second message; text=%d markdown=%d cards=%d",
			len(api.textSent), len(api.mdCardSent), len(api.sent))
	}
	if len(q.statusUpdates) != 1 || q.statusUpdates[0] != string(CardStatusFinal) {
		t.Fatalf("stream card must reach final status; updates=%+v", q.statusUpdates)
	}
}

func TestPatcherKeepsNativeFinalWhenDurableScheduleWasNeverSent(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100011-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ID: taskID, ChatSessionID: q.binding.ChatSessionID, ChatInputTaskID: taskID}
	q.taskChannelIngested = true
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:            uuidFromString(t, "dd100011-dd10-dd10-dd10-dddddddddddd"),
		ChatSessionID: q.binding.ChatSessionID,
		TaskID:        taskID,
		Status:        string(CardStatusPending),
	}

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{
			TaskID:        uuidString(taskID),
			ChatSessionID: uuidString(q.binding.ChatSessionID),
			Content:       "最终答案。",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 0 || len(api.patched) != 0 || len(api.textSent) != 1 {
		t.Fatalf("unsent schedule must settle locally and keep native final; cards=%d patched=%d text=%d", len(api.sent), len(api.patched), len(api.textSent))
	}
}

func TestPatcherFinalizesExistingStreamCardAsConfirmationAction(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100009-ee10-ee10-ee10-eeeeeeeeeeee")
	requesterID := uuidFromString(t, "99999999-9999-9999-9999-999999999999")
	q.task = db.AgentTaskQueue{
		ID:              taskID,
		ChatSessionID:   q.binding.ChatSessionID,
		ChatInputTaskID: taskID,
		InitiatorUserID: requesterID,
	}
	q.taskChannelIngested = true
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:                   uuidFromString(t, "dd100009-dd10-dd10-dd10-dddddddddddd"),
		ChatSessionID:        q.binding.ChatSessionID,
		TaskID:               taskID,
		ChannelCardMessageID: "om_streaming",
		Status:               string(CardStatusStreaming),
	}
	q.bindings = []InboxNotificationBinding{
		{
			UserBinding: UserBinding{
				MulticaUserID:  requesterID,
				InstallationID: q.installation.ID,
				ChannelUserID:  "ou_requester",
			},
			Installation: q.installation,
		},
	}

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{
			TaskID:        uuidString(taskID),
			ChatSessionID: uuidString(q.binding.ChatSessionID),
			Content:       "项目：`测试`\n\n请回复“确认执行”，我再触发。",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 {
		t.Fatalf("slow confirmation must patch the existing card; patched=%d", len(api.patched))
	}
	for _, want := range []string{confirmationCardActionKind, confirmationMessageConfirm, "ou_requester"} {
		if !strings.Contains(api.patched[0].CardJSON, want) {
			t.Errorf("final confirmation card missing %q: %s", want, api.patched[0].CardJSON)
		}
	}
	if len(api.sent) != 0 || len(api.textSent) != 0 || len(api.mdCardSent) != 0 {
		t.Fatalf("slow confirmation must not create a second message")
	}
}

func TestPatcherFailurePatchesExistingStreamCard(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100007-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ID: taskID, ChatSessionID: q.binding.ChatSessionID, ChatInputTaskID: taskID}
	q.taskChannelIngested = true
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:                   uuidFromString(t, "dd100007-dd10-dd10-dd10-dddddddddddd"),
		ChannelCardMessageID: "om_streaming",
		Status:               string(CardStatusStreaming),
	}

	p.handleEvent(events.Event{
		Type:          protocol.EventTaskFailed,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       map[string]any{"error": "boom"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 || !strings.Contains(api.patched[0].CardJSON, "boom") {
		t.Fatalf("failure must patch the existing stream card; patched=%+v", api.patched)
	}
	if len(api.sent) != 0 {
		t.Fatalf("failure must not create a second card; sent=%d", len(api.sent))
	}
	if len(q.statusUpdates) != 1 || q.statusUpdates[0] != string(CardStatusError) {
		t.Fatalf("failure status updates=%+v", q.statusUpdates)
	}
}

func TestPatcherFailureKeepsOneShotCardWhenDurableScheduleWasNeverSent(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100012-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ID: taskID, ChatSessionID: q.binding.ChatSessionID, ChatInputTaskID: taskID}
	q.taskChannelIngested = true
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:            uuidFromString(t, "dd100012-dd10-dd10-dd10-dddddddddddd"),
		ChatSessionID: q.binding.ChatSessionID,
		TaskID:        taskID,
		Status:        string(CardStatusPending),
	}

	p.handleEvent(events.Event{
		Type:          protocol.EventTaskFailed,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       map[string]any{"error": "boom"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 || api.sent[0].IdempotencyKey != "" || !strings.Contains(api.sent[0].CardJSON, "boom") {
		t.Fatalf("unsent schedule must keep the one-shot error card; sent=%+v", api.sent)
	}
	if len(api.patched) != 0 {
		t.Fatalf("unsent schedule must not patch a nonexistent card; patched=%+v", api.patched)
	}
}

func TestPatcherCancellationSettlesExistingStreamCard(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100008-ee10-ee10-ee10-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ID: taskID, ChatSessionID: q.binding.ChatSessionID, ChatInputTaskID: taskID}
	q.taskChannelIngested = true
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:                   uuidFromString(t, "dd100008-dd10-dd10-dd10-dddddddddddd"),
		ChannelCardMessageID: "om_streaming",
		Status:               string(CardStatusStreaming),
	}

	p.handleEvent(events.Event{
		Type:          protocol.EventTaskCancelled,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 || !strings.Contains(api.patched[0].CardJSON, "已取消") {
		t.Fatalf("cancellation must settle the stream card; patched=%+v", api.patched)
	}
	if len(q.statusUpdates) != 1 || q.statusUpdates[0] != string(CardStatusFinal) {
		t.Fatalf("cancellation status updates=%+v", q.statusUpdates)
	}
}

// TestPatcherRoutesConfirmationPromptToActionCard pins the Patcher-level
// contract: explicit confirmation prompts in a channel reply render a Lark
// interactive card with buttons, not the normal text/markdown reply path.
func TestPatcherRoutesConfirmationPromptToActionCard(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantMessage string
	}{
		{
			name:        "quoted confirm",
			content:     "项目：`Satanpit`\n流水线：`私服更新重启-main`\n\n请回复“确认执行”，我再触发。",
			wantMessage: confirmationMessageConfirm,
		},
		{
			name:        "standalone dynamic confirm",
			content:     "项目：`测试`\n流水线：`测试后端发布`\n\n确认发布",
			wantMessage: "确认发布",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, q, api := newTestPatcher(t)
			taskID := uuidFromString(t, "ee888888-ee88-ee88-ee88-eeeeeeeeeeee")
			requesterID := uuidFromString(t, "99999999-9999-9999-9999-999999999999")
			q.task = db.AgentTaskQueue{ID: taskID, ChatInputTaskID: taskID, InitiatorUserID: requesterID}
			q.taskChannelIngested = true
			q.bindings = []InboxNotificationBinding{
				{
					UserBinding: UserBinding{
						MulticaUserID:  requesterID,
						InstallationID: q.installation.ID,
						ChannelUserID:  "ou_requester",
					},
					Installation: q.installation,
				},
			}

			p.handleEvent(events.Event{
				Type:          protocol.EventChatDone,
				TaskID:        uuidString(taskID),
				ChatSessionID: uuidString(q.binding.ChatSessionID),
				Payload: protocol.ChatDonePayload{
					TaskID:        uuidString(taskID),
					ChatSessionID: uuidString(q.binding.ChatSessionID),
					Content:       tt.content,
				},
			})

			api.mu.Lock()
			defer api.mu.Unlock()
			if len(api.sent) != 1 {
				t.Fatalf("expected one confirmation card; got %d", len(api.sent))
			}
			if len(api.textSent) != 0 || len(api.mdCardSent) != 0 {
				t.Fatalf("confirmation prompt must not also send text/markdown; text=%d markdown=%d",
					len(api.textSent), len(api.mdCardSent))
			}
			var card map[string]any
			if err := json.Unmarshal([]byte(api.sent[0].CardJSON), &card); err != nil {
				t.Fatalf("decode card json: %v", err)
			}
			raw, _ := json.Marshal(card)
			cardText := string(raw)
			for _, want := range []string{confirmationCardActionKind, tt.wantMessage, uuidString(taskID), "ou_requester", q.binding.ChannelChatID} {
				if !strings.Contains(cardText, want) {
					t.Errorf("confirmation card missing %q: %s", want, cardText)
				}
			}
		})
	}
}

// TestPatcherRoutesMarkdownReplyToCard pins the two-path chat reply:
// when the agent's body contains markdown syntax, the Patcher MUST
// route to SendMarkdownCard (schema-2.0 interactive card with a
// `tag: "markdown"` body element) so Lark renders the formatting
// instead of leaving raw `**bold**` / `# heading` characters in the
// transcript. Plain prose continues to go through SendTextMessage.
func TestPatcherRoutesMarkdownReplyToCard(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee444444-ee44-ee44-ee44-eeeeeeeeeeee")

	body := "# Summary\n\n- bullet one\n- bullet two\n\n```go\nfunc f() {}\n```\n"
	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{
			TaskID:        uuidString(taskID),
			ChatSessionID: uuidString(q.binding.ChatSessionID),
			Content:       body,
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.mdCardSent) != 1 {
		t.Fatalf("expected one SendMarkdownCard call; got %d", len(api.mdCardSent))
	}
	got := api.mdCardSent[0]
	if got.Markdown != body {
		t.Errorf("markdown body must be forwarded verbatim; got %q", got.Markdown)
	}
	if got.ChatID != ChatID(q.binding.ChannelChatID) {
		t.Errorf("chat_id mismatch: got %q want %q", got.ChatID, q.binding.ChannelChatID)
	}
	if len(api.textSent) != 0 {
		t.Errorf("markdown body must NOT also fire SendTextMessage; got %d", len(api.textSent))
	}
	if len(api.sent) != 0 || len(api.patched) != 0 {
		t.Errorf("ChatDone must NOT use legacy card paths; sent=%d patched=%d", len(api.sent), len(api.patched))
	}
}

// TestPatcherRoutesPlainReplyToText is the inverse: a short prose
// reply without any markdown syntax should stay on the cheap
// msg_type=text path so the user sees a normal IM bubble.
func TestPatcherRoutesPlainReplyToText(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee555555-ee55-ee55-ee55-eeeeeeeeeeee")

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{
			TaskID:        uuidString(taskID),
			ChatSessionID: uuidString(q.binding.ChatSessionID),
			Content:       "Sure, on it.",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 {
		t.Fatalf("plain prose must take the text path; got %d text sends", len(api.textSent))
	}
	if len(api.mdCardSent) != 0 {
		t.Errorf("plain prose must NOT wrap in a markdown card; got %d card sends", len(api.mdCardSent))
	}
}

// TestPatcherDropsEmptyChatReply guards the fallback we deliberately
// removed: the previous design rendered "Done." when content was
// empty. Now an empty Content is silently dropped (no text message
// sent at all). Showing nothing is better than showing the misleading
// "Done." fallback, which Bohan reported confused him in the live env.
func TestPatcherDropsEmptyChatReply(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee777777-ee77-ee77-ee77-eeeeeeeeeeee")

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{
			TaskID:        uuidString(taskID),
			ChatSessionID: uuidString(q.binding.ChatSessionID),
			Content:       "",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 0 {
		t.Errorf("empty content must drop, not render the Done. fallback; got %d text sends", len(api.textSent))
	}
}

func TestPatcherSkipsWhenNoChatSessionBinding(t *testing.T) {
	p, q, api := newTestPatcher(t)
	q.bindingErr = pgx.ErrNoRows

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(uuidFromString(t, "ee222222-ee22-ee22-ee22-eeeeeeeeeeee")),
		ChatSessionID: uuidString(uuidFromString(t, "cc222222-cc22-cc22-cc22-cccccccccccc")),
		Payload: protocol.ChatDonePayload{
			Content: "irrelevant — no binding",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 0 || len(api.sent) != 0 {
		t.Fatalf("web-only chat sessions must produce no outbound; got text=%d cards=%d",
			len(api.textSent), len(api.sent))
	}
}

// TestPatcherSkipsDirectChatTaskOnBoundSession guards the channel boundary:
// opening a Lark-bound session in the web/mobile UI must not make that direct
// conversation's reply or failure leak back into the external chat. Sealed
// channel tasks own an input batch too, so the discriminator is the
// channel_ingested provenance of the owned batch, not owner presence.
func TestPatcherSkipsDirectChatTaskOnBoundSession(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		payload   any
	}{
		{
			name:      "completed reply",
			eventType: protocol.EventChatDone,
			payload:   protocol.ChatDonePayload{Content: "web-only answer"},
		},
		{
			name:      "failed run",
			eventType: protocol.EventTaskFailed,
			payload:   map[string]any{"error": "web-only failure"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, q, api := newTestPatcher(t)
			taskID := uuidFromString(t, "ee999999-ee99-ee99-ee99-eeeeeeeeeeee")
			q.task = db.AgentTaskQueue{ChatInputTaskID: taskID}

			p.handleEvent(events.Event{
				Type:          tt.eventType,
				TaskID:        uuidString(taskID),
				ChatSessionID: uuidString(q.binding.ChatSessionID),
				Payload:       tt.payload,
			})

			api.mu.Lock()
			defer api.mu.Unlock()
			if len(api.textSent) != 0 || len(api.mdCardSent) != 0 || len(api.sent) != 0 || len(api.patched) != 0 {
				t.Fatalf("direct task must produce no channel outbound; got text=%d markdown=%d cards=%d patches=%d",
					len(api.textSent), len(api.mdCardSent), len(api.sent), len(api.patched))
			}
		})
	}
}

// TestPatcherFailEventSendsErrorCard verifies the failure path still
// surfaces a card. The visual distinction between a successful reply
// (plain text bubble) and a failure (red header card) is genuinely
// useful — and failures are rare enough that the card chrome isn't
// noisy. One-shot send (no patching of any prior thinking card,
// because there isn't one anymore).
func TestPatcherFailEventSendsErrorCard(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee444444-ee44-ee44-ee44-eeeeeeeeeeee")

	p.handleEvent(events.Event{
		Type:          protocol.EventTaskFailed,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: map[string]any{
			"task_id":         uuidString(taskID),
			"chat_session_id": uuidString(q.binding.ChatSessionID),
			"error":           "boom",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("fail event must send an error card; got %d card sends", len(api.sent))
	}
	if len(api.patched) != 0 {
		t.Errorf("fail event must NOT patch any card (no prior card lifecycle); got %d patches", len(api.patched))
	}
	if !strings.Contains(api.sent[0].CardJSON, "boom") {
		t.Errorf("error card body should embed the error message; got %s", api.sent[0].CardJSON)
	}
}

func TestPatcherSwallowsInstallationLoadErrors(t *testing.T) {
	p, q, api := newTestPatcher(t)
	q.installationErr = errors.New("db down")

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(uuidFromString(t, "ee555555-ee55-ee55-ee55-eeeeeeeeeeee")),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{
			Content: "would-be reply",
		},
	})

	// The patcher logs but never panics; no outbound.
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 0 || len(api.sent) != 0 {
		t.Fatalf("DB failure must not produce outbound; got text=%d cards=%d",
			len(api.textSent), len(api.sent))
	}
}

// TestPatcherIgnoresEventTaskCompletedForChatTasks pins the no-extra-send
// invariant. TaskService publishes ChatDone (with content) immediately
// before TaskCompleted (without content) for every chat task. The
// Patcher must NOT react to TaskCompleted — doing so would either
// re-send the same text reply (duplicate bubble) or send the "Done."
// fallback (the original bug Bohan reported). The fix is to leave
// EventTaskCompleted unsubscribed; this test asserts exactly one
// outbound text message from the sequence.
func TestPatcherIgnoresEventTaskCompletedForChatTasks(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee666666-ee66-ee66-ee66-eeeeeeeeeeee")

	// Step 1: ChatDone arrives with the real agent reply. Plain text
	// is sent to Lark.
	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{
			TaskID:        uuidString(taskID),
			ChatSessionID: uuidString(q.binding.ChatSessionID),
			Content:       "Hello! I'm cc, a coding agent…",
		},
	})

	// Step 2: TaskCompleted fires immediately after with no content.
	// The Patcher MUST NOT send a second message — neither a
	// duplicate of the reply nor the "Done." fallback.
	p.handleEvent(events.Event{
		Type:          protocol.EventTaskCompleted,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: map[string]any{
			"task_id":         uuidString(taskID),
			"chat_session_id": uuidString(q.binding.ChatSessionID),
			"status":          "completed",
		},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 {
		t.Fatalf("exactly one text send expected (ChatDone); EventTaskCompleted must be ignored. Got %d sends", len(api.textSent))
	}
	if api.textSent[0].Text != "Hello! I'm cc, a coding agent…" {
		t.Errorf("text content mismatch; got %q", api.textSent[0].Text)
	}
	if len(api.sent) != 0 || len(api.patched) != 0 {
		t.Errorf("no card outbound expected on the success path; got sent=%d patched=%d",
			len(api.sent), len(api.patched))
	}
}

// TestDefaultRendererConfigCarriesUpdateMulti pins the streaming-card
// contract: every lifecycle kind is a CardKit JSON 2.0 card, remains shared,
// and terminal kinds explicitly close streaming mode.
func TestDefaultRendererConfigCarriesUpdateMulti(t *testing.T) {
	r := NewDefaultRenderer()
	for _, kind := range []CardKind{CardKindThinking, CardKindRunning, CardKindFinal, CardKindError} {
		t.Run(string(kind), func(t *testing.T) {
			out, err := r.Render(RenderInput{Kind: kind, Content: "x", ErrorMessage: "y"})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			var doc map[string]any
			if err := json.Unmarshal([]byte(out.JSON), &doc); err != nil {
				t.Fatalf("decode card json: %v", err)
			}
			cfg, ok := doc["config"].(map[string]any)
			if !ok {
				t.Fatalf("missing config block: %v", doc)
			}
			if v, _ := cfg["update_multi"].(bool); !v {
				t.Errorf("config.update_multi must be true so subsequent patches apply; got %v", cfg)
			}
			if v, _ := cfg["wide_screen_mode"].(bool); !v {
				t.Errorf("config.wide_screen_mode regression: %v", cfg)
			}
			if doc["schema"] != "2.0" {
				t.Errorf("schema=%v want 2.0", doc["schema"])
			}
			body, _ := doc["body"].(map[string]any)
			raw, _ := json.Marshal(body["elements"])
			if !strings.Contains(string(raw), `"tag":"markdown"`) {
				t.Errorf("card body must render markdown-capable visible text: %s", raw)
			}
			streaming, _ := cfg["streaming_mode"].(bool)
			if want := kind == CardKindThinking || kind == CardKindRunning; streaming != want {
				t.Errorf("streaming_mode=%v want %v", streaming, want)
			}
		})
	}
}

// TestPatcherRepliesInThreadWhenTriggerWasInThread pins the core
// behavior of this feature: when the chat binding's most-recent trigger
// lived inside a Lark topic (last_lark_thread_id set), the agent reply
// is routed through the reply endpoint targeting that message with
// reply_in_thread=true, so it lands inside the 话题 instead of the group.
func TestPatcherRepliesInThreadWhenTriggerWasInThread(t *testing.T) {
	p, q, api := newTestPatcher(t)
	q.binding.LastMessageID = pgtype.Text{String: "om_trigger", Valid: true}
	q.binding.LastThreadID = pgtype.Text{String: "omt_topic", Valid: true}
	taskID := uuidFromString(t, "ee666666-ee66-ee66-ee66-eeeeeeeeeeee")

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       protocol.ChatDonePayload{Content: "in-thread reply"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 {
		t.Fatalf("expected one text send; got %d", len(api.textSent))
	}
	got := api.textSent[0].ReplyTarget
	if got.MessageID != "om_trigger" || !got.InThread {
		t.Errorf("expected thread reply target {om_trigger, InThread:true}; got %+v", got)
	}
}

// TestPatcherTopicSessionSendsToRealChatID pins the composite-key outbound
// contract: a per-topic session stores "chat:thread" as channel_chat_id (the
// isolation key), so the send target MUST come from the binding config's real
// chat id — the raw key is not a valid Lark chat id.
func TestPatcherTopicSessionSendsToRealChatID(t *testing.T) {
	p, q, api := newTestPatcher(t)
	q.binding.ChannelChatID = "oc_test_chat:omt_topic1"
	q.binding.Config = []byte(`{"chat_id":"oc_test_chat"}`)
	q.binding.ChatType = "group"
	q.binding.LastMessageID = pgtype.Text{String: "om_trigger", Valid: true}
	q.binding.LastThreadID = pgtype.Text{String: "omt_topic1", Valid: true}
	taskID := uuidFromString(t, "eeaaaaaa-eeaa-eeaa-eeaa-eeeeeeeeeeee")

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       protocol.ChatDonePayload{Content: "topic reply"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 {
		t.Fatalf("expected one text send; got %d", len(api.textSent))
	}
	got := api.textSent[0]
	if got.ChatID != "oc_test_chat" {
		t.Errorf("chat_id = %q, want the real chat id from binding config", got.ChatID)
	}
	if got.ReplyTarget.MessageID != "om_trigger" || !got.ReplyTarget.InThread {
		t.Errorf("expected thread reply target {om_trigger, InThread:true}; got %+v", got.ReplyTarget)
	}
}

// TestPatcherLegacyBindingFallsBackToKey pins backward compatibility: rows
// created before topic isolation have the raw chat id as the key and "{}" as
// config — the send target must stay the key itself.
func TestPatcherLegacyBindingFallsBackToKey(t *testing.T) {
	p, q, api := newTestPatcher(t)
	q.binding.Config = []byte(`{}`)
	taskID := uuidFromString(t, "eebbbbbb-eebb-eebb-eebb-eeeeeeeeeeee")

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       protocol.ChatDonePayload{Content: "legacy reply"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 {
		t.Fatalf("expected one text send; got %d", len(api.textSent))
	}
	if got := api.textSent[0].ChatID; got != "oc_test_chat" {
		t.Errorf("chat_id = %q, want the raw binding key for legacy rows", got)
	}
}

// TestPatcherSendsToChatWhenNoThread verifies that a non-thread trigger
// (no last_lark_thread_id on the binding) keeps the historical
// chat-level send: ReplyTarget stays empty so SendTextMessage targets
// the chat by chat_id. This is the no-behavior-change guarantee for
// normal group / p2p chats.
func TestPatcherSendsToChatWhenNoThread(t *testing.T) {
	p, q, api := newTestPatcher(t)
	// binding has a message id but NO thread id → must not thread.
	q.binding.LastMessageID = pgtype.Text{String: "om_trigger", Valid: true}
	taskID := uuidFromString(t, "ee777777-ee77-ee77-ee77-eeeeeeeeeeee")

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       protocol.ChatDonePayload{Content: "plain reply"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 {
		t.Fatalf("expected one text send; got %d", len(api.textSent))
	}
	if api.textSent[0].ReplyTarget.IsSet() {
		t.Errorf("non-thread trigger must NOT route through the reply endpoint; got %+v",
			api.textSent[0].ReplyTarget)
	}
}

// TestPatcherThreadReplyMarkdownRoutesToThread verifies the markdown
// card path also threads when the trigger was in a topic.
func TestPatcherThreadReplyMarkdownRoutesToThread(t *testing.T) {
	p, q, api := newTestPatcher(t)
	q.binding.LastMessageID = pgtype.Text{String: "om_trigger", Valid: true}
	q.binding.LastThreadID = pgtype.Text{String: "omt_topic", Valid: true}
	taskID := uuidFromString(t, "ee888888-ee88-ee88-ee88-eeeeeeeeeeee")

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       protocol.ChatDonePayload{Content: "# heading\n- bullet"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.mdCardSent) != 1 {
		t.Fatalf("expected one markdown card send; got %d", len(api.mdCardSent))
	}
	got := api.mdCardSent[0].ReplyTarget
	if got.MessageID != "om_trigger" || !got.InThread {
		t.Errorf("expected markdown thread reply target {om_trigger, InThread:true}; got %+v", got)
	}
}

// TestPatcherThreadReplyFallsBackToChatLevel verifies that when a
// threaded send fails with a classified "topic cannot receive this
// reply" Lark error (e.g. the trigger message was recalled or the topic
// was aggregated), the patcher retries once at the chat level so the
// agent's reply is never silently lost.
func TestPatcherThreadReplyFallsBackToChatLevel(t *testing.T) {
	p, q, api := newTestPatcher(t)
	api.threadReplyErr = errThreadReplyClassified
	q.binding.LastMessageID = pgtype.Text{String: "om_trigger", Valid: true}
	q.binding.LastThreadID = pgtype.Text{String: "omt_topic", Valid: true}
	taskID := uuidFromString(t, "ee999999-ee99-ee99-ee99-eeeeeeeeeeee")

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       protocol.ChatDonePayload{Content: "reply that must survive"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 2 {
		t.Fatalf("expected two text sends (thread attempt + chat-level fallback); got %d", len(api.textSent))
	}
	if !api.textSent[0].ReplyTarget.IsSet() {
		t.Errorf("first attempt should be the thread reply; got %+v", api.textSent[0].ReplyTarget)
	}
	if api.textSent[1].ReplyTarget.IsSet() {
		t.Errorf("fallback attempt must be chat-level (empty ReplyTarget); got %+v", api.textSent[1].ReplyTarget)
	}
}

// TestPatcherThreadReplyDoesNotFallBackOnAmbiguousError verifies that a
// non-classified failure (transport error, 5xx, timeout, rate limit)
// from the threaded send is NOT retried at chat level: a blind retry
// could duplicate the reply or leak a thread-only reply into the group.
func TestPatcherThreadReplyDoesNotFallBackOnAmbiguousError(t *testing.T) {
	p, q, api := newTestPatcher(t)
	api.threadReplyErr = errThreadReplyTransport
	q.binding.LastMessageID = pgtype.Text{String: "om_trigger", Valid: true}
	q.binding.LastThreadID = pgtype.Text{String: "omt_topic", Valid: true}
	taskID := uuidFromString(t, "ee888888-ee88-ee88-ee88-eeeeeeeeeeee")

	p.handleEvent(events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       protocol.ChatDonePayload{Content: "reply that must not duplicate"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 {
		t.Fatalf("expected a single thread attempt with no chat-level fallback; got %d sends", len(api.textSent))
	}
	if !api.textSent[0].ReplyTarget.IsSet() {
		t.Errorf("the single attempt should be the thread reply; got %+v", api.textSent[0].ReplyTarget)
	}
}
