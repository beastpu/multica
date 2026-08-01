package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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

// CardKind enumerates the small set of card variants the patcher
// renders. The Renderer is plug-replaceable so the on-wire card
// template can evolve without touching the patcher's transport / DB
// logic.
type CardKind string

const (
	CardKindThinking CardKind = "thinking"
	CardKindRunning  CardKind = "running"
	CardKindFinal    CardKind = "final"
	CardKindError    CardKind = "error"
)

// CardRender is the rendered card body the Renderer produces. The
// patcher serializes the JSON before handing it to APIClient.
type CardRender struct {
	JSON string
}

// RenderInput is the (typed) snapshot the Renderer sees when building
// or patching a card. Fields are populated as they become available
// during a task lifecycle — IssueNumber is set for `/issue` flows,
// Content is set for completed chat tasks, ErrorMessage for failed.
type RenderInput struct {
	Kind         CardKind
	AgentName    string
	IssueNumber  int32
	IssueID      pgtype.UUID
	TaskID       pgtype.UUID
	Content      string
	ErrorMessage string
	Stage        string
	ElapsedSecs  int64
	FilesRead    int32
	FilesEdited  int32
	Searches     int32
	Commands     int32
}

// Renderer turns a typed RenderInput into the actual Lark card JSON.
// Centralizing this lets us swap card templates (or A/B them) without
// touching event subscription or persistence code.
type Renderer interface {
	Render(in RenderInput) (CardRender, error)
}

// defaultRenderer produces CardKit 2.0 cards with native streaming enabled
// while work is in progress and explicitly disabled for terminal revisions.
type defaultRenderer struct{}

const maxStreamingCardBytes = 24 * 1024

const (
	progressStageProcessing     = "processing"
	progressStageReadingFiles   = "reading_files"
	progressStageSearching      = "searching"
	progressStageEditingFiles   = "editing_files"
	progressStageRunningCommand = "running_command"
	progressStageResponding     = "responding"
)

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
	streaming := in.Kind == CardKindThinking || in.Kind == CardKindRunning
	doc := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
			"update_multi":     true,
			"streaming_mode":   streaming,
			"summary": map[string]any{
				"content": map[bool]string{true: "正在生成回复…", false: "Multica 回复"}[streaming],
			},
			"streaming_config": map[string]any{
				"print_frequency_ms": map[string]any{"default": 70, "android": 70, "ios": 70, "pc": 70},
				"print_step":         map[string]any{"default": 1, "android": 1, "ios": 1, "pc": 1},
				"print_strategy":     "fast",
			},
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
	if len(raw) <= maxStreamingCardBytes {
		return CardRender{JSON: string(raw)}, nil
	}
	// User-visible assistant/error text is variable-sized. Trim by encoded byte
	// budget, then rebuild so the final serialized card stays below CardKit's
	// documented 30KB limit with headroom for JSON escaping.
	trimRenderInput(&in, maxStreamingCardBytes/2)
	body, _ = renderCardBody(in)
	doc["body"].(map[string]any)["elements"].([]any)[0].(map[string]any)["content"] = body
	raw, err = json.Marshal(doc)
	if err != nil {
		return CardRender{}, err
	}
	for len(raw) > maxStreamingCardBytes && renderInputTextLen(in) > 0 {
		trimRenderInput(&in, renderInputTextLen(in)*3/4)
		body, _ = renderCardBody(in)
		doc["body"].(map[string]any)["elements"].([]any)[0].(map[string]any)["content"] = body
		raw, err = json.Marshal(doc)
		if err != nil {
			return CardRender{}, err
		}
	}
	return CardRender{JSON: string(raw)}, nil
}

