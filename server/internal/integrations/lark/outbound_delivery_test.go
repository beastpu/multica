package lark

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeCardKitClient struct {
	*fakeAPIClient
	mu           sync.Mutex
	createCalls  []CreateCardKitCardParams
	sendCalls    []SendCardKitCardParams
	updateCalls  []UpdateCardKitCardParams
	createReturn string
	createErr    error
	sendReturn   string
	sendErr      error
	updateErr    error
}

type concurrentDeliveryQueries struct {
	*fakePatcherQueries
	mu     sync.Mutex
	claims []OutboundCardMessage
}

func (q *concurrentDeliveryQueries) ClaimLarkOutboundCardDelivery(_ context.Context, arg ClaimOutboundCardDeliveryParams) (OutboundCardMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.claims) == 0 {
		return OutboundCardMessage{}, pgx.ErrNoRows
	}
	card := q.claims[0]
	q.claims = q.claims[1:]
	card.LeaseToken = arg.LeaseToken
	return card, nil
}

func (q *concurrentDeliveryQueries) CompleteLarkOutboundCardDelivery(_ context.Context, _ OutboundDeliveryLeaseParams) (OutboundCardMessage, error) {
	return OutboundCardMessage{}, nil
}

type blockingPatchClient struct {
	*fakeAPIClient
	slowStarted chan struct{}
	releaseSlow chan struct{}
	fastDone    chan struct{}
	slowOnce    sync.Once
	fastOnce    sync.Once
}

func (c *blockingPatchClient) PatchInteractiveCard(ctx context.Context, p PatchCardParams) error {
	if p.CardJSON == "slow" {
		c.slowOnce.Do(func() { close(c.slowStarted) })
		select {
		case <-c.releaseSlow:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.fastOnce.Do(func() { close(c.fastDone) })
	return nil
}

func (f *fakeCardKitClient) CreateCardKitCard(_ context.Context, p CreateCardKitCardParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = append(f.createCalls, p)
	return f.createReturn, f.createErr
}

func (f *fakeCardKitClient) SendCardKitCard(_ context.Context, p SendCardKitCardParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCalls = append(f.sendCalls, p)
	return f.sendReturn, f.sendErr
}

func (f *fakeCardKitClient) UpdateCardKitCard(_ context.Context, p UpdateCardKitCardParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls = append(f.updateCalls, p)
	return f.updateErr
}

func TestPatcherCardKitRetryReusesSequenceUUIDAndPayload(t *testing.T) {
	p, q, legacy := newTestPatcher(t)
	client := &fakeCardKitClient{fakeAPIClient: legacy}
	p.client = client
	taskID := uuidFromString(t, "ee200001-ee20-ee20-ee20-eeeeeeeeeeee")
	lease := uuidFromString(t, "dd200001-dd20-dd20-dd20-dddddddddddd")
	q.task = db.AgentTaskQueue{
		ID: taskID, ChatSessionID: q.binding.ChatSessionID,
		CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-20 * time.Second), Valid: true},
	}
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:     uuidFromString(t, "cc200001-cc20-cc20-cc20-cccccccccccc"),
		TaskID: taskID, ChatSessionID: q.binding.ChatSessionID,
		ChannelCardID: "7371713483664506900", ChannelCardMessageID: "om_cardkit",
		Transport: "cardkit", Status: string(CardStatusStreaming),
		DesiredRevision: 2, AppliedRevision: 1,
		InflightRevision: pgtype.Int8{Int64: 2, Valid: true},
		InflightSequence: pgtype.Int4{Int32: 2, Valid: true},
		InflightCardJSON: `{"schema":"2.0","body":{"elements":[]}}`,
		LeaseToken:       lease,
	}
	client.updateErr = errors.New("result uncertain")
	if err := p.deliverClaimed(context.Background(), q.card); err == nil {
		t.Fatal("first update should fail")
	}
	client.updateErr = nil
	q.card.NextAttemptAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true}
	p.RunOnce(context.Background())

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.updateCalls) != 2 {
		t.Fatalf("updates=%d want 2", len(client.updateCalls))
	}
	first, second := client.updateCalls[0], client.updateCalls[1]
	if first.Sequence != second.Sequence || first.IdempotencyKey != second.IdempotencyKey || first.CardJSON != second.CardJSON {
		t.Fatalf("retry changed operation: first=%+v second=%+v", first, second)
	}
	if q.card.AppliedRevision != 2 || q.card.Status != string(CardStatusStreaming) {
		t.Fatalf("completed card=%+v", q.card)
	}
}

