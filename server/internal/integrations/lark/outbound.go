package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// CardStatus mirrors channel_outbound_card_message.status. Kept as a typed
// alias so callers can't pass arbitrary strings into the status column.
type CardStatus string

const (
	CardStatusPending   CardStatus = "pending"
	CardStatusStreaming CardStatus = "streaming"
	CardStatusFinal     CardStatus = "final"
	CardStatusError     CardStatus = "error"
)

// CardKind enumerates the small set of card variants the Renderer
// produces. The Renderer is plug-replaceable so the on-wire card
// template can evolve without touching transport or persistence logic.
type CardKind string

const (
	CardKindRunning CardKind = "running"
	CardKindFinal   CardKind = "final"
	CardKindError   CardKind = "error"
)

// CardRender is the rendered card body the Renderer produces. The
// caller serializes nothing further — this JSON goes on the wire.
type CardRender struct {
	JSON string
}

// RenderInput is the typed snapshot the Renderer sees. A running card knows
// only how long the task has been going; Content is set for a completed chat
// task and ErrorMessage for a failed one.
type RenderInput struct {
	Kind         CardKind
	AgentName    string
	TaskID       pgtype.UUID
	Content      string
	ErrorMessage string
	ElapsedSecs  int64
}

// Renderer turns a typed RenderInput into the actual Lark card JSON.
// Centralizing this lets us swap card templates (or A/B them) without
// touching event subscription or persistence code.
type Renderer interface {
	Render(in RenderInput) (CardRender, error)
}

// defaultRenderer produces a schema-2.0 card with a single markdown block.
// update_multi marks it as a shared card, which is what makes it patchable
// after the fact for every viewer rather than per-recipient.
type defaultRenderer struct{}

// Lark documents a 30KB ceiling for a card payload. Stay well under it so
// JSON escaping of user-visible text cannot push a reply over the edge.
const maxCardBytes = 24 * 1024

// NewDefaultRenderer returns the production-default Renderer. Override
// via PatcherConfig.Renderer when a custom template is needed.
func NewDefaultRenderer() Renderer { return &defaultRenderer{} }

func (defaultRenderer) Render(in RenderInput) (CardRender, error) {
	header := "Multica"
	if in.AgentName != "" {
		header = in.AgentName
	}
	body, ok := renderCardBody(in)
	if !ok {
		return CardRender{}, fmt.Errorf("unknown card kind %q", in.Kind)
	}
	doc := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
		},
		"header": map[string]any{
			"template": "blue",
			"title":    map[string]any{"tag": "plain_text", "content": header},
		},
		"body": map[string]any{
			"elements": []any{
				map[string]any{
					"tag":        "markdown",
					"element_id": "agent_progress",
					"content":    body,
				},
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return CardRender{}, err
	}
	if len(raw) <= maxCardBytes {
		return CardRender{JSON: string(raw)}, nil
	}
	// User-visible assistant/error text is variable-sized. Trim by encoded byte
	// budget, then rebuild until the serialized card fits.
	trimRenderInput(&in, maxCardBytes/2)
	for {
		body, _ = renderCardBody(in)
		doc["body"].(map[string]any)["elements"].([]any)[0].(map[string]any)["content"] = body
		raw, err = json.Marshal(doc)
		if err != nil {
			return CardRender{}, err
		}
		if len(raw) <= maxCardBytes || renderInputTextLen(in) == 0 {
			return CardRender{JSON: string(raw)}, nil
		}
		trimRenderInput(&in, renderInputTextLen(in)*3/4)
	}
}

func renderCardBody(in RenderInput) (string, bool) {
	switch in.Kind {
	case CardKindRunning:
		return progressMarkdown(in), true
	case CardKindFinal:
		// Empty content reaches here only when a live card has to be settled:
		// the native path drops it instead of speaking. A card cannot be
		// dropped — leaving it on "正在处理" forever is worse — so it retires
		// with a neutral line in the same language as the rest of the card.
		if in.Content == "" {
			return "**已完成**", true
		}
		return in.Content, true
	case CardKindError:
		if in.ErrorMessage == "" {
			return "**运行失败**", true
		}
		return "**运行失败**\n\n" + in.ErrorMessage, true
	default:
		return "", false
	}
}

