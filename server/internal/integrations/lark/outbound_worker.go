package lark

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// streamElementID is the card element the agent's answer streams into. The
// Renderer must emit an element with this id, because CardKit addresses
// streaming text by element rather than by position.
const streamElementID = "agent_reply"

// sequenceReserve is how many CardKit operation numbers one paint may spend.
// A terminal paint is the greediest: close streaming, then draw the answer.
const sequenceReserve = 2

// Run paints live cards until ctx is done. The database row is the source of
// truth; this ticker only bounds how late a paint can be.
func (p *Patcher) Run(ctx context.Context) {
	p.RunOnce(ctx)
	ticker := time.NewTicker(p.cfg.WorkerPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.RunOnce(ctx)
		}
	}
}

// RunOnce drains up to MaxBatch due cards. Each claim leases exactly one row
// and reserves its operation numbers, so two replicas polling at the same
// moment take different cards rather than colliding on one.
func (p *Patcher) RunOnce(ctx context.Context) {
	for i := 0; i < p.cfg.MaxBatch; i++ {
		if ctx.Err() != nil {
			return
		}
		card, err := p.queries.ClaimLarkOutboundCardPaint(ctx, ClaimOutboundCardPaintParams{
			LeaseToken:      pgtype.UUID{Bytes: uuid.New(), Valid: true},
			LeaseSeconds:    p.cfg.LeaseDuration.Seconds(),
			ThrottleSeconds: p.cfg.StreamThrottle.Seconds(),
			SequenceReserve: sequenceReserve,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				p.cfg.Logger.Warn("lark outbound worker: claim failed", "error", err)
			}
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		paintErr := p.paintCard(callCtx, card)
		cancel()
		if paintErr != nil {
			p.failPaint(card, paintErr)
		}
	}
}

// paintCard advances one card by one step. It is called under a lease that
// already reserved this paint's operation numbers, so every CardKit call below
// can spend them without coordinating further.
func (p *Patcher) paintCard(ctx context.Context, card OutboundCardMessage) error {
	target, ok, err := p.cardTarget(ctx, card.ChatSessionID)
	if err != nil {
		return err
	}
	if !ok {
		p.cfg.Logger.Info("lark outbound worker: skipping card with no live chat target",
			"task_id", uuidString(card.TaskID))
		return p.completePaint(card, card.VisibleText, false)
	}

	// The claim advanced operation_sequence past the block it reserved, so the
	// usable numbers are the last `sequenceReserve` of them.
	seq := card.OperationSequence - sequenceReserve
	next := func() int32 { seq++; return seq }

	if card.Transport != "cardkit" {
		return p.paintLegacy(ctx, card, target)
	}
	// Gate on delivery, not on the entity: a card that was created but whose
	// message never landed carries nothing, and streaming into it would look
	// healthy while the user sees an empty chat.
	if card.ChannelCardMessageID == "" {
		created, shown, err := p.createCardEntity(ctx, card, target)
		if err != nil {
			return err
		}
		if created.Transport != "cardkit" {
			// Downgraded mid-paint: the app lacks cardkit:card:write.
			return p.paintLegacy(ctx, created, target)
		}
		// The card was born carrying the text, so this paint is done. A
		// terminal answer that arrived meanwhile is drawn by the next one.
		return p.completePaint(created, shown, false)
	}
	if card.Status == string(CardStatusFinal) || card.Status == string(CardStatusError) {
		return p.paintTerminal(ctx, card, target, next)
	}
	return p.paintStreamingText(ctx, card, target, next)
}

// createCardEntity mints the CardKit card and posts the message that carries
// it. An app without cardkit:card:write cannot get past the first call, so that
// failure downgrades the row to the legacy transport instead of retrying
// forever against a permission that will not appear.
func (p *Patcher) createCardEntity(ctx context.Context, card OutboundCardMessage, target cardPaintTarget) (OutboundCardMessage, string, error) {
	client, ok := p.client.(CardKitAPIClient)
	if !ok {
		downgraded, err := p.downgrade(ctx, card, "client_unsupported")
		return downgraded, "", err
	}
	text, err := p.queries.ListLarkTaskVisibleText(ctx, card.TaskID)
	if err != nil {
		return card, "", fmt.Errorf("load visible text: %w", err)
	}
	text = truncateUTF8Bytes(text, maxCardBytes/2)
	// An entity from a previous attempt whose send failed is reused rather
	// than replaced, so a retry cannot leave a second orphan behind.
	cardID := card.ChannelCardID
	if cardID == "" {
		render, err := p.cfg.Renderer.Render(RenderInput{
			Kind: CardKindStreaming, AgentName: target.agentName, TaskID: card.TaskID, Content: text,
		})
		if err != nil {
			return card, "", fmt.Errorf("render streaming card: %w", err)
		}
		cardID, err = client.CreateCardKitCard(ctx, CreateCardKitCardParams{
			InstallationID: target.creds, CardJSON: render.JSON,
		})
		if err != nil {
			if isCardKitUnavailable(err) {
				p.cfg.Logger.Warn("lark outbound worker: CardKit unavailable, using legacy card updates",
					"task_id", uuidString(card.TaskID), "error", err)
				downgraded, dErr := p.downgrade(ctx, card, "permission_denied")
				return downgraded, "", dErr
			}
			return card, "", fmt.Errorf("create CardKit card: %w", err)
		}
	}
	var messageID string
	sendErr := sendWithThreadFallback(p.cfg.Logger, "send CardKit card", threadReplyTarget(target.binding), func(t ReplyTarget) error {
		var err error
		messageID, err = client.SendCardKitCard(ctx, SendCardKitCardParams{
			InstallationID: target.creds, ChatID: outboundChatID(target.binding), CardID: cardID,
			IdempotencyKey: "stream-" + uuidString(card.TaskID), ReplyTarget: t,
		})
		return err
	})
	if sendErr != nil {
		// The entity exists but nothing carries it. Record the id anyway so the
		// retry reuses it rather than minting a second orphan.
		if _, recErr := p.queries.RecordLarkOutboundCardEntity(ctx, RecordOutboundCardEntityParams{
			ID: card.ID, LeaseToken: card.LeaseToken, ChannelCardID: cardID,
		}); recErr != nil {
			p.cfg.Logger.Warn("lark outbound worker: could not record orphan card entity",
				"task_id", uuidString(card.TaskID), "error", recErr)
		}
		return card, "", sendErr
	}
	updated, err := p.queries.RecordLarkOutboundCardEntity(ctx, RecordOutboundCardEntityParams{
		ID: card.ID, LeaseToken: card.LeaseToken,
		ChannelCardID: cardID, ChannelCardMessageID: messageID,
	})
	if err != nil {
		return card, "", fmt.Errorf("record CardKit entity: %w", err)
	}
	p.recordDelivery(string(CardStatusStreaming), "sent")
	return updated, text, nil
}

func (p *Patcher) downgrade(ctx context.Context, card OutboundCardMessage, reason string) (OutboundCardMessage, error) {
	updated, err := p.queries.DowngradeLarkOutboundCardTransport(ctx, OutboundCardLeaseParams{
		ID: card.ID, LeaseToken: card.LeaseToken,
	})
	if err != nil {
		return card, fmt.Errorf("persist transport downgrade (%s): %w", reason, err)
	}
	return updated, nil
}

// paintStreamingText pushes the answer-so-far into the card's text element.
// CardKit takes the whole text, not a delta, and animates the tail when what it
// already holds is a prefix of what arrives.
func (p *Patcher) paintStreamingText(ctx context.Context, card OutboundCardMessage, target cardPaintTarget, next func() int32) error {
	text, err := p.queries.ListLarkTaskVisibleText(ctx, card.TaskID)
	if err != nil {
		return fmt.Errorf("load visible text: %w", err)
	}
	text = truncateUTF8Bytes(text, maxCardBytes/2)
	if text == card.VisibleText || strings.TrimSpace(text) == "" {
		return p.completePaint(card, card.VisibleText, false)
	}
	client, ok := p.client.(CardKitAPIClient)
	if !ok {
		return errors.New("CardKit transport unavailable")
	}
	if err := client.StreamCardKitText(ctx, StreamCardKitTextParams{
		InstallationID: target.creds, CardID: card.ChannelCardID, ElementID: streamElementID,
		Content: text, Sequence: next(),
		IdempotencyKey: fmt.Sprintf("stream-%s-%d", uuidString(card.TaskID), card.OperationSequence),
	}); err != nil {
		return fmt.Errorf("stream CardKit text: %w", err)
	}
	p.recordDelivery(string(CardStatusStreaming), "streamed")
	return p.completePaint(card, text, false)
}

// paintTerminal closes streaming and draws the answer.
//
// The close is not optional: while streaming_mode is on, Feishu will not apply
// a callback-driven update, so a confirmation card's buttons would look alive
// and do nothing. Closing first costs one extra round trip per reply and buys
// the guarantee that whatever the final card offers actually works.
func (p *Patcher) paintTerminal(ctx context.Context, card OutboundCardMessage, target cardPaintTarget, next func() int32) error {
	client, ok := p.client.(CardKitAPIClient)
	if !ok {
		return errors.New("CardKit transport unavailable")
	}
	if !card.StreamingClosedAt.Valid {
		if err := client.CloseCardKitStreaming(ctx, CloseCardKitStreamingParams{
			InstallationID: target.creds, CardID: card.ChannelCardID, Sequence: next(),
			IdempotencyKey: fmt.Sprintf("close-%s", uuidString(card.TaskID)),
		}); err != nil {
			// Not fatal, and deliberately so. Feishu auto-closes streaming
			// after ten minutes, so a long run reaches here with nothing left
			// to close and this call can fail for a reason that no longer
			// matters. Blocking the draw would trade "buttons might not
			// respond" for "no answer at all", which is the worse of the two.
			p.cfg.Logger.Warn("lark outbound worker: could not close streaming before the final card",
				"task_id", uuidString(card.TaskID), "error", err)
		}
	}
	in := RenderInput{
		Kind: CardKindFinal, AgentName: target.agentName, TaskID: card.TaskID,
		Content: card.TerminalContent,
	}
	if card.Status == string(CardStatusError) {
		in.Kind = CardKindError
		in.ErrorMessage = card.TerminalContent
	}
	cardJSON, err := p.renderTerminalCard(ctx, target.inst, target.binding, card.TaskID, in)
	if err != nil {
		return err
	}
	if err := client.UpdateCardKitCard(ctx, UpdateCardKitCardParams{
		InstallationID: target.creds, CardID: card.ChannelCardID, CardJSON: cardJSON,
		Sequence:       next(),
		IdempotencyKey: fmt.Sprintf("final-%s", uuidString(card.TaskID)),
	}); err != nil {
		return fmt.Errorf("draw terminal CardKit card: %w", err)
	}
	p.recordDelivery(card.Status, "drawn")
	return p.completePaint(card, card.TerminalContent, true)
}

// paintLegacy is the fallback for apps without cardkit:card:write: an ordinary
// interactive card, sent once and patched in place. No typewriter, but the
// answer still lands and still replaces the card it was streaming into.
func (p *Patcher) paintLegacy(ctx context.Context, card OutboundCardMessage, target cardPaintTarget) error {
	terminal := card.Status == string(CardStatusFinal) || card.Status == string(CardStatusError)
	in := RenderInput{Kind: CardKindStreaming, AgentName: target.agentName, TaskID: card.TaskID}
	if terminal {
		in.Kind = CardKindFinal
		in.Content = card.TerminalContent
		if card.Status == string(CardStatusError) {
			in.Kind = CardKindError
			in.ErrorMessage = card.TerminalContent
		}
	} else {
		text, err := p.queries.ListLarkTaskVisibleText(ctx, card.TaskID)
		if err != nil {
			return fmt.Errorf("load visible text: %w", err)
		}
		in.Content = truncateUTF8Bytes(text, maxCardBytes/2)
		if in.Content == card.VisibleText {
			return p.completePaint(card, card.VisibleText, false)
		}
	}
	cardJSON, err := p.renderTerminalCard(ctx, target.inst, target.binding, card.TaskID, in)
	if err != nil {
		return err
	}
	if card.ChannelCardMessageID == "" {
		var messageID string
		if err := sendWithThreadFallback(p.cfg.Logger, "send legacy card", threadReplyTarget(target.binding), func(t ReplyTarget) error {
			var sendErr error
			messageID, sendErr = p.client.SendInteractiveCard(ctx, SendCardParams{
				InstallationID: target.creds, ChatID: outboundChatID(target.binding), CardJSON: cardJSON,
				IdempotencyKey: "stream-" + uuidString(card.TaskID), ReplyTarget: t,
			})
			return sendErr
		}); err != nil {
			return err
		}
		if _, err := p.queries.RecordLarkOutboundCardEntity(ctx, RecordOutboundCardEntityParams{
			ID: card.ID, LeaseToken: card.LeaseToken, ChannelCardMessageID: messageID,
		}); err != nil {
			return fmt.Errorf("record legacy card message id: %w", err)
		}
	} else if err := p.client.PatchInteractiveCard(ctx, PatchCardParams{
		InstallationID: target.creds, LarkCardMessageID: card.ChannelCardMessageID, CardJSON: cardJSON,
	}); err != nil {
		return fmt.Errorf("patch legacy card: %w", err)
	}
	p.recordDelivery(card.Status, "legacy")
	return p.completePaint(card, in.Content, terminal)
}

func (p *Patcher) completePaint(card OutboundCardMessage, visibleText string, streamingClosed bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.queries.CompleteLarkOutboundCardPaint(ctx, CompleteOutboundCardPaintParams{
		ID: card.ID, LeaseToken: card.LeaseToken,
		VisibleText: visibleText, StreamingClosed: streamingClosed,
	}); err != nil {
		return fmt.Errorf("complete card paint: %w", err)
	}
	return nil
}

// failPaint releases the lease so another replica can retry. The reserved
// operation numbers are not returned: a repeated sequence is rejected by
// Feishu, while a gap costs nothing.
func (p *Patcher) failPaint(card OutboundCardMessage, paintErr error) {
	p.cfg.Logger.Warn("lark outbound worker: paint failed",
		"task_id", uuidString(card.TaskID), "status", card.Status,
		"attempt", card.AttemptCount+1, "error", paintErr)
	p.recordDelivery(card.Status, "failed")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.queries.FailLarkOutboundCardPaint(ctx, FailOutboundCardPaintParams{
		ID: card.ID, LeaseToken: card.LeaseToken,
		LastError: truncateUTF8Bytes(paintErr.Error(), 512),
	}); err != nil {
		p.cfg.Logger.Warn("lark outbound worker: could not release paint lease",
			"task_id", uuidString(card.TaskID), "error", err)
	}
}

// cardPaintTarget is everything a paint needs to know about the chat behind a
// card.
type cardPaintTarget struct {
	creds     InstallationCredentials
	binding   ChatSessionBinding
	inst      Installation
	agentName string
}

// cardTarget resolves the paint target. A binding or installation that vanished
// since the task started, or an installation that was revoked, is not an error
// worth retrying — it reports ok=false so the caller stops quietly.
func (p *Patcher) cardTarget(ctx context.Context, chatSessionID pgtype.UUID) (cardPaintTarget, bool, error) {
	binding, err := p.queries.GetLarkChatSessionBindingBySession(ctx, chatSessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return cardPaintTarget{}, false, nil
	}
	if err != nil {
		return cardPaintTarget{}, false, fmt.Errorf("load chat binding: %w", err)
	}
	inst, err := p.queries.GetLarkInstallation(ctx, binding.InstallationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return cardPaintTarget{}, false, nil
	}
	if err != nil {
		return cardPaintTarget{}, false, fmt.Errorf("load installation: %w", err)
	}
	if InstallationStatus(inst.Status) != InstallationActive {
		return cardPaintTarget{}, false, nil
	}
	creds, err := p.installationCredentials(inst)
	if err != nil {
		return cardPaintTarget{}, false, err
	}
	agentName := ""
	if agent, agentErr := p.queries.GetAgent(ctx, inst.AgentID); agentErr == nil {
		agentName = agent.Name
	}
	return cardPaintTarget{creds: creds, binding: binding, inst: inst, agentName: agentName}, true, nil
}

// isCardKitUnavailable reports a definitive "this app may not use CardKit".
// Content, rate-limit, timeout and server errors stay retries rather than
// silently changing transport.
func isCardKitUnavailable(err error) bool {
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == 403 && strings.HasPrefix(statusErr.Path, "/open-apis/cardkit/") {
		return true
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case 99991672, 99991679:
		return true
	default:
		return false
	}
}