func TestPatcherRunOnceDoesNotHeadOfLineBlockOtherDeliveries(t *testing.T) {
	p, base, api := newTestPatcher(t)
	p.cfg.WorkerConcurrency = 2
	queries := &concurrentDeliveryQueries{fakePatcherQueries: base}
	for i, payload := range []string{"slow", "fast"} {
		queries.claims = append(queries.claims, OutboundCardMessage{
			ID:            uuidFromString(t, fmt.Sprintf("cc20001%d-cc20-cc20-cc20-cccccccccccc", i)),
			TaskID:        uuidFromString(t, fmt.Sprintf("ee20001%d-ee20-ee20-ee20-eeeeeeeeeeee", i)),
			ChatSessionID: base.binding.ChatSessionID,
			Transport:     "legacy", Status: string(CardStatusStreaming),
			DesiredRevision: 1, InflightRevision: pgtype.Int8{Int64: 1, Valid: true},
			InflightCardJSON: payload, ChannelCardMessageID: "om_" + payload,
		})
	}
	client := &blockingPatchClient{
		fakeAPIClient: api,
		slowStarted:   make(chan struct{}),
		releaseSlow:   make(chan struct{}),
		fastDone:      make(chan struct{}),
	}
	p.queries = queries
	p.client = client

	done := make(chan struct{})
	go func() {
		p.RunOnce(context.Background())
		close(done)
	}()

	select {
	case <-client.slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow delivery did not start")
	}
	select {
	case <-client.fastDone:
	case <-time.After(time.Second):
		close(client.releaseSlow)
		t.Fatal("fast delivery was blocked behind slow delivery")
	}
	close(client.releaseSlow)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish after slow delivery was released")
	}
}

