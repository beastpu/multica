package lark

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const outboundDeliveryBatchLimit = 50

// Run owns the durable outbound delivery loop. The database row is the source
// of truth; this ticker only reduces pickup latency and carries no correctness
// weight because another replica can reclaim every expired lease.
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

// RunOnce drains a bounded number of due rows. SKIP LOCKED in the claim query
// distributes work across replicas without reserving a batch ahead of time.
func (p *Patcher) RunOnce(ctx context.Context) {
	workerCount := min(p.cfg.WorkerConcurrency, outboundDeliveryBatchLimit)
	var attempts atomic.Int32
	var exhausted atomic.Bool
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer workers.Done()
			for !exhausted.Load() {
				if attempts.Add(1) > outboundDeliveryBatchLimit {
					return
				}
				if !p.runOneDelivery(ctx) {
					exhausted.Store(true)
					return
				}
			}
		}()
	}
	workers.Wait()
}

// runOneDelivery claims immediately before processing. It returns false when
// no due work remains (or the worker context has stopped), allowing the bounded
// worker group to finish without reserving a batch of leases in advance.
func (p *Patcher) runOneDelivery(ctx context.Context) bool {
	claim, err := p.claimDelivery(ctx, pgtype.UUID{})
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, context.Canceled) {
		return false
	}
	if err != nil {
		p.cfg.Logger.Warn("lark outbound worker: claim failed", "error", err)
		return false
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = p.deliverClaimed(callCtx, claim)
	cancel()
	if err != nil {
		p.cfg.Logger.Warn("lark outbound worker: delivery failed",
			"task_id", uuidString(claim.TaskID),
			"revision", claim.InflightRevision.Int64,
			"attempt", claim.AttemptCount+1,
			"error", err)
	}
	return true
}

func (p *Patcher) flushTask(ctx context.Context, taskID pgtype.UUID) error {
	claim, err := p.claimDelivery(ctx, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim outbound delivery: %w", err)
	}
	return p.deliverClaimed(ctx, claim)
}

func (p *Patcher) claimDelivery(ctx context.Context, taskID pgtype.UUID) (OutboundCardMessage, error) {
	return p.queries.ClaimLarkOutboundCardDelivery(ctx, ClaimOutboundCardDeliveryParams{
		TaskID:       taskID,
		LeaseToken:   pgtype.UUID{Bytes: uuid.New(), Valid: true},
		LeaseSeconds: p.cfg.DeliveryLease.Seconds(),
	})
}

func (p *Patcher) deliverClaimed(ctx context.Context, card OutboundCardMessage) error {
	if !card.LeaseToken.Valid || !card.InflightRevision.Valid {
		return errors.New("claimed outbound card is missing lease/revision")
	}
	var err error
	card, err = p.reconcileTaskProgress(ctx, card)
	if err != nil {
		return p.failDelivery(card, fmt.Errorf("reconcile persisted task messages: %w", err))
	}
	task, err := p.queries.GetAgentTask(ctx, card.TaskID)
	if err != nil {
		return p.failDelivery(card, classifyMissingDeliveryResource("load agent task", err))
	}
	binding, err := p.queries.GetLarkChatSessionBindingBySession(ctx, card.ChatSessionID)
	if err != nil {
		return p.failDelivery(card, classifyMissingDeliveryResource("load chat binding", err))
	}
	inst, err := p.queries.GetLarkInstallation(ctx, binding.InstallationID)
	if err != nil {
		return p.failDelivery(card, classifyMissingDeliveryResource("load installation", err))
	}
	if InstallationStatus(inst.Status) != InstallationActive {
		return p.failDelivery(card, permanentDelivery(errors.New("installation is not active")))
	}
	creds, err := p.installationCredentials(inst)
	if err != nil {
		return p.failDelivery(card, err)
	}

	agentName := ""
	if agent, agentErr := p.queries.GetAgent(ctx, inst.AgentID); agentErr == nil {
		agentName = agent.Name
	}
	if card.InflightCardJSON == "" {
		cardJSON, renderErr := p.renderDeliveryCard(ctx, card, task, inst, binding, agentName)
		if renderErr != nil {
			return p.failDelivery(card, permanentDelivery(renderErr))
		}
		updated, persistErr := p.queries.SetLarkOutboundInflightPayload(ctx, SetOutboundInflightPayloadParams{
			ID: card.ID, LeaseToken: card.LeaseToken,
			DesiredRevision: card.DesiredRevision, CardJSON: cardJSON,
		})
		if persistErr != nil {
			return p.failDelivery(card, fmt.Errorf("persist inflight card payload: %w", persistErr))
		}
		card = updated
	}

	// applied_revision remains zero until the first remote delivery is durably
	// acknowledged. This still records exactly once when sending succeeded but
	// persisting completion failed and the same operation had to be retried.
	firstDelivery := card.AppliedRevision == 0
	if card.Transport == "cardkit" {
		card, err = p.deliverCardKit(ctx, card, creds, binding)
		// Only an unsent card can change transports without creating a second
		// visible message. Existing CardKit cards keep their transport; a
		// definitive update permission failure is terminal and observable.
		if card.ChannelCardMessageID == "" && isCardKitUnavailable(err) {
			fallbackReason := cardKitUnavailableReason(err)
			downgraded, downgradeErr := p.queries.DowngradeLarkOutboundCardTransport(ctx, OutboundDeliveryLeaseParams{ID: card.ID, LeaseToken: card.LeaseToken})
			if downgradeErr == nil {
				card = downgraded
				if p.cfg.Metrics != nil {
					p.cfg.Metrics.RecordFallback(fallbackReason)
				}
				p.cfg.Logger.Warn("lark outbound worker: CardKit unavailable, using legacy card updates",
					"task_id", uuidString(card.TaskID))
				card, err = p.deliverLegacy(ctx, card, creds, binding)
			} else {
				err = fmt.Errorf("persist CardKit transport downgrade: %w", downgradeErr)
			}
		}
	} else {
		card, err = p.deliverLegacy(ctx, card, creds, binding)
	}
	if err != nil {
		return p.failDelivery(card, err)
	}
	if _, err := p.queries.CompleteLarkOutboundCardDelivery(ctx, OutboundDeliveryLeaseParams{
		ID: card.ID, LeaseToken: card.LeaseToken,
	}); err != nil {
		return p.failDelivery(card, fmt.Errorf("complete outbound delivery: %w", err))
	}
	if p.cfg.Metrics != nil {
		p.cfg.Metrics.RecordDelivery(card.Transport, card.Status, "success", p.deliveryLagSeconds(card))
		if firstDelivery {
			firstFeedback := 0.0
			if task.CreatedAt.Valid {
				firstFeedback = p.cfg.Now().Sub(task.CreatedAt.Time).Seconds()
				if firstFeedback < 0 {
					firstFeedback = 0
				}
			}
			p.cfg.Metrics.RecordCardStarted(card.Transport, firstFeedback)
		}
	}
	return nil
}