func renderInputTextLen(in RenderInput) int {
	if in.Kind == CardKindError {
		return len(in.ErrorMessage)
	}
	return len(in.Content)
}

func trimRenderInput(in *RenderInput, max int) {
	if in.Kind == CardKindError {
		in.ErrorMessage = truncateUTF8Bytes(in.ErrorMessage, max)
		return
	}
	in.Content = truncateUTF8Bytes(in.Content, max)
}

// progressMarkdown is the whole body of a running card: the fact that work is
// happening, and for how long. It deliberately says nothing about what the
// agent is doing — surfacing tool activity meant projecting every task message
// into the row, and that machinery cost far more than the detail was worth.
func progressMarkdown(in RenderInput) string {
	if in.ElapsedSecs > 0 {
		return fmt.Sprintf("**正在处理 · %d 秒**", in.ElapsedSecs)
	}
	return "**正在处理**"
}

func truncateUTF8Bytes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && (s[cut]&0xc0) == 0x80 {
		cut--
	}
	return s[:cut]
}

// PatcherQueries is the narrow subset of *db.Queries the Patcher
// needs. Declared as an interface so the patcher is unit-testable
// without a real Postgres connection.
type PatcherQueries interface {
	GetAgentTask(ctx context.Context, id pgtype.UUID) (db.AgentTaskQueue, error)
	TaskHasChannelIngestedMessages(ctx context.Context, taskID pgtype.UUID) (bool, error)
	GetChatSession(ctx context.Context, id pgtype.UUID) (db.ChatSession, error)
	GetAgent(ctx context.Context, id pgtype.UUID) (db.Agent, error)
	GetLarkInstallation(ctx context.Context, id pgtype.UUID) (Installation, error)
	GetLarkChatSessionBindingBySession(ctx context.Context, chatSessionID pgtype.UUID) (ChatSessionBinding, error)
	ListActiveLarkUserBindingsByMember(ctx context.Context, arg ListInboxNotificationBindingsParams) ([]InboxNotificationBinding, error)
	GetLarkOutboundCardByTask(ctx context.Context, taskID pgtype.UUID) (OutboundCardMessage, error)
	CreateLarkOutboundCardMessage(ctx context.Context, arg CreateOutboundCardMessageParams) (OutboundCardMessage, error)
	ClaimLarkOutboundCardWork(ctx context.Context, arg ClaimOutboundCardWorkParams) ([]OutboundCardMessage, error)
	SetLarkOutboundCardMessageID(ctx context.Context, arg SetOutboundCardMessageIDParams) (OutboundCardMessage, error)
	SettleLarkOutboundCard(ctx context.Context, arg SettleOutboundCardParams) (OutboundCardMessage, error)
	UpdateChatAskChannelMessage(ctx context.Context, arg db.UpdateChatAskChannelMessageParams) error
}

// CredentialsResolver decrypts an installation's app_secret for the
// transport layer. *InstallationService satisfies it directly; tests
// substitute a fake.
type CredentialsResolver interface {
	DecryptAppSecret(inst Installation) (string, error)
}

// PatcherConfig tunes the outbound Patcher. Defaults via withDefaults;
// tests typically override timing, Renderer, Now, or Logger.
type PatcherConfig struct {
	// StreamingDelay keeps fast replies as native messages. A task's progress
	// card is only sent once it has been running this long.
	StreamingDelay time.Duration
	// HeartbeatInterval is how often a live card is repainted with its new
	// elapsed time. Enforced by Postgres, so it holds across replicas.
	HeartbeatInterval  time.Duration
	WorkerPollInterval time.Duration
	// MaxBatch bounds how many cards one poll may paint.
	MaxBatch int
	Metrics  OutboundMetrics
	// Renderer drives the progress card and its terminal states. Replies that
	// never grew a card keep their native text, markdown-card, or
	// confirmation-card presentation.
	Renderer Renderer
	Now      func() time.Time
	Logger   *slog.Logger
}

