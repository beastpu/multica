package lark

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Run paints live progress cards until ctx is done. The database row is the
// source of truth; this ticker only bounds how late a paint can be, and carries
// no correctness weight because the next tick on any replica picks up whatever
// this one missed.
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

// RunOnce claims every card whose paint is due and paints it. Claiming stamps
// last_patched_at in the same statement, so a slow paint pushes back that
// card's next turn instead of letting paints of the same message pile up.
func (p *Patcher) RunOnce(ctx context.Context) {
	cards, err := p.queries.ClaimLarkOutboundCardWork(ctx, ClaimOutboundCardWorkParams{
		StartDelaySeconds: p.cfg.StreamingDelay.Seconds(),
		HeartbeatSeconds:  p.cfg.HeartbeatInterval.Seconds(),
		MaxRows:           int32(p.cfg.MaxBatch),
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			p.cfg.Logger.Warn("lark outbound worker: claim failed", "error", err)
		}
		return
	}
	for _, card := range cards {
		if ctx.Err() != nil {
			return
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := p.paintCard(callCtx, card)
		cancel()
		if err != nil {
			p.cfg.Logger.Warn("lark outbound worker: paint failed",
				"task_id", uuidString(card.TaskID),
				"status", card.Status,
				"error", err)
		}
	}
}

// paintCard sends a card that has come due, or repaints one that is already in
// the chat. A failure here is not compensated: the row keeps its live status,
// so the next time it comes due the same work is simply retried. Repainting is
// idempotent — the same row rendered twice is the same bytes twice — which is
// what lets the claim get away without a delivery lease.
func (p *Patcher) paintCard(ctx context.Context, card OutboundCardMessage) error {
	target, ok, err := p.cardTarget(ctx, card.ChatSessionID)
	if err != nil {
		return err
	}
	if !ok {
		// Binding or installation gone since the task started. Nothing to paint
		// and nothing to fix; say so rather than returning a nil error that
		// reads like a successful paint.
		p.cfg.Logger.Info("lark outbound worker: skipping card with no live chat target",
			"task_id", uuidString(card.TaskID))
		return nil
	}
	creds, binding, agentName := target.creds, target.binding, target.agentName
	render, err := p.cfg.Renderer.Render(RenderInput{
		Kind:        CardKindRunning,
		AgentName:   agentName,
		TaskID:      card.TaskID,
		ElapsedSecs: p.elapsedSeconds(card.CreatedAt),
	})
	if err != nil {
		return fmt.Errorf("render progress card: %w", err)
	}
	if card.ChannelCardMessageID != "" {
		if err := p.client.PatchInteractiveCard(ctx, PatchCardParams{
			InstallationID:    creds,
			LarkCardMessageID: card.ChannelCardMessageID,
			CardJSON:          render.JSON,
		}); err != nil {
			return fmt.Errorf("repaint progress card: %w", err)
		}
		p.recordDelivery(card.Status, "repaint")
		return nil
	}
	return p.sendProgressCard(ctx, creds, binding, card, render.JSON)
}

func (p *Patcher) sendProgressCard(ctx context.Context, creds InstallationCredentials, binding ChatSessionBinding, card OutboundCardMessage, cardJSON string) error {
	var messageID string
	err := sendWithThreadFallback(p.cfg.Logger, "send progress card", threadReplyTarget(binding), func(target ReplyTarget) error {
		var sendErr error
		messageID, sendErr = p.client.SendInteractiveCard(ctx, SendCardParams{
			InstallationID: creds,
			ChatID:         outboundChatID(binding),
			CardJSON:       cardJSON,
			// Scoped to the task so a retry after an ambiguous send cannot post
			// a second progress card for the same run.
			IdempotencyKey: "progress-" + uuidString(card.TaskID),
			ReplyTarget:    target,
		})
		return sendErr
	})
	if err != nil {
		return err
	}
	_, err = p.queries.SetLarkOutboundCardMessageID(ctx, SetOutboundCardMessageIDParams{
		ID:                   card.ID,
		ChannelCardMessageID: messageID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The row left 'pending' while this send was in flight: the task
		// finished and its answer already went out on its own. The progress
		// card is orphaned in the chat — noisy, but nothing was lost, and
		// adopting it now would race the reply that is already delivered.
		p.cfg.Logger.Warn("lark outbound worker: progress card landed after the task settled",
			"task_id", uuidString(card.TaskID),
			"card_message_id", messageID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("persist progress card message id: %w", err)
	}
	if p.cfg.Metrics != nil {
		p.cfg.Metrics.RecordCardStarted(p.elapsedFloat(card.CreatedAt))
	}
	p.recordDelivery(string(CardStatusStreaming), "sent")
	return nil
}

// cardPaintTarget is everything a paint needs to know about the chat behind a
// card.
type cardPaintTarget struct {
	creds     InstallationCredentials
	binding   ChatSessionBinding
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
	return cardPaintTarget{creds: creds, binding: binding, agentName: agentName}, true, nil
}

func (p *Patcher) elapsedSeconds(since pgtype.Timestamptz) int64 {
	return int64(p.elapsedFloat(since))
}

func (p *Patcher) elapsedFloat(since pgtype.Timestamptz) float64 {
	if !since.Valid {
		return 0
	}
	elapsed := p.cfg.Now().Sub(since.Time).Seconds()
	if elapsed < 0 {
		return 0
	}
	return elapsed
}