func (p *Patcher) reconcileTaskProgress(ctx context.Context, card OutboundCardMessage) (OutboundCardMessage, error) {
	if !card.TaskID.Valid || card.Status == string(CardStatusFinal) || card.Status == string(CardStatusError) {
		return card, nil
	}
	messages, err := p.queries.ListTaskMessagesSince(ctx, db.ListTaskMessagesSinceParams{
		TaskID: card.TaskID,
		Seq:    card.ProjectedSeq,
	})
	if err != nil {
		return card, err
	}
	for _, message := range messages {
		projection := projectTaskProgress(protocolTaskMessage(message))
		updated, projectErr := p.queries.ProjectLarkOutboundTaskMessage(ctx, ProjectOutboundTaskMessageParams{
			TaskID: card.TaskID, Seq: projection.Seq,
			VisibleTextAppend: projection.VisibleTextAppend, CurrentStage: projection.Stage,
			FilesReadDelta: projection.FilesReadDelta, FilesEditedDelta: projection.FilesEditedDelta,
			SearchesDelta: projection.SearchesDelta, CommandsDelta: projection.CommandsDelta,
			MinIntervalSeconds: p.cfg.MinPatchInterval.Seconds(),
		})
		if errors.Is(projectErr, pgx.ErrNoRows) {
			current, loadErr := p.queries.GetLarkOutboundCardByTask(ctx, card.TaskID)
			if loadErr != nil {
				return card, loadErr
			}
			card = current
			if card.Status == string(CardStatusFinal) || card.Status == string(CardStatusError) {
				break
			}
			continue
		}
		if projectErr != nil {
			return card, projectErr
		}
		card = updated
	}
	return card, nil
}

func protocolTaskMessage(message db.TaskMessage) protocol.TaskMessagePayload {
	return protocol.TaskMessagePayload{
		Seq: int(message.Seq), Type: message.Type,
		Tool: message.Tool.String, Content: message.Content.String,
	}
}

