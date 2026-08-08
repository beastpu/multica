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

// fakePatcherQueries models the single channel_outbound_card_message row and
// the guard each query puts on it. The guards are the point: they are what
// makes duplicate events, replica races and late sends safe in Postgres, so a
// fake that skipped them would let tests pass on behaviour the database
// forbids.
type fakePatcherQueries struct {
	mu                  sync.Mutex
	now                 func() time.Time
	task                db.AgentTaskQueue
	taskErr             error
	taskChannelIngested bool
	binding             ChatSessionBinding
	bindingErr          error
	installation        Installation
	installationErr     error
	agent               db.Agent
	agentErr            error
	bindings            []InboxNotificationBinding
	bindingsErr         error

	card    OutboundCardMessage
	hasCard bool
	cardErr error

	created           []CreateOutboundCardMessageParams
	messageIDWrites   []SetOutboundCardMessageIDParams
	settled           []string
	askMessageUpdates []db.UpdateChatAskChannelMessageParams
}

func (f *fakePatcherQueries) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

// seedLiveCard puts the fake in the state it reaches after a progress card has
// been sent: the row is streaming and carries the Lark message id to patch.
func (f *fakePatcherQueries) seedLiveCard(messageID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hasCard = true
	f.card = OutboundCardMessage{
		ID:                   uuidFromStringNoTest("dddddddd-dddd-dddd-dddd-dddddddddddd"),
		ChatSessionID:        f.binding.ChatSessionID,
		TaskID:               f.task.ID,
		ChannelChatID:        f.binding.ChannelChatID,
		ChannelCardMessageID: messageID,
		Status:               string(CardStatusStreaming),
		LastPatchedAt:        pgtype.Timestamptz{Time: f.clock(), Valid: true},
		CreatedAt:            pgtype.Timestamptz{Time: f.clock(), Valid: true},
	}
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cardErr != nil {
		return OutboundCardMessage{}, f.cardErr
	}
	if !f.hasCard {
		return OutboundCardMessage{}, pgx.ErrNoRows
	}
	return f.card, nil
}

// CreateLarkOutboundCardMessage mirrors ON CONFLICT (task_id) DO NOTHING: a
// second EventTaskRunning for the same task reports no row rather than opening
// a second card.
func (f *fakePatcherQueries) CreateLarkOutboundCardMessage(ctx context.Context, arg CreateOutboundCardMessageParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, arg)
	if f.hasCard {
		return OutboundCardMessage{}, pgx.ErrNoRows
	}
	now := f.clock()
	f.card = OutboundCardMessage{
		ID:            uuidFromStringNoTest("dddddddd-dddd-dddd-dddd-dddddddddddd"),
		ChatSessionID: arg.ChatSessionID,
		TaskID:        arg.TaskID,
		ChannelChatID: arg.ChannelChatID,
		Status:        string(CardStatusPending),
		LastPatchedAt: pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:     pgtype.Timestamptz{Time: now, Valid: true},
	}
	f.hasCard = true
	return f.card, nil
}

// ClaimLarkOutboundCardWork mirrors the due predicate and the last_patched_at
// stamp that stands in for a delivery lease.
func (f *fakePatcherQueries) ClaimLarkOutboundCardWork(ctx context.Context, arg ClaimOutboundCardWorkParams) ([]OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.hasCard {
		return nil, nil
	}
	switch f.task.Status {
	case "completed", "failed", "cancelled":
		return nil, nil
	}
	wait := arg.HeartbeatSeconds
	if f.card.Status == string(CardStatusPending) {
		wait = arg.StartDelaySeconds
	} else if f.card.Status != string(CardStatusStreaming) {
		return nil, nil
	}
	now := f.clock()
	if now.Sub(f.card.LastPatchedAt.Time) < time.Duration(wait*float64(time.Second)) {
		return nil, nil
	}
	f.card.LastPatchedAt = pgtype.Timestamptz{Time: now, Valid: true}
	return []OutboundCardMessage{f.card}, nil
}