func renderCardBody(in RenderInput) (string, bool) {
	switch in.Kind {
	case CardKindThinking, CardKindRunning:
		return progressMarkdown(in, "正在处理…"), true
	case CardKindFinal:
		if in.Content == "" {
			return "Done.", true
		}
		return in.Content, true
	case CardKindError:
		if in.ErrorMessage == "" {
			return "Run failed.", true
		}
		return "Run failed: " + in.ErrorMessage, true
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

type taskProgressProjection struct {
	Seq               int32
	Stage             string
	VisibleTextAppend string
	FilesReadDelta    int32
	FilesEditedDelta  int32
	SearchesDelta     int32
	CommandsDelta     int32
}

func projectTaskProgress(message protocol.TaskMessagePayload) taskProgressProjection {
	p := taskProgressProjection{Seq: int32(message.Seq), Stage: progressStageProcessing}
	switch message.Type {
	case "text":
		p.Stage = progressStageResponding
		p.VisibleTextAppend = message.Content
	case "tool_use":
		tool := strings.ToLower(message.Tool)
		switch {
		case strings.Contains(tool, "read"), strings.Contains(tool, "glob"):
			p.Stage = progressStageReadingFiles
			p.FilesReadDelta = 1
		case strings.Contains(tool, "grep"), strings.Contains(tool, "search"):
			p.Stage = progressStageSearching
			p.SearchesDelta = 1
		case strings.Contains(tool, "edit"), strings.Contains(tool, "write"):
			p.Stage = progressStageEditingFiles
			p.FilesEditedDelta = 1
		case strings.Contains(tool, "bash"), strings.Contains(tool, "exec"), strings.Contains(tool, "terminal"):
			p.Stage = progressStageRunningCommand
			p.CommandsDelta = 1
		}
	}
	return p
}

func progressStageLabel(stage string) string {
	switch stage {
	case progressStageReadingFiles:
		return "正在读取文件"
	case progressStageSearching:
		return "正在搜索"
	case progressStageEditingFiles:
		return "正在修改文件"
	case progressStageRunningCommand:
		return "正在运行命令"
	case progressStageResponding:
		return "正在组织回复"
	default:
		return "正在处理"
	}
}

func progressMarkdown(in RenderInput, fallback string) string {
	stage := progressStageLabel(in.Stage)
	if in.ElapsedSecs > 0 {
		stage += fmt.Sprintf(" · %d 秒", in.ElapsedSecs)
	}
	parts := []string{"**" + stage + "**"}
	if strings.TrimSpace(in.Content) != "" {
		parts = append(parts, strings.TrimSpace(in.Content))
	} else if fallback != "" {
		parts = append(parts, fallback)
	}
	var activity []string
	if in.FilesRead > 0 {
		activity = append(activity, fmt.Sprintf("读取 %d 个文件", in.FilesRead))
	}
	if in.FilesEdited > 0 {
		activity = append(activity, fmt.Sprintf("修改 %d 个文件", in.FilesEdited))
	}
	if in.Searches > 0 {
		activity = append(activity, fmt.Sprintf("搜索 %d 次", in.Searches))
	}
	if in.Commands > 0 {
		activity = append(activity, fmt.Sprintf("执行 %d 条命令", in.Commands))
	}
	if len(activity) > 0 {
		parts = append(parts, "---\n"+strings.Join(activity, " · "))
	}
	return strings.Join(parts, "\n\n")
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
	ListTaskMessagesSince(ctx context.Context, arg db.ListTaskMessagesSinceParams) ([]db.TaskMessage, error)
	CreateLarkOutboundCardMessage(ctx context.Context, arg CreateOutboundCardMessageParams) (OutboundCardMessage, error)
	ProjectLarkOutboundTaskMessage(ctx context.Context, arg ProjectOutboundTaskMessageParams) (OutboundCardMessage, error)
	ScheduleLarkOutboundTaskMessage(ctx context.Context, arg ScheduleOutboundTaskMessageParams) (OutboundCardMessage, error)
	SetLarkOutboundTerminalDesired(ctx context.Context, arg SetOutboundTerminalDesiredParams) (OutboundCardMessage, error)
	ClaimLarkOutboundCardDelivery(ctx context.Context, arg ClaimOutboundCardDeliveryParams) (OutboundCardMessage, error)
	SetLarkOutboundInflightPayload(ctx context.Context, arg SetOutboundInflightPayloadParams) (OutboundCardMessage, error)
	SetLarkOutboundCardEntityID(ctx context.Context, arg SetOutboundCardEntityIDParams) (OutboundCardMessage, error)
	SetLarkOutboundCardMessageID(ctx context.Context, arg SetOutboundCardMessageIDParams) (OutboundCardMessage, error)
	DowngradeLarkOutboundCardTransport(ctx context.Context, arg OutboundDeliveryLeaseParams) (OutboundCardMessage, error)
	CompleteLarkOutboundCardDelivery(ctx context.Context, arg OutboundDeliveryLeaseParams) (OutboundCardMessage, error)
	FailLarkOutboundCardDelivery(ctx context.Context, arg FailOutboundCardDeliveryParams) (OutboundCardMessage, error)
	AbandonLarkOutboundCardDelivery(ctx context.Context, arg AbandonOutboundCardDeliveryParams) (OutboundCardMessage, error)
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
	// StreamingDelay keeps fast replies as native messages. A task becomes
	// eligible for a progress card only after it has been running this long.
	StreamingDelay time.Duration
	// MinPatchInterval is enforced by Postgres across server replicas.
	MinPatchInterval   time.Duration
	WorkerPollInterval time.Duration
	WorkerConcurrency  int
	DeliveryLease      time.Duration
	Metrics            OutboundMetrics
	// Renderer drives the delayed progress card and its terminal states.
	// Fast EventChatDone replies bypass it and keep their native text,
	// markdown-card, or confirmation-card presentation.
	Renderer Renderer
	Now      func() time.Time
	Logger   *slog.Logger
}

type OutboundMetrics interface {
	RecordCardStarted(transport string, seconds float64)
	RecordDelivery(transport, status, outcome string, lagSeconds float64)
	RecordFallback(reason string)
}

func (c PatcherConfig) withDefaults() PatcherConfig {
	if c.StreamingDelay == 0 {
		c.StreamingDelay = 7 * time.Second
	}
	if c.MinPatchInterval == 0 {
		c.MinPatchInterval = 2 * time.Second
	}
	if c.WorkerPollInterval == 0 {
		c.WorkerPollInterval = 500 * time.Millisecond
	}
	if c.WorkerConcurrency <= 0 {
		c.WorkerConcurrency = 4
	}
	if c.DeliveryLease == 0 {
		c.DeliveryLease = 30 * time.Second
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
// replies to Lark. Fast runs keep native-feeling final replies. Runs that cross
// StreamingDelay get one live card which is patched with visible assistant text
// and then settled in place, avoiding both a long silent wait and card chrome on
// the common fast path.
//
// Scope:
//
//   - Only tasks whose chat_session has a lark_chat_session_binding
//     produce outbound. Tasks born from the web UI or autopilot pass
//     through unchanged.
//
//   - EventTaskRunning durably schedules a card for StreamingDelay; task
//     messages update its public-safe projection. Database rows coordinate
//     delivery and MinPatchInterval across replicas.
//
//   - Only persisted visible text is streamed; reasoning and tool payloads are
//     never rendered into the channel.
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

// Register subscribes the patcher to the task-lifecycle events it
// cares about on the supplied bus. Idempotent only if you call it
// against a fresh bus; call sites should invoke it exactly once
// during server boot (after the bus + patcher are constructed and
// before HTTP traffic starts).
//
// EventTaskRunning schedules the quiet-run fallback, EventTaskMessage drives
// progress updates, and terminal chat, failure, and cancellation events settle
// the card. EventTaskCompleted stays unsubscribed because EventChatDone carries
// the actual reply body.
func (p *Patcher) Register(bus *events.Bus) {
	bus.Subscribe(protocol.EventTaskFailed, p.handleEvent)
	bus.Subscribe(protocol.EventTaskCancelled, p.handleEvent)
	bus.Subscribe(protocol.EventTaskRunning, p.handleEvent)
	bus.Subscribe(protocol.EventTaskMessage, p.handleEvent)
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
		return p.scheduleStreamingStart(ctx, binding, task)
	}
	if e.Type == protocol.EventTaskMessage {
		if _, ok := taskMessagePayloadFromEvent(e.Payload); !ok {
			return nil
		}
		return p.scheduleTaskMessage(ctx, binding, task)
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
		return p.sendChatReply(ctx, creds, inst, binding, taskID, agentName, e.Payload)
	case protocol.EventTaskFailed:
		p.clearTyping(ctx, chatSessionID)
		return p.fail(ctx, creds, binding, taskID, agentName, e.Payload)
	case protocol.EventTaskCancelled:
		p.clearTyping(ctx, chatSessionID)
		return p.cancelStreamCard(ctx, creds, binding, taskID, agentName)
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

func (p *Patcher) scheduleStreamingStart(ctx context.Context, binding ChatSessionBinding, task db.AgentTaskQueue) error {
	switch task.Status {
	case "completed", "failed", "cancelled":
		return nil
	}
	_, err := p.queries.CreateLarkOutboundCardMessage(ctx, CreateOutboundCardMessageParams{
		ChatSessionID:        binding.ChatSessionID,
		ChannelChatID:        string(outboundChatID(binding)),
		ChannelCardMessageID: "",
		Status:               string(CardStatusPending),
		TaskID:               task.ID,
		StartDelaySeconds:    p.cfg.StreamingDelay.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("schedule streaming card: %w", err)
	}
	return nil
}

func (p *Patcher) scheduleTaskMessage(ctx context.Context, binding ChatSessionBinding, task db.AgentTaskQueue) error {
	switch task.Status {
	case "completed", "failed", "cancelled":
		return nil
	}
	if _, err := p.queries.GetLarkOutboundCardByTask(ctx, task.ID); errors.Is(err, pgx.ErrNoRows) {
		delay := p.cfg.StreamingDelay
		if task.CreatedAt.Valid {
			delay -= p.cfg.Now().Sub(task.CreatedAt.Time)
			if delay < 0 {
				delay = 0
			}
		}
		_, err = p.queries.CreateLarkOutboundCardMessage(ctx, CreateOutboundCardMessageParams{
			ChatSessionID: binding.ChatSessionID, ChannelChatID: string(outboundChatID(binding)),
			ChannelCardMessageID: "", Status: string(CardStatusPending), TaskID: task.ID,
			StartDelaySeconds: delay.Seconds(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("recover missing streaming schedule: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("load streaming projection: %w", err)
	}
	_, err := p.queries.ScheduleLarkOutboundTaskMessage(ctx, ScheduleOutboundTaskMessageParams{
		TaskID:             task.ID,
		MinIntervalSeconds: p.cfg.MinPatchInterval.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("schedule streaming task message: %w", err)
	}
	return nil
}

func taskMessagePayloadFromEvent(payload any) (protocol.TaskMessagePayload, bool) {
	switch value := payload.(type) {
	case protocol.TaskMessagePayload:
		return value, true
	case *protocol.TaskMessagePayload:
		if value != nil {
			return *value, true
		}
	}
	return protocol.TaskMessagePayload{}, false
}

// sendChatReply turns ChatDonePayload.Content into a Lark message.
// The wire shape is chosen per-reply based on whether the body
// contains any markdown syntax:
//
//   - Plain prose (no markdown) → `msg_type=text`. A one-line "Hi!"
//     reply should feel like a normal IM message, not a notification
//     card with chrome around it.
//
//   - Anything with markdown (headings, lists, code blocks, tables,
//     bold/italic, links) → schema-2.0 interactive card with a
//     `tag: "markdown"` body element so Lark's client renders the
//     formatting instead of leaving raw `**bold**` characters in
//     the transcript. The card is visually subtler than the legacy
//     binding-prompt template — just a single markdown block, no
//     header / icon / CTA buttons.
//
// Empty content is silently dropped: we'd rather show nothing than
// "Done." (the prior card fallback that confused Bohan in the live
// dev env). In practice an empty Content means the daemon completed
// the task without producing visible output, which only happens for
// edge cases like a chat task that just acknowledged a system event;
// not emitting a message there is the right product call.
func (p *Patcher) sendChatReply(ctx context.Context, creds InstallationCredentials, inst Installation, binding ChatSessionBinding, taskID pgtype.UUID, agentName string, payload any) error {
	content := chatDoneContent(payload)
	finalized, err := p.finalizeStreamCard(ctx, creds, inst, binding, taskID, agentName, content)
	if err != nil {
		return err
	}
	if finalized {
		return nil
	}
	if content == "" {
		return nil
	}
	target := threadReplyTarget(binding)
	if chatReplyNeedsConfirmationAction(content) {
		if allowedOpenID, ok := p.confirmationAllowedOpenID(ctx, inst.WorkspaceID, binding.InstallationID, taskID); ok {
			return p.sendConfirmationCard(ctx, creds, binding, taskID, allowedOpenID, content, target)
		}
		p.cfg.Logger.Warn("lark: confirmation prompt fell back to native reply because requester binding was unavailable",
			"task_id", uuidString(taskID),
			"chat_type", binding.ChatType)
	}
	if containsMarkdown(content) {
		return sendWithThreadFallback(p.cfg.Logger, "send markdown card", target, func(t ReplyTarget) error {
			_, err := p.client.SendMarkdownCard(ctx, SendMarkdownCardParams{
				InstallationID: creds,
				ChatID:         outboundChatID(binding),
				Markdown:       content,
				ReplyTarget:    t,
			})
			return err
		})
	}
	return sendWithThreadFallback(p.cfg.Logger, "send text message", target, func(t ReplyTarget) error {
		_, err := p.client.SendTextMessage(ctx, SendTextParams{
			InstallationID: creds,
			ChatID:         outboundChatID(binding),
			Text:           content,
			ReplyTarget:    t,
		})
		return err
	})
}

func (p *Patcher) finalizeStreamCard(ctx context.Context, creds InstallationCredentials, inst Installation, binding ChatSessionBinding, taskID pgtype.UUID, agentName, content string) (bool, error) {
	card, err := p.queries.GetLarkOutboundCardByTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load streaming card for final: %w", err)
	}
	card, err = p.queries.SetLarkOutboundTerminalDesired(ctx, SetOutboundTerminalDesiredParams{
		TaskID: taskID, Status: string(CardStatusFinal), TerminalContent: content,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		current, loadErr := p.queries.GetLarkOutboundCardByTask(ctx, taskID)
		if loadErr == nil && (current.Status == string(CardStatusFinal) || current.Status == string(CardStatusError)) {
			return true, nil
		}
		return false, loadErr
	}
	if err != nil {
		return true, fmt.Errorf("queue final streaming card: %w", err)
	}
	// An unsent, unleased row settled locally: preserve the native fast reply.
	if card.ChannelCardMessageID == "" && card.ChannelCardID == "" && card.AppliedRevision == card.DesiredRevision {
		return false, nil
	}
	if err := p.flushTask(ctx, taskID); err != nil {
		return true, err
	}
	return true, nil
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

// fail settles an existing live card as an error. Fast failures that never
// crossed StreamingDelay retain the existing one-shot error-card behavior.
func (p *Patcher) fail(ctx context.Context, creds InstallationCredentials, binding ChatSessionBinding, taskID pgtype.UUID, agentName string, payload any) error {
	errorMessage := errorMessageFromPayload(payload)
	patched, err := p.queueTerminalStreamCard(ctx, taskID, CardStatusError, errorMessage)
	if err != nil {
		return err
	}
	if patched {
		return p.flushTask(ctx, taskID)
	}
	render, err := p.cfg.Renderer.Render(RenderInput{
		Kind:         CardKindError,
		AgentName:    agentName,
		TaskID:       taskID,
		ErrorMessage: errorMessage,
	})
	if err != nil {
		return fmt.Errorf("render error card: %w", err)
	}
	return sendWithThreadFallback(p.cfg.Logger, "send error card", threadReplyTarget(binding), func(t ReplyTarget) error {
		_, err := p.client.SendInteractiveCard(ctx, SendCardParams{
			InstallationID: creds,
			ChatID:         outboundChatID(binding),
			CardJSON:       render.JSON,
			ReplyTarget:    t,
		})
		return err
	})
}

func (p *Patcher) cancelStreamCard(ctx context.Context, creds InstallationCredentials, binding ChatSessionBinding, taskID pgtype.UUID, agentName string) error {
	queued, err := p.queueTerminalStreamCard(ctx, taskID, CardStatusFinal, "已取消")
	if err != nil || !queued {
		return err
	}
	return p.flushTask(ctx, taskID)
}

func (p *Patcher) queueTerminalStreamCard(ctx context.Context, taskID pgtype.UUID, status CardStatus, content string) (bool, error) {
	card, err := p.queries.GetLarkOutboundCardByTask(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load streaming card for terminal patch: %w", err)
	}
	card, err = p.queries.SetLarkOutboundTerminalDesired(ctx, SetOutboundTerminalDesiredParams{
		TaskID: taskID, Status: string(status), TerminalContent: content,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		current, loadErr := p.queries.GetLarkOutboundCardByTask(ctx, taskID)
		if loadErr == nil && (current.Status == string(CardStatusFinal) || current.Status == string(CardStatusError)) {
			return true, nil
		}
		return false, loadErr
	}
	if err != nil {
		return true, fmt.Errorf("queue terminal streaming card: %w", err)
	}
	return card.ChannelCardMessageID != "" || card.ChannelCardID != "" || card.AppliedRevision < card.DesiredRevision, nil
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