type OutboundMetrics interface {
	RecordCardStarted(seconds float64)
	RecordDelivery(status, outcome string)
}

func (c PatcherConfig) withDefaults() PatcherConfig {
	if c.StreamingDelay == 0 {
		c.StreamingDelay = 7 * time.Second
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 30 * time.Second
	}
	if c.WorkerPollInterval == 0 {
		c.WorkerPollInterval = time.Second
	}
	if c.MaxBatch <= 0 {
		c.MaxBatch = 50
	}
	if c.Renderer == nil {
		c.Renderer = NewDefaultRenderer()
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// Patcher reacts to task-lifecycle events on the event bus and forwards chat
// replies to Lark. A run that answers quickly keeps a native-feeling reply. A
// run that crosses StreamingDelay grows one progress card, which is repainted
// with its elapsed time and finally replaced in place by the answer.
//
// Scope:
//
//   - Only tasks whose chat_session has a lark_chat_session_binding produce
//     outbound. Tasks born from the web UI or autopilot pass through unchanged.
//
//   - EventTaskRunning only records the card row. Every remote call belongs to
//     the worker, so the synchronous bus path never blocks on Lark.
//
//   - The card carries no transcript — only that work is in progress and for
//     how long. Reasoning, tool payloads and partial text never reach a channel.
type Patcher struct {
	queries         PatcherQueries
	credentials     CredentialsResolver
	client          APIClient
	typingIndicator *TypingIndicatorManager
	cfg             PatcherConfig
}

// NewPatcher constructs a Patcher bound to its dependencies. The
// patcher does not subscribe to the bus until Register is called.
func NewPatcher(queries PatcherQueries, credentials CredentialsResolver, client APIClient, cfg PatcherConfig) *Patcher {
	cfg = cfg.withDefaults()
	return &Patcher{
		queries:     queries,
		credentials: credentials,
		client:      client,
		cfg:         cfg,
	}
}

// SetTypingIndicatorManager wires the typing-indicator manager into the
// patcher so that replies clear the "processing" reaction before they
// are sent. Call once at boot after both the patcher and manager are
// constructed. Nil disables the clear step.
func (p *Patcher) SetTypingIndicatorManager(m *TypingIndicatorManager) {
	p.typingIndicator = m
}

// Register subscribes the patcher to the task-lifecycle events it cares about
// on the supplied bus. Idempotent only if you call it against a fresh bus; call
// sites should invoke it exactly once during server boot (after the bus and
// patcher are constructed and before HTTP traffic starts).
//
// EventTaskRunning opens the card; terminal chat, failure and cancellation
// events settle it. EventTaskCompleted stays unsubscribed because EventChatDone
// carries the actual reply body. EventTaskMessage stays unsubscribed too: the
// card reports elapsed time rather than transcript, so a wakeup per streamed
// token would buy nothing.
func (p *Patcher) Register(bus *events.Bus) {
	bus.Subscribe(protocol.EventTaskFailed, p.handleEvent)
	bus.Subscribe(protocol.EventTaskCancelled, p.handleEvent)
	bus.Subscribe(protocol.EventTaskRunning, p.handleEvent)
	bus.Subscribe(protocol.EventChatDone, p.handleEvent)
	// Structured asks (docs/chat-ask-structured-signal-spec.md): render the
	// declared interaction, and patch the card into its receipt form once
	// the ask leaves the pending state.
	bus.Subscribe(protocol.EventChatAsk, p.handleEvent)
	bus.Subscribe(protocol.EventChatAskResolved, p.handleEvent)
}

func (p *Patcher) handleEvent(e events.Event) {
	// Use a fresh background ctx with a tight timeout: bus delivery is
	// synchronous so a stuck Lark HTTP call would otherwise wedge the
	// whole publish call site.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.processEvent(ctx, e); err != nil {
		p.cfg.Logger.Warn("lark patcher: event handling failed",
			"event_type", e.Type,
			"task_id", e.TaskID,
			"chat_session_id", e.ChatSessionID,
			"error", err,
		)
	}
}

func (p *Patcher) processEvent(ctx context.Context, e events.Event) error {
	taskID, chatSessionID, ok := taskAndSessionFromEvent(e)
	if !ok {
		return nil
	}
	task, err := p.queries.GetAgentTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("load agent task: %w", err)
	}
	if !chatSessionID.Valid {
		chatSessionID = task.ChatSessionID
	}
	if !chatSessionID.Valid {
		// Issue / autopilot tasks have no chat_session.
		return nil
	}
	binding, err := p.queries.GetLarkChatSessionBindingBySession(ctx, chatSessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Web-only chat session — not a Lark target.
			return nil
		}
		return fmt.Errorf("lookup chat session binding: %w", err)
	}

	// Only bound sessions reach here, so classify the task origin before
	// spending any send work. Web/mobile direct-chat tasks can reuse a session
	// that originated in Lark, but their replies belong only in Multica.
	// Sealed channel tasks own an input batch just like direct tasks, so the
	// discriminator is the immutable channel_ingested provenance of that
	// batch, not chat_input_task_id presence (which #5645 originally used).
	deliver, err := engine.TaskInputIsChannelIngested(ctx, p.queries, task)
	if err != nil {
		return fmt.Errorf("classify task input origin: %w", err)
	}
	if !deliver {
		return nil
	}
	if e.Type == protocol.EventTaskRunning {
		return p.openCard(ctx, binding, task)
	}

	inst, err := p.queries.GetLarkInstallation(ctx, binding.InstallationID)
	if err != nil {
		return fmt.Errorf("load installation: %w", err)
	}
	if InstallationStatus(inst.Status) != InstallationActive {
		// Revoked between trigger and event; nothing to patch.
		return nil
	}
	creds, err := p.installationCredentials(inst)
	if err != nil {
		return err
	}

	agent, agentErr := p.queries.GetAgent(ctx, inst.AgentID)
	agentName := ""
	if agentErr == nil {
		agentName = agent.Name
	}

	switch e.Type {
	case protocol.EventChatDone:
		p.clearTyping(ctx, chatSessionID)
		return p.settleReply(ctx, creds, inst, binding, taskID, agentName, RenderInput{
			Kind: CardKindFinal, Content: chatDoneContent(e.Payload),
		})
	case protocol.EventTaskFailed:
		p.clearTyping(ctx, chatSessionID)
		return p.settleReply(ctx, creds, inst, binding, taskID, agentName, RenderInput{
			Kind: CardKindError, ErrorMessage: errorMessageFromPayload(e.Payload),
		})
	case protocol.EventTaskCancelled:
		p.clearTyping(ctx, chatSessionID)
		return p.settleReply(ctx, creds, inst, binding, taskID, agentName, RenderInput{
			Kind: CardKindFinal, Content: "已取消",
		})
	case protocol.EventChatAsk:
		p.clearTyping(ctx, chatSessionID)
		payload, ok := chatAskPayloadFromEvent(e.Payload)
		if !ok {
			return nil
		}
		return p.sendChatAsk(ctx, creds, inst, binding, payload)
	case protocol.EventChatAskResolved:
		p.clearTyping(ctx, chatSessionID)
		payload, ok := chatAskResolvedPayloadFromEvent(e.Payload)
		if !ok {
			return nil
		}
		return p.patchChatAskResolved(ctx, creds, payload)
	}
	return nil
}