func (p *Patcher) renderDeliveryCard(ctx context.Context, card OutboundCardMessage, task db.AgentTaskQueue, inst Installation, binding ChatSessionBinding, agentName string) (string, error) {
	if card.Status == string(CardStatusFinal) && chatReplyNeedsConfirmationAction(card.TerminalContent) {
		if allowedOpenID, ok := p.confirmationAllowedOpenID(ctx, inst.WorkspaceID, binding.InstallationID, card.TaskID); ok {
			return renderConfirmationCardV2(card.TerminalContent, binding, uuidString(card.TaskID), allowedOpenID, p.cfg.Now())
		}
	}
	in := RenderInput{
		AgentName: agentName, TaskID: card.TaskID,
		Stage: card.CurrentStage, Content: card.VisibleText,
		FilesRead: card.FilesReadCount, FilesEdited: card.FilesEditedCount,
		Searches: card.SearchesCount, Commands: card.CommandsCount,
	}
	if task.CreatedAt.Valid {
		in.ElapsedSecs = int64(p.cfg.Now().Sub(task.CreatedAt.Time).Seconds())
		if in.ElapsedSecs < 0 {
			in.ElapsedSecs = 0
		}
	}
	switch card.Status {
	case string(CardStatusFinal):
		in.Kind = CardKindFinal
		in.Content = card.TerminalContent
	case string(CardStatusError):
		in.Kind = CardKindError
		in.ErrorMessage = card.TerminalContent
	default:
		in.Kind = CardKindRunning
	}
	rendered, err := p.cfg.Renderer.Render(in)
	if err != nil {
		return "", fmt.Errorf("render outbound delivery: %w", err)
	}
	return rendered.JSON, nil
}

func (p *Patcher) deliverCardKit(ctx context.Context, card OutboundCardMessage, creds InstallationCredentials, binding ChatSessionBinding) (OutboundCardMessage, error) {
	client, ok := p.client.(CardKitAPIClient)
	if !ok {
		return card, errCardKitUnavailable
	}
	fresh := false
	if card.ChannelCardID == "" {
		cardID, err := client.CreateCardKitCard(ctx, CreateCardKitCardParams{InstallationID: creds, CardJSON: card.InflightCardJSON})
		if err != nil {
			return card, err
		}
		updated, persistErr := p.queries.SetLarkOutboundCardEntityID(ctx, SetOutboundCardEntityIDParams{
			ID: card.ID, LeaseToken: card.LeaseToken, ChannelCardID: cardID,
		})
		if persistErr != nil {
			return card, fmt.Errorf("persist CardKit card id: %w", persistErr)
		}
		card = updated
		fresh = true
	}
	if card.ChannelCardMessageID == "" {
		var messageID string
		err := sendWithThreadFallback(p.cfg.Logger, "send CardKit streaming card", threadReplyTarget(binding), func(target ReplyTarget) error {
			var sendErr error
			messageID, sendErr = client.SendCardKitCard(ctx, SendCardKitCardParams{
				InstallationID: creds, ChatID: outboundChatID(binding), CardID: card.ChannelCardID,
				IdempotencyKey: "stream-" + uuidString(card.TaskID), ReplyTarget: target,
			})
			return sendErr
		})
		if err != nil {
			return card, err
		}
		updated, persistErr := p.queries.SetLarkOutboundCardMessageID(ctx, SetOutboundCardMessageIDParams{
			ID: card.ID, LeaseToken: card.LeaseToken, ChannelCardMessageID: messageID,
		})
		if persistErr != nil {
			return card, fmt.Errorf("persist CardKit message id: %w", persistErr)
		}
		card = updated
		fresh = true
	}
	if fresh {
		return card, nil
	}
	if !card.InflightSequence.Valid {
		// A successful initial send persists the message ID before the delivery is
		// marked complete. If only that final DB write failed, the retry must not
		// turn the already-delivered initial revision into CardKit update sequence 1.
		if card.AppliedRevision == 0 && card.ChannelCardMessageID != "" {
			return card, nil
		}
		return card, permanentDelivery(errors.New("CardKit update is missing operation sequence"))
	}
	err := client.UpdateCardKitCard(ctx, UpdateCardKitCardParams{
		InstallationID: creds, CardID: card.ChannelCardID, CardJSON: card.InflightCardJSON,
		IdempotencyKey: fmt.Sprintf("stream-%s-%d", uuidString(card.TaskID), card.InflightRevision.Int64),
		Sequence:       card.InflightSequence.Int32,
	})
	if err != nil {
		return card, fmt.Errorf("update CardKit card: %w", err)
	}
	return card, nil
}