func TestPatcherCardKitFirstRealUpdateStartsAtSequenceOne(t *testing.T) {
	p, q, legacy := newTestPatcher(t)
	client := &fakeCardKitClient{
		fakeAPIClient: legacy,
		createReturn:  "7371713483664506900",
		sendReturn:    "om_cardkit_first",
	}
	p.client = client
	taskID := uuidFromString(t, "ee200004-ee20-ee20-ee20-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ID: taskID, ChatSessionID: q.binding.ChatSessionID}
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:     uuidFromString(t, "cc200004-cc20-cc20-cc20-cccccccccccc"),
		TaskID: taskID, ChatSessionID: q.binding.ChatSessionID,
		Transport: "cardkit", Status: string(CardStatusPending), DesiredRevision: 1,
		InflightRevision: pgtype.Int8{Int64: 1, Valid: true},
		LeaseToken:       uuidFromString(t, "dd200004-dd20-dd20-dd20-dddddddddddd"),
	}

	if err := p.deliverClaimed(context.Background(), q.card); err != nil {
		t.Fatalf("initial create/send: %v", err)
	}
	if q.card.OperationSequence != 0 {
		t.Fatalf("initial create/send consumed CardKit sequence: %d", q.card.OperationSequence)
	}

	q.card.DesiredRevision = 2
	q.card.NextAttemptAt = pgtype.Timestamptz{Time: time.Now().Add(-time.Second), Valid: true}
	claim, err := p.claimDelivery(context.Background(), taskID)
	if err != nil {
		t.Fatalf("claim first update: %v", err)
	}
	if err := p.deliverClaimed(context.Background(), claim); err != nil {
		t.Fatalf("first update: %v", err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.updateCalls) != 1 || client.updateCalls[0].Sequence != 1 {
		t.Fatalf("first CardKit update=%+v, want sequence 1", client.updateCalls)
	}
}

func TestPatcherCardKitInitialDeliveryCompletionRetrySkipsRemoteUpdate(t *testing.T) {
	p, q, legacy := newTestPatcher(t)
	client := &fakeCardKitClient{fakeAPIClient: legacy}
	p.client = client
	taskID := uuidFromString(t, "ee200005-ee20-ee20-ee20-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ID: taskID, ChatSessionID: q.binding.ChatSessionID}
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:                   uuidFromString(t, "cc200005-cc20-cc20-cc20-cccccccccccc"),
		TaskID:               taskID,
		ChatSessionID:        q.binding.ChatSessionID,
		ChannelCardID:        "7371713483664506900",
		ChannelCardMessageID: "om_cardkit_initial_retry",
		Transport:            "cardkit",
		Status:               string(CardStatusPending),
		DesiredRevision:      1,
		InflightRevision:     pgtype.Int8{Int64: 1, Valid: true},
		InflightCardJSON:     `{"schema":"2.0","body":{"elements":[]}}`,
		LeaseToken:           uuidFromString(t, "dd200005-dd20-dd20-dd20-dddddddddddd"),
	}

	if err := p.deliverClaimed(context.Background(), q.card); err != nil {
		t.Fatalf("retry completion after successful initial send: %v", err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.updateCalls) != 0 {
		t.Fatalf("completion retry unexpectedly updated CardKit: %+v", client.updateCalls)
	}
	if q.card.OperationSequence != 0 || q.card.AppliedRevision != 1 {
		t.Fatalf("completion retry card=%+v, want revision applied without consuming sequence", q.card)
	}
}

func TestPatcherFallsBackToLegacyOnlyForCardKitPermissionFailure(t *testing.T) {
	p, q, legacy := newTestPatcher(t)
	client := &fakeCardKitClient{
		fakeAPIClient: legacy,
		createErr:     &APIError{Op: "create CardKit card", Code: 99991672, Msg: "forbidden"},
	}
	p.client = client
	taskID := uuidFromString(t, "ee200002-ee20-ee20-ee20-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ID: taskID, ChatSessionID: q.binding.ChatSessionID}
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:     uuidFromString(t, "cc200002-cc20-cc20-cc20-cccccccccccc"),
		TaskID: taskID, ChatSessionID: q.binding.ChatSessionID,
		Transport: "cardkit", Status: string(CardStatusStreaming),
		DesiredRevision:  1,
		InflightRevision: pgtype.Int8{Int64: 1, Valid: true},
		InflightSequence: pgtype.Int4{Int32: 1, Valid: true},
		InflightCardJSON: `{"schema":"2.0","body":{"elements":[]}}`,
		LeaseToken:       uuidFromString(t, "dd200002-dd20-dd20-dd20-dddddddddddd"),
	}
	if err := p.deliverClaimed(context.Background(), q.card); err != nil {
		t.Fatalf("permission fallback: %v", err)
	}
	legacy.mu.Lock()
	defer legacy.mu.Unlock()
	if len(legacy.sent) != 1 || q.card.Transport != "legacy" {
		t.Fatalf("legacy sends=%d card=%+v", len(legacy.sent), q.card)
	}
	if isCardKitUnavailable(&APIError{Op: "create CardKit card", Code: 99991663, Msg: "token expired"}) {
		t.Fatal("token expiry must retry CardKit, not downgrade transport")
	}
	if isCardKitUnavailable(&HTTPStatusError{StatusCode: 403, Path: "/open-apis/auth/v3/tenant_access_token/internal", Body: "forbidden"}) {
		t.Fatal("token endpoint 403 must not downgrade CardKit")
	}
	if isCardKitUnavailable(&APIError{Op: "send CardKit card", Code: 99991672, Msg: "forbidden"}) {
		t.Fatal("IM send permission failure must not downgrade CardKit")
	}
	if !isCardKitUnavailable(&HTTPStatusError{StatusCode: 403, Path: "/open-apis/cardkit/v1/cards", Body: "forbidden"}) {
		t.Fatal("CardKit endpoint 403 should downgrade to the legacy transport")
	}
}

func TestPatcherDoesNotDowngradeExistingCardKitCard(t *testing.T) {
	p, q, legacy := newTestPatcher(t)
	client := &fakeCardKitClient{
		fakeAPIClient: legacy,
		updateErr:     &HTTPStatusError{StatusCode: 403, Path: "/open-apis/cardkit/v1/cards/card_1", Body: "forbidden"},
	}
	p.client = client
	taskID := uuidFromString(t, "ee200006-ee20-ee20-ee20-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ID: taskID, ChatSessionID: q.binding.ChatSessionID}
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:                   uuidFromString(t, "cc200006-cc20-cc20-cc20-cccccccccccc"),
		TaskID:               taskID,
		ChatSessionID:        q.binding.ChatSessionID,
		ChannelCardID:        "card_1",
		ChannelCardMessageID: "om_existing_cardkit",
		Transport:            "cardkit",
		Status:               string(CardStatusStreaming),
		DesiredRevision:      2,
		AppliedRevision:      1,
		InflightRevision:     pgtype.Int8{Int64: 2, Valid: true},
		InflightSequence:     pgtype.Int4{Int32: 1, Valid: true},
		InflightCardJSON:     `{"schema":"2.0","body":{"elements":[]}}`,
		LeaseToken:           uuidFromString(t, "dd200006-dd20-dd20-dd20-dddddddddddd"),
	}

	if err := p.deliverClaimed(context.Background(), q.card); err == nil {
		t.Fatal("existing CardKit update permission failure should be terminal")
	}
	if q.card.Transport != "cardkit" || len(legacy.sent) != 0 || len(q.abandonDeliveryCalls) != 1 {
		t.Fatalf("existing CardKit card was incorrectly downgraded: card=%+v legacy_sends=%d abandoned=%d",
			q.card, len(legacy.sent), len(q.abandonDeliveryCalls))
	}
}

func TestPatcherStopsRetryingPermanentDeliveryFailure(t *testing.T) {
	p, q, _ := newTestPatcher(t)
	taskID := uuidFromString(t, "ee200005-ee20-ee20-ee20-eeeeeeeeeeee")
	q.task = db.AgentTaskQueue{ID: taskID, ChatSessionID: q.binding.ChatSessionID}
	q.installation.Status = string(InstallationRevoked)
	q.cardErr = nil
	q.card = OutboundCardMessage{
		ID:     uuidFromString(t, "cc200005-cc20-cc20-cc20-cccccccccccc"),
		TaskID: taskID, ChatSessionID: q.binding.ChatSessionID,
		Transport: "cardkit", Status: string(CardStatusStreaming), DesiredRevision: 1,
		InflightRevision: pgtype.Int8{Int64: 1, Valid: true},
		LeaseToken:       uuidFromString(t, "dd200005-dd20-dd20-dd20-dddddddddddd"),
	}

	if err := p.deliverClaimed(context.Background(), q.card); err == nil {
		t.Fatal("revoked installation should fail delivery")
	}
	if len(q.abandonDeliveryCalls) != 1 || len(q.failDeliveryCalls) != 0 {
		t.Fatalf("abandon=%d retries=%d, want 1/0", len(q.abandonDeliveryCalls), len(q.failDeliveryCalls))
	}
	if !q.card.DeliveryFailedAt.Valid || q.card.NextAttemptAt.Valid {
		t.Fatalf("permanent failure remained retryable: %+v", q.card)
	}
}

func TestPatcherPersistenceFailureKeepsClaimIdentityForRetry(t *testing.T) {
	p, q, _ := newTestPatcher(t)
	taskID := uuidFromString(t, "ee200003-ee20-ee20-ee20-eeeeeeeeeeee")
	cardID := uuidFromString(t, "cc200003-cc20-cc20-cc20-cccccccccccc")
	lease := uuidFromString(t, "dd200003-dd20-dd20-dd20-dddddddddddd")
	q.task = db.AgentTaskQueue{ID: taskID, ChatSessionID: q.binding.ChatSessionID}
	q.cardErr = nil
	q.setPayloadErr = errors.New("database connection reset")
	q.card = OutboundCardMessage{
		ID: cardID, TaskID: taskID, ChatSessionID: q.binding.ChatSessionID,
		Transport: "cardkit", Status: string(CardStatusStreaming), DesiredRevision: 1,
		InflightRevision: pgtype.Int8{Int64: 1, Valid: true},
		InflightSequence: pgtype.Int4{Int32: 1, Valid: true},
		LeaseToken:       lease,
	}

	if err := p.deliverClaimed(context.Background(), q.card); err == nil {
		t.Fatal("payload persistence failure should schedule a retry")
	}
	if len(q.failDeliveryCalls) != 1 {
		t.Fatalf("retry writes=%d want 1", len(q.failDeliveryCalls))
	}
	got := q.failDeliveryCalls[0]
	if got.ID != cardID || got.LeaseToken != lease {
		t.Fatalf("retry lost claimed identity: got id=%v lease=%v", got.ID, got.LeaseToken)
	}
}