func (p *Patcher) clearTyping(ctx context.Context, chatSessionID pgtype.UUID) {
	if p.typingIndicator != nil {
		p.typingIndicator.Clear(ctx, chatSessionID)
	}
}

// openCard records the card row for a task that has started running. Nothing
// goes to Lark yet: the worker sends the card once StreamingDelay has passed,
// so a task that answers before then never grows card chrome at all.
func (p *Patcher) openCard(ctx context.Context, binding ChatSessionBinding, task db.AgentTaskQueue) error {
	switch task.Status {
	case "completed", "failed", "cancelled":
		return nil
	}
	_, err := p.queries.CreateLarkOutboundCardMessage(ctx, CreateOutboundCardMessageParams{
		ChatSessionID: binding.ChatSessionID,
		ChannelChatID: string(outboundChatID(binding)),
		TaskID:        task.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// A card already exists for this task: duplicate EventTaskRunning from a
		// bus replay or a second replica.
		return nil
	}
	if err != nil {
		return fmt.Errorf("open progress card: %w", err)
	}
	return nil
}

// settleReply delivers a task's single terminal message and closes its card.
//
// A live card is patched in place, so the progress the user was already
// watching becomes the answer instead of scrolling away above a second message.
// Otherwise the reply is sent on its own, and the wire shape is chosen per
// reply: plain prose goes out as msg_type=text, because a one-line "Hi!" should
// feel like a normal IM message rather than a notification card with chrome;
// anything containing markdown goes out as a card with a markdown body element
// so Lark renders the formatting instead of leaving raw ** in the transcript.
func (p *Patcher) settleReply(ctx context.Context, creds InstallationCredentials, inst Installation, binding ChatSessionBinding, taskID pgtype.UUID, agentName string, in RenderInput) error {
	card, owned, err := p.claimReply(ctx, taskID, in.Kind)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	in.AgentName = agentName
	in.TaskID = taskID
	if card.ChannelCardMessageID != "" {
		patchErr := p.patchTerminalCard(ctx, creds, inst, binding, card, in)
		if patchErr == nil {
			p.recordDelivery(card.Status, "patched")
			return nil
		}
		// The stale card is left on screen, but a card we cannot patch must
		// never swallow the answer: say it again as an ordinary message.
		p.cfg.Logger.Warn("lark: terminal card patch failed, falling back to a native reply",
			"task_id", uuidString(taskID), "error", patchErr)
		p.recordDelivery(card.Status, "patch_failed")
	}
	return p.sendNativeReply(ctx, creds, inst, binding, taskID, in)
}

// claimReply flips the task's card into its terminal status and reports whether
// this caller now owns the reply. chat:done, task:failed and a cancel all race
// for the same task, and only the event that wins that write may speak. A row
// that is already terminal means somebody else won. No row at all means the
// task never opened a card, which is the ordinary fast-reply path and still
// speaks.
func (p *Patcher) claimReply(ctx context.Context, taskID pgtype.UUID, kind CardKind) (OutboundCardMessage, bool, error) {
	status := CardStatusFinal
	if kind == CardKindError {
		status = CardStatusError
	}
	card, err := p.queries.SettleLarkOutboundCard(ctx, SettleOutboundCardParams{
		TaskID: taskID, Status: string(status),
	})
	if err == nil {
		return card, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return OutboundCardMessage{}, false, fmt.Errorf("settle outbound card: %w", err)
	}
	_, loadErr := p.queries.GetLarkOutboundCardByTask(ctx, taskID)
	if errors.Is(loadErr, pgx.ErrNoRows) {
		return OutboundCardMessage{}, true, nil
	}
	if loadErr != nil {
		return OutboundCardMessage{}, false, fmt.Errorf("load outbound card: %w", loadErr)
	}
	return OutboundCardMessage{}, false, nil
}

func (p *Patcher) patchTerminalCard(ctx context.Context, creds InstallationCredentials, inst Installation, binding ChatSessionBinding, card OutboundCardMessage, in RenderInput) error {
	cardJSON, err := p.renderTerminalCard(ctx, inst, binding, card.TaskID, in)
	if err != nil {
		return err
	}
	return p.client.PatchInteractiveCard(ctx, PatchCardParams{
		InstallationID:    creds,
		LarkCardMessageID: card.ChannelCardMessageID,
		CardJSON:          cardJSON,
	})
}

// renderTerminalCard picks the template for a settled reply. A reply that asks
// the user to confirm something renders as the interactive confirmation card
// when the requester's Lark identity is known, so the buttons stay usable after
// the progress card turns into the answer.
func (p *Patcher) renderTerminalCard(ctx context.Context, inst Installation, binding ChatSessionBinding, taskID pgtype.UUID, in RenderInput) (string, error) {
	if in.Kind == CardKindFinal && chatReplyNeedsConfirmationAction(in.Content) {
		if allowedOpenID, ok := p.confirmationAllowedOpenID(ctx, inst.WorkspaceID, binding.InstallationID, taskID); ok {
			return renderConfirmationCard(in.Content, binding, uuidString(taskID), allowedOpenID, p.cfg.Now())
		}
	}
	render, err := p.cfg.Renderer.Render(in)
	if err != nil {
		return "", fmt.Errorf("render terminal card: %w", err)
	}
	return render.JSON, nil
}

// sendNativeReply posts a settled reply as its own message: for tasks that
// never grew a card, and as the fallback when a live card cannot be patched.
func (p *Patcher) sendNativeReply(ctx context.Context, creds InstallationCredentials, inst Installation, binding ChatSessionBinding, taskID pgtype.UUID, in RenderInput) error {
	target := threadReplyTarget(binding)
	if in.Kind == CardKindError {
		// A failure keeps its own card so it stays visually distinct from a
		// successful reply.
		render, err := p.cfg.Renderer.Render(in)
		if err != nil {
			return fmt.Errorf("render error card: %w", err)
		}
		return sendWithThreadFallback(p.cfg.Logger, "send error card", target, func(t ReplyTarget) error {
			_, err := p.client.SendInteractiveCard(ctx, SendCardParams{
				InstallationID: creds, ChatID: outboundChatID(binding), CardJSON: render.JSON, ReplyTarget: t,
			})
			return err
		})
	}
	// Empty content is silently dropped: we'd rather show nothing than "Done."
	// (the card fallback that confused Bohan in the live dev env). In practice
	// an empty Content means the daemon completed the task without producing
	// visible output, which only happens for edge cases like a chat task that
	// just acknowledged a system event; not emitting a message there is the
	// right product call.
	if in.Content == "" {
		return nil
	}
	if chatReplyNeedsConfirmationAction(in.Content) {
		if allowedOpenID, ok := p.confirmationAllowedOpenID(ctx, inst.WorkspaceID, binding.InstallationID, taskID); ok {
			return p.sendConfirmationCard(ctx, creds, binding, taskID, allowedOpenID, in.Content, target)
		}
		p.cfg.Logger.Warn("lark: confirmation prompt fell back to native reply because requester binding was unavailable",
			"task_id", uuidString(taskID),
			"chat_type", binding.ChatType)
	}
	if containsMarkdown(in.Content) {
		return sendWithThreadFallback(p.cfg.Logger, "send markdown card", target, func(t ReplyTarget) error {
			_, err := p.client.SendMarkdownCard(ctx, SendMarkdownCardParams{
				InstallationID: creds,
				ChatID:         outboundChatID(binding),
				Markdown:       in.Content,
				ReplyTarget:    t,
			})
			return err
		})
	}
	return sendWithThreadFallback(p.cfg.Logger, "send text message", target, func(t ReplyTarget) error {
		_, err := p.client.SendTextMessage(ctx, SendTextParams{
			InstallationID: creds,
			ChatID:         outboundChatID(binding),
			Text:           in.Content,
			ReplyTarget:    t,
		})
		return err
	})
}

func (p *Patcher) recordDelivery(status, outcome string) {
	if p.cfg.Metrics != nil {
		p.cfg.Metrics.RecordDelivery(status, outcome)
	}
}

func (p *Patcher) sendConfirmationCard(ctx context.Context, creds InstallationCredentials, binding ChatSessionBinding, taskID pgtype.UUID, allowedOpenID, content string, target ReplyTarget) error {
	cardJSON, err := renderConfirmationCard(content, binding, uuidString(taskID), allowedOpenID, p.cfg.Now())
	if err != nil {
		return fmt.Errorf("render confirmation card: %w", err)
	}
	return sendWithThreadFallback(p.cfg.Logger, "send confirmation card", target, func(t ReplyTarget) error {
		_, err := p.client.SendInteractiveCard(ctx, SendCardParams{
			InstallationID: creds,
			ChatID:         outboundChatID(binding),
			CardJSON:       cardJSON,
			ReplyTarget:    t,
		})
		return err
	})
}

func (p *Patcher) confirmationAllowedOpenID(ctx context.Context, workspaceID, installationID, taskID pgtype.UUID) (string, bool) {
	if !workspaceID.Valid || !installationID.Valid || !taskID.Valid {
		return "", false
	}
	task, err := p.queries.GetAgentTask(ctx, taskID)
	if err != nil || !task.InitiatorUserID.Valid {
		return "", false
	}
	rows, err := p.queries.ListActiveLarkUserBindingsByMember(ctx, ListInboxNotificationBindingsParams{
		WorkspaceID:   workspaceID,
		MulticaUserID: task.InitiatorUserID,
	})
	if err != nil {
		return "", false
	}
	for _, row := range rows {
		if uuidEqual(row.Installation.ID, installationID) && row.UserBinding.ChannelUserID != "" {
			return row.UserBinding.ChannelUserID, true
		}
	}
	return "", false
}

// outboundChatID recovers the real Lark chat id from the chat binding. The
// channel_chat_id may be a composite "chat:thread" topic-isolation key, so
// the real chat id is read from the binding config (larkBindingConfig);
// pre-topic rows (config "{}") route by the key itself, which for them IS the
// real chat id.
func outboundChatID(b ChatSessionBinding) ChatID {
	if len(b.Config) > 0 {
		var cfg larkBindingConfig
		if err := json.Unmarshal(b.Config, &cfg); err == nil && cfg.ChatID != "" {
			return ChatID(cfg.ChatID)
		}
	}
	return ChatID(b.ChannelChatID)
}

// threadReplyTarget derives the outbound reply target from the chat
// binding's most-recent inbound trigger. We thread the reply ONLY when
// that trigger was itself inside a Lark topic (last_lark_thread_id
// present): normal group / p2p chats keep the unchanged chat-level send
// path, and only an @-mention that happened inside a thread gets a
// threaded reply (replying to last_lark_message_id with reply_in_thread).
// The zero ReplyTarget means "send at the chat level".
func threadReplyTarget(binding ChatSessionBinding) ReplyTarget {
	if binding.LastThreadID.Valid && binding.LastThreadID.String != "" &&
		binding.LastMessageID.Valid && binding.LastMessageID.String != "" {
		return ReplyTarget{MessageID: binding.LastMessageID.String, InThread: true}
	}
	return ReplyTarget{}
}

// sendWithThreadFallback runs send with the thread reply target and,
// ONLY when the threaded attempt fails with a Lark error that means the
// topic reply legitimately cannot land (trigger message recalled, topic
// gone, topics disabled, aggregated message — see
// threadReplyUnsupportedCodes), retries once at the chat level so the
// reply is not silently lost. Any other failure — transport error,
// 5xx, timeout, rate limit, or an ambiguous "the server may have
// received it" error — is logged and returned as a failure rather than
// retried: a blind chat-level retry could duplicate the reply or leak a
// thread-only reply into the main group chat. When target is already
// chat-level there is nothing to fall back to and the error is returned.
//
// It is a package-level function (rather than a Patcher method) so the
// event-driven Patcher and the immediate OutcomeReplier share one
// classified fallback path.
func sendWithThreadFallback(log *slog.Logger, op string, target ReplyTarget, send func(ReplyTarget) error) error {
	err := send(target)
	if err == nil {
		return nil
	}
	if target.IsSet() && isThreadReplyUnsupported(err) {
		log.Warn("lark: thread reply unsupported for target, retrying at chat level",
			"op", op, "reply_message_id", target.MessageID, "error", err)
		if fallbackErr := send(ReplyTarget{}); fallbackErr != nil {
			return fmt.Errorf("%s (chat-level fallback after thread-unsupported reply: %v): %w", op, err, fallbackErr)
		}
		return nil
	}
	if target.IsSet() {
		log.Warn("lark: thread reply failed; not falling back (non-classified error)",
			"op", op, "reply_message_id", target.MessageID, "error", err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

func (p *Patcher) installationCredentials(inst Installation) (InstallationCredentials, error) {
	if p.credentials == nil {
		return InstallationCredentials{}, errors.New("lark patcher: credentials resolver missing")
	}
	secret, err := p.credentials.DecryptAppSecret(inst)
	if err != nil {
		return InstallationCredentials{}, fmt.Errorf("decrypt app_secret: %w", err)
	}
	creds := InstallationCredentials{
		AppID:     inst.AppID,
		AppSecret: secret,
		Region:    RegionOrDefault(inst.Region),
	}
	if inst.TenantKey.Valid {
		creds.TenantKey = inst.TenantKey.String
	}
	return creds, nil
}

// taskAndSessionFromEvent parses the typed-ish payload broadcastTaskEvent
// publishes — a map[string]any with `task_id` (always) and
// `chat_session_id` (chat tasks only). EventChatDone carries a
// ChatDonePayload struct instead.
func taskAndSessionFromEvent(e events.Event) (taskID, chatSessionID pgtype.UUID, ok bool) {
	if e.TaskID != "" {
		if err := taskID.Scan(e.TaskID); err != nil {
			taskID = pgtype.UUID{}
		}
	}
	if e.ChatSessionID != "" {
		if err := chatSessionID.Scan(e.ChatSessionID); err != nil {
			chatSessionID = pgtype.UUID{}
		}
	}
	switch p := e.Payload.(type) {
	case map[string]any:
		if !taskID.Valid {
			if s, _ := p["task_id"].(string); s != "" {
				_ = taskID.Scan(s)
			}
		}
		if !chatSessionID.Valid {
			if s, _ := p["chat_session_id"].(string); s != "" {
				_ = chatSessionID.Scan(s)
			}
		}
	case protocol.ChatDonePayload:
		if !taskID.Valid {
			_ = taskID.Scan(p.TaskID)
		}
		if !chatSessionID.Valid {
			_ = chatSessionID.Scan(p.ChatSessionID)
		}
	case protocol.ChatAskPayload:
		if !taskID.Valid {
			_ = taskID.Scan(p.TaskID)
		}
		if !chatSessionID.Valid {
			_ = chatSessionID.Scan(p.ChatSessionID)
		}
	case protocol.ChatAskResolvedPayload:
		if !taskID.Valid {
			_ = taskID.Scan(p.TaskID)
		}
		if !chatSessionID.Valid {
			_ = chatSessionID.Scan(p.ChatSessionID)
		}
	}
	return taskID, chatSessionID, taskID.Valid
}

func chatDoneContent(payload any) string {
	switch p := payload.(type) {
	case protocol.ChatDonePayload:
		return p.Content
	case map[string]any:
		if s, ok := p["content"].(string); ok {
			return s
		}
	}
	return ""
}

func errorMessageFromPayload(payload any) string {
	if m, ok := payload.(map[string]any); ok {
		if s, ok := m["error"].(string); ok {
			return s
		}
		if s, ok := m["error_message"].(string); ok {
			return s
		}
	}
	return ""
}