// SetLarkOutboundCardMessageID mirrors the guard that keeps a card which landed
// after the task settled from dragging the row back into 'streaming'.
func (f *fakePatcherQueries) SetLarkOutboundCardMessageID(ctx context.Context, arg SetOutboundCardMessageIDParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messageIDWrites = append(f.messageIDWrites, arg)
	if !f.hasCard || f.card.ChannelCardMessageID != "" || f.card.Status != string(CardStatusPending) {
		return OutboundCardMessage{}, pgx.ErrNoRows
	}
	f.card.ChannelCardMessageID = arg.ChannelCardMessageID
	f.card.Status = string(CardStatusStreaming)
	f.card.LastPatchedAt = pgtype.Timestamptz{Time: f.clock(), Valid: true}
	return f.card, nil
}

// SettleLarkOutboundCard mirrors the write that elects one terminal event as
// the owner of the reply.
func (f *fakePatcherQueries) SettleLarkOutboundCard(ctx context.Context, arg SettleOutboundCardParams) (OutboundCardMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cardErr != nil {
		return OutboundCardMessage{}, f.cardErr
	}
	if !f.hasCard || (f.card.Status != string(CardStatusPending) && f.card.Status != string(CardStatusStreaming)) {
		return OutboundCardMessage{}, pgx.ErrNoRows
	}
	f.card.Status = arg.Status
	f.card.LastPatchedAt = pgtype.Timestamptz{Time: f.clock(), Valid: true}
	f.settled = append(f.settled, arg.Status)
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
	// onSend runs after a card send is recorded but before the caller can
	// persist its message id, so a test can interleave an event that races it.
	onSend func()
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
	f.sent = append(f.sent, p)
	threadErr, onSend := f.threadReplyErr, f.onSend
	sendReturn, sendErr := f.sendReturn, f.sendErr
	f.mu.Unlock()
	if threadErr != nil && p.ReplyTarget.IsSet() {
		return "", threadErr
	}
	if onSend != nil {
		onSend()
	}
	return sendReturn, sendErr
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
		agent: db.Agent{Name: "TestAgent"},
	}
	api := &fakeAPIClient{sendReturn: "lark_card_msg_1", textSendReturn: "lark_text_msg_1"}
	p := NewPatcher(q, fakeCredentials{secret: "shh"}, api, PatcherConfig{
		Logger: newDiscardLogger(),
		Now:    time.Now,
	})
	return p, q, api
}

// newClockedPatcher is newTestPatcher on a clock the test drives, for the
// timing the card lifecycle actually depends on: the start delay before a card
// appears and the heartbeat between repaints.
func newClockedPatcher(t *testing.T) (*Patcher, *fakePatcherQueries, *fakeAPIClient, func(time.Duration)) {
	t.Helper()
	p, q, api := newTestPatcher(t)
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	p.cfg.Now = clock
	q.now = clock
	return p, q, api, func(d time.Duration) { now = now.Add(d) }
}

// runningEvent is the event that opens a card.
func runningEvent(q *fakePatcherQueries, taskID pgtype.UUID) events.Event {
	return events.Event{
		Type:          protocol.EventTaskRunning,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: map[string]any{
			"task_id":         uuidString(taskID),
			"chat_session_id": uuidString(q.binding.ChatSessionID),
		},
	}
}

func chatDoneEvent(q *fakePatcherQueries, taskID pgtype.UUID, content string) events.Event {
	return events.Event{
		Type:          protocol.EventChatDone,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload: protocol.ChatDonePayload{
			TaskID:        uuidString(taskID),
			ChatSessionID: uuidString(q.binding.ChatSessionID),
			Content:       content,
		},
	}
}