func (p *Patcher) deliverLegacy(ctx context.Context, card OutboundCardMessage, creds InstallationCredentials, binding ChatSessionBinding) (OutboundCardMessage, error) {
	if card.ChannelCardMessageID == "" {
		var messageID string
		err := sendWithThreadFallback(p.cfg.Logger, "send legacy streaming card", threadReplyTarget(binding), func(target ReplyTarget) error {
			var sendErr error
			messageID, sendErr = p.client.SendInteractiveCard(ctx, SendCardParams{
				InstallationID: creds, ChatID: outboundChatID(binding), CardJSON: card.InflightCardJSON,
				IdempotencyKey: "stream-" + uuidString(card.TaskID), ReplyTarget: target,
			})
			return sendErr
		})
		if err != nil {
			return card, err
		}
		updated, err := p.queries.SetLarkOutboundCardMessageID(ctx, SetOutboundCardMessageIDParams{
			ID: card.ID, LeaseToken: card.LeaseToken, ChannelCardMessageID: messageID,
		})
		if err != nil {
			return card, fmt.Errorf("persist legacy card message id: %w", err)
		}
		return updated, nil
	}
	if err := p.client.PatchInteractiveCard(ctx, PatchCardParams{
		InstallationID: creds, LarkCardMessageID: card.ChannelCardMessageID, CardJSON: card.InflightCardJSON,
	}); err != nil {
		return card, fmt.Errorf("patch legacy streaming card: %w", err)
	}
	return card, nil
}

func (p *Patcher) failDelivery(card OutboundCardMessage, deliveryErr error) error {
	if isPermanentDeliveryError(deliveryErr) {
		return p.abandonDelivery(card, deliveryErr)
	}
	retry := time.Second << min(int(card.AttemptCount), 6)
	if retry > time.Minute {
		retry = time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, persistErr := p.queries.FailLarkOutboundCardDelivery(ctx, FailOutboundCardDeliveryParams{
		ID: card.ID, LeaseToken: card.LeaseToken, LastError: truncateUTF8Bytes(deliveryErr.Error(), 512), RetrySeconds: retry.Seconds(),
	})
	if persistErr != nil {
		return fmt.Errorf("%v; persist retry: %w", deliveryErr, persistErr)
	}
	if p.cfg.Metrics != nil {
		p.cfg.Metrics.RecordDelivery(card.Transport, card.Status, "retry", p.deliveryLagSeconds(card))
	}
	return deliveryErr
}

type permanentOutboundError struct{ err error }

func (e *permanentOutboundError) Error() string { return e.err.Error() }
func (e *permanentOutboundError) Unwrap() error { return e.err }

func permanentDelivery(err error) error {
	if err == nil || isPermanentDeliveryError(err) {
		return err
	}
	return &permanentOutboundError{err: err}
}

func classifyMissingDeliveryResource(op string, err error) error {
	wrapped := fmt.Errorf("%s: %w", op, err)
	if errors.Is(err, pgx.ErrNoRows) {
		return permanentDelivery(wrapped)
	}
	return wrapped
}

func isPermanentDeliveryError(err error) bool {
	var permanentErr *permanentOutboundError
	if errors.As(err, &permanentErr) {
		return true
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode >= 400 && statusErr.StatusCode < 500 {
		switch statusErr.StatusCode {
		case 408, 409, 425, 429:
			return false
		default:
			return true
		}
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case 10002, 200220, 200740, 200750, 200770, 200860,
		300301, 300302, 300303, 300305, 300307, 300311, 300317,
		99991672, 99991679:
		return true
	default:
		return false
	}
}

func (p *Patcher) abandonDelivery(card OutboundCardMessage, deliveryErr error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, persistErr := p.queries.AbandonLarkOutboundCardDelivery(ctx, AbandonOutboundCardDeliveryParams{
		ID: card.ID, LeaseToken: card.LeaseToken, LastError: truncateUTF8Bytes(deliveryErr.Error(), 512),
	})
	if persistErr != nil {
		return fmt.Errorf("%v; persist permanent failure: %w", deliveryErr, persistErr)
	}
	if p.cfg.Metrics != nil {
		p.cfg.Metrics.RecordDelivery(card.Transport, card.Status, "permanent", p.deliveryLagSeconds(card))
	}
	return deliveryErr
}

var errCardKitUnavailable = errors.New("CardKit transport unavailable")

func isCardKitUnavailable(err error) bool {
	if errors.Is(err, errCardKitUnavailable) {
		return true
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == 403 && strings.HasPrefix(statusErr.Path, "/open-apis/cardkit/") {
		return true
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	// Open Platform permission failures are definitive. Content, rate-limit,
	// timeout, and server errors remain CardKit retries rather than silently
	// changing transport.
	if apiErr.Op != "create CardKit card" {
		return false
	}
	switch apiErr.Code {
	case 99991672, 99991679:
		return true
	default:
		return false
	}
}

func cardKitUnavailableReason(err error) string {
	if errors.Is(err, errCardKitUnavailable) {
		return "client_unsupported"
	}
	return "permission_denied"
}

func (p *Patcher) deliveryLagSeconds(card OutboundCardMessage) float64 {
	if !card.NextAttemptAt.Valid {
		return 0
	}
	lag := p.cfg.Now().Sub(card.NextAttemptAt.Time).Seconds()
	if lag < 0 {
		return 0
	}
	return lag
}