// startChannelTask wires the fake up as a running, channel-ingested task.
func startChannelTask(q *fakePatcherQueries, taskID pgtype.UUID) {
	q.task = db.AgentTaskQueue{
		ID:              taskID,
		ChatSessionID:   q.binding.ChatSessionID,
		ChatInputTaskID: taskID,
		Status:          "running",
	}
	q.taskChannelIngested = true
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

// TestPatcherHoldsCardUntilStartDelay pins why the delay exists at all: a task
// that answers quickly must reach the chat as an ordinary message, never as a
// card that flashes up and is immediately overwritten.
func TestPatcherHoldsCardUntilStartDelay(t *testing.T) {
	p, q, api, advance := newClockedPatcher(t)
	taskID := uuidFromString(t, "ee100001-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)

	p.handleEvent(runningEvent(q, taskID))
	advance(p.cfg.StreamingDelay - time.Second)
	p.RunOnce(context.Background())

	if len(q.created) != 1 {
		t.Fatalf("running event must open exactly one card row; created=%d", len(q.created))
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 0 {
		t.Fatalf("no card may reach Lark before the start delay; sent=%d", len(api.sent))
	}
}

// TestPatcherSendsCardOnceStartDelayPasses is the other half: a run that is
// still going after the delay gets exactly one card, and the row records the
// Lark message id so later paints know what to patch.
func TestPatcherSendsCardOnceStartDelayPasses(t *testing.T) {
	p, q, api, advance := newClockedPatcher(t)
	taskID := uuidFromString(t, "ee100002-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)

	p.handleEvent(runningEvent(q, taskID))
	advance(p.cfg.StreamingDelay + time.Second)
	p.RunOnce(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("expected one progress card; sent=%d", len(api.sent))
	}
	if want := "progress-" + uuidString(taskID); api.sent[0].IdempotencyKey != want {
		t.Errorf("idempotency key=%q want %q", api.sent[0].IdempotencyKey, want)
	}
	if !strings.Contains(api.sent[0].CardJSON, "正在处理") {
		t.Errorf("progress card must say work is under way: %s", api.sent[0].CardJSON)
	}
	if q.card.ChannelCardMessageID != "lark_card_msg_1" {
		t.Errorf("row must record the sent card; got %q", q.card.ChannelCardMessageID)
	}
	if q.card.Status != string(CardStatusStreaming) {
		t.Errorf("row status=%q want streaming", q.card.Status)
	}
}

// TestPatcherRepaintsLiveCardOnHeartbeat covers the only thing the card reports
// while a run is in flight: that it is still going, and for how long. A repaint
// patches the message already on screen rather than posting another one.
func TestPatcherRepaintsLiveCardOnHeartbeat(t *testing.T) {
	p, q, api, advance := newClockedPatcher(t)
	taskID := uuidFromString(t, "ee100003-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)

	p.handleEvent(runningEvent(q, taskID))
	advance(p.cfg.StreamingDelay + time.Second)
	p.RunOnce(context.Background())

	// Not due yet: a poll inside the heartbeat window must be a no-op.
	advance(p.cfg.HeartbeatInterval - time.Second)
	p.RunOnce(context.Background())
	api.mu.Lock()
	if len(api.patched) != 0 {
		api.mu.Unlock()
		t.Fatalf("repaint before the heartbeat is due; patched=%d", len(api.patched))
	}
	api.mu.Unlock()

	advance(2 * time.Second)
	p.RunOnce(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("a repaint must not post a second card; sent=%d", len(api.sent))
	}
	if len(api.patched) != 1 {
		t.Fatalf("expected one repaint; patched=%d", len(api.patched))
	}
	if api.patched[0].LarkCardMessageID != "lark_card_msg_1" {
		t.Errorf("repaint targeted %q want lark_card_msg_1", api.patched[0].LarkCardMessageID)
	}
	if !strings.Contains(api.patched[0].CardJSON, "秒") {
		t.Errorf("repaint must carry elapsed time: %s", api.patched[0].CardJSON)
	}
}

// TestPatcherStopsPaintingWhenTaskIsTerminal keeps a card from counting upwards
// forever when the task reached a terminal state without a terminal event —
// a daemon that died, or an event lost on the way to this replica.
func TestPatcherStopsPaintingWhenTaskIsTerminal(t *testing.T) {
	p, q, api, advance := newClockedPatcher(t)
	taskID := uuidFromString(t, "ee100004-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)

	p.handleEvent(runningEvent(q, taskID))
	advance(p.cfg.StreamingDelay + time.Second)
	p.RunOnce(context.Background())

	q.task.Status = "failed"
	advance(p.cfg.HeartbeatInterval + time.Second)
	p.RunOnce(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 0 {
		t.Fatalf("a terminal task must stop its card; patched=%d", len(api.patched))
	}
}

// TestPatcherSkipsPaintWhenInstallationRevoked covers the bot being unbound
// while a task is still running. There is no chat left to paint into, so the
// worker must stop quietly rather than retry a call that can only fail.
func TestPatcherSkipsPaintWhenInstallationRevoked(t *testing.T) {
	p, q, api, advance := newClockedPatcher(t)
	taskID := uuidFromString(t, "ee100016-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)

	p.handleEvent(runningEvent(q, taskID))
	q.installation.Status = string(InstallationRevoked)
	advance(p.cfg.StreamingDelay + time.Second)
	p.RunOnce(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 0 || len(api.patched) != 0 {
		t.Fatalf("a revoked installation must not be painted; sent=%d patched=%d", len(api.sent), len(api.patched))
	}
}

// TestPatcherOpensOneCardPerTask covers duplicate EventTaskRunning delivery —
// a bus replay, or the same event reaching two replicas.
func TestPatcherOpensOneCardPerTask(t *testing.T) {
	p, q, api, advance := newClockedPatcher(t)
	taskID := uuidFromString(t, "ee100005-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)

	p.handleEvent(runningEvent(q, taskID))
	p.handleEvent(runningEvent(q, taskID))
	advance(p.cfg.StreamingDelay + time.Second)
	p.RunOnce(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.sent) != 1 {
		t.Fatalf("duplicate running events must not post two cards; sent=%d", len(api.sent))
	}
}

// TestPatcherFinalReplacesLiveCardInPlace is the payoff of keeping a card at
// all: the thing the user was already watching turns into the answer, instead
// of the answer arriving below a card still claiming to be working.
func TestPatcherFinalReplacesLiveCardInPlace(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100006-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)
	q.seedLiveCard("om_streaming")

	p.handleEvent(chatDoneEvent(q, taskID, "the answer"))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 {
		t.Fatalf("expected the live card to be patched; patched=%d", len(api.patched))
	}
	if !strings.Contains(api.patched[0].CardJSON, "the answer") {
		t.Errorf("final card missing the answer: %s", api.patched[0].CardJSON)
	}
	if len(api.textSent) != 0 || len(api.sent) != 0 || len(api.mdCardSent) != 0 {
		t.Fatalf("a patched card must not also post a second message")
	}
	if q.card.Status != string(CardStatusFinal) {
		t.Errorf("row status=%q want final", q.card.Status)
	}
}

// TestPatcherFallsBackToNativeReplyWhenCardPatchFails is the regression for the
// incident this whole path was rebuilt around: a card that cannot be patched
// used to swallow the answer entirely. A stale card on screen is acceptable;
// losing what the agent said is not.
func TestPatcherFallsBackToNativeReplyWhenCardPatchFails(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100007-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)
	q.seedLiveCard("om_streaming")
	api.patchErr = errors.New("fake: card is no longer patchable")

	p.handleEvent(chatDoneEvent(q, taskID, "the answer"))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 {
		t.Fatalf("expected one patch attempt; patched=%d", len(api.patched))
	}
	if len(api.textSent) != 1 || api.textSent[0].Text != "the answer" {
		t.Fatalf("the answer must still reach the chat; textSent=%+v", api.textSent)
	}
}

// TestPatcherKeepsNativeReplyWhenCardWasNeverSent covers the fast path once a
// row exists but the start delay has not elapsed: the row settles and the reply
// goes out as an ordinary message, with no orphan card left behind.
func TestPatcherKeepsNativeReplyWhenCardWasNeverSent(t *testing.T) {
	p, q, api, _ := newClockedPatcher(t)
	taskID := uuidFromString(t, "ee100008-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)

	p.handleEvent(runningEvent(q, taskID))
	p.handleEvent(chatDoneEvent(q, taskID, "quick answer"))
	p.RunOnce(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 || api.textSent[0].Text != "quick answer" {
		t.Fatalf("fast reply must stay native; textSent=%+v", api.textSent)
	}
	if len(api.sent) != 0 || len(api.patched) != 0 {
		t.Fatalf("no card outbound expected; sent=%d patched=%d", len(api.sent), len(api.patched))
	}
	if q.card.Status != string(CardStatusFinal) {
		t.Errorf("row status=%q want final", q.card.Status)
	}
}

// TestPatcherEmptyFinalRetiresLiveCardWithoutSpeaking covers the awkward pair
// of rules around an empty reply. On its own it is dropped — we would rather
// say nothing than post "Done." at someone. But a card already on screen cannot
// be dropped: left alone it would sit on "正在处理" forever, so it retires with
// a neutral line instead, and still posts no second message.
func TestPatcherEmptyFinalRetiresLiveCardWithoutSpeaking(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100014-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)
	q.seedLiveCard("om_streaming")

	p.handleEvent(chatDoneEvent(q, taskID, ""))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 {
		t.Fatalf("a live card must be retired, not left spinning; patched=%d", len(api.patched))
	}
	if strings.Contains(api.patched[0].CardJSON, "正在处理") {
		t.Errorf("retired card still claims to be working: %s", api.patched[0].CardJSON)
	}
	if strings.Contains(api.patched[0].CardJSON, "Done.") {
		t.Errorf("retired card must not use the Done. wording: %s", api.patched[0].CardJSON)
	}
	if len(api.textSent) != 0 || len(api.sent) != 0 || len(api.mdCardSent) != 0 {
		t.Fatalf("an empty reply must not post a message; textSent=%d sent=%d md=%d",
			len(api.textSent), len(api.sent), len(api.mdCardSent))
	}
	if q.card.Status != string(CardStatusFinal) {
		t.Errorf("row status=%q want final", q.card.Status)
	}
}

// TestPatcherEmptyFinalWithoutCardStaysSilent is the other half: with no card
// on screen there is nothing to retire, so the empty reply is simply dropped.
func TestPatcherEmptyFinalWithoutCardStaysSilent(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100015-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)

	p.handleEvent(chatDoneEvent(q, taskID, ""))

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 0 || len(api.sent) != 0 || len(api.patched) != 0 {
		t.Fatalf("empty reply must be silent; textSent=%d sent=%d patched=%d",
			len(api.textSent), len(api.sent), len(api.patched))
	}
}

// TestPatcherSecondTerminalEventStaysQuiet pins the settle write as the
// dedup: chat:done and task:failed race for the same task, and the loser must
// not add a second message.
func TestPatcherSecondTerminalEventStaysQuiet(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100009-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)
	q.seedLiveCard("om_streaming")

	p.handleEvent(chatDoneEvent(q, taskID, "the answer"))
	p.handleEvent(events.Event{
		Type:          protocol.EventTaskFailed,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       map[string]any{"error": "too late"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 {
		t.Fatalf("only the winning terminal event may speak; patched=%d", len(api.patched))
	}
	if len(api.sent) != 0 || len(api.textSent) != 0 {
		t.Fatalf("late terminal event must stay quiet; sent=%d textSent=%d", len(api.sent), len(api.textSent))
	}
	if len(q.settled) != 1 {
		t.Errorf("settle writes=%v want exactly one winner", q.settled)
	}
}

// TestPatcherDoesNotAdoptCardThatLandedAfterSettle covers the narrow race where
// a task finishes while its first card send is still in flight. The answer has
// already gone out on its own, so the late card must not drag the row back to
// streaming and start a heartbeat behind it.
func TestPatcherDoesNotAdoptCardThatLandedAfterSettle(t *testing.T) {
	p, q, api, advance := newClockedPatcher(t)
	taskID := uuidFromString(t, "ee100010-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)
	p.handleEvent(runningEvent(q, taskID))
	advance(p.cfg.StreamingDelay + time.Second)

	// The send lands, but the task settles before the row can record it.
	api.onSend = func() { p.handleEvent(chatDoneEvent(q, taskID, "the answer")) }
	p.RunOnce(context.Background())

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.textSent) != 1 || api.textSent[0].Text != "the answer" {
		t.Fatalf("the answer must reach the chat on its own; textSent=%+v", api.textSent)
	}
	if q.card.Status != string(CardStatusFinal) {
		t.Errorf("row status=%q want final", q.card.Status)
	}
	if q.card.ChannelCardMessageID != "" {
		t.Errorf("settled row must not adopt the late card; got %q", q.card.ChannelCardMessageID)
	}
}

// TestPatcherFinalCardCarriesConfirmationAction keeps the interactive
// confirmation buttons usable when a live card becomes the answer.
func TestPatcherFinalCardCarriesConfirmationAction(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100011-ee10-ee10-ee10-eeeeeeeeeeee")
	requesterID := uuidFromString(t, "99999999-9999-9999-9999-999999999999")
	startChannelTask(q, taskID)
	q.task.InitiatorUserID = requesterID
	q.seedLiveCard("om_streaming")
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

	p.handleEvent(chatDoneEvent(q, taskID, "项目：`测试`\n\n请回复“确认执行”，我再触发。"))

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

// TestPatcherFailurePatchesLiveCard settles a live card as an error rather than
// leaving it spinning next to a separate failure notice.
func TestPatcherFailurePatchesLiveCard(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100012-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)
	q.seedLiveCard("om_streaming")

	p.handleEvent(events.Event{
		Type:          protocol.EventTaskFailed,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       map[string]any{"error": "boom"},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 {
		t.Fatalf("expected the live card to be patched; patched=%d", len(api.patched))
	}
	if !strings.Contains(api.patched[0].CardJSON, "boom") {
		t.Errorf("error card missing the reason: %s", api.patched[0].CardJSON)
	}
	if len(api.sent) != 0 {
		t.Fatalf("failure must not post a second card; sent=%d", len(api.sent))
	}
	if q.card.Status != string(CardStatusError) {
		t.Errorf("row status=%q want error", q.card.Status)
	}
}

// TestPatcherCancellationSettlesLiveCard stops the heartbeat and says so.
func TestPatcherCancellationSettlesLiveCard(t *testing.T) {
	p, q, api := newTestPatcher(t)
	taskID := uuidFromString(t, "ee100013-ee10-ee10-ee10-eeeeeeeeeeee")
	startChannelTask(q, taskID)
	q.seedLiveCard("om_streaming")

	p.handleEvent(events.Event{
		Type:          protocol.EventTaskCancelled,
		TaskID:        uuidString(taskID),
		ChatSessionID: uuidString(q.binding.ChatSessionID),
		Payload:       map[string]any{"task_id": uuidString(taskID)},
	})

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.patched) != 1 || !strings.Contains(api.patched[0].CardJSON, "已取消") {
		t.Fatalf("cancellation must settle the card in place; patched=%+v", api.patched)
	}
	if q.card.Status != string(CardStatusFinal) {
		t.Errorf("row status=%q want final", q.card.Status)
	}
}

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

// TestDefaultRendererConfigCarriesUpdateMulti pins the card contract: every
// lifecycle kind is a JSON 2.0 card with a markdown body, and stays a shared
// card so a later patch reaches everyone who can see it.
func TestDefaultRendererConfigCarriesUpdateMulti(t *testing.T) {
	r := NewDefaultRenderer()
	for _, kind := range []CardKind{CardKindRunning, CardKindFinal, CardKindError} {
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
		})
	}
}

// TestProgressCardReportsElapsedOnly pins what a running card is allowed to
// say. Anything drawn from the transcript would put agent reasoning and tool
// activity into a chat the requester does not control.
func TestProgressCardReportsElapsedOnly(t *testing.T) {
	out, err := NewDefaultRenderer().Render(RenderInput{
		Kind: CardKindRunning, AgentName: "TestAgent", ElapsedSecs: 42,
		Content: "secret reasoning", ErrorMessage: "secret failure",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out.JSON, "42 秒") {
		t.Errorf("running card must report elapsed time: %s", out.JSON)
	}
	for _, leaked := range []string{"secret reasoning", "secret failure"} {
		if strings.Contains(out.JSON, leaked) {
			t.Errorf("running card leaked %q: %s", leaked, out.JSON)
		}
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
