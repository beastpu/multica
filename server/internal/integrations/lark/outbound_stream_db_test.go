package lark

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestOutboundDeliveryLeaseIsCrossReplicaSafeAndTerminalIsMonotonic(t *testing.T) {
	pool := channelScopeTestDB(t)
	ctx := context.Background()
	storeA := NewChannelStore(db.New(pool))
	storeB := NewChannelStore(db.New(pool))

	const (
		sessionID = "5c09e100-0000-4000-8000-000000000001"
		taskID    = "5c09e100-0000-4000-8000-000000000002"
	)
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM channel_outbound_card_message WHERE task_id = $1`, taskID)
	}
	cleanup()
	t.Cleanup(cleanup)

	params := CreateOutboundCardMessageParams{
		ChatSessionID: util.MustParseUUID(sessionID), TaskID: util.MustParseUUID(taskID),
		ChannelChatID: "oc_stream_test", Status: string(CardStatusPending), StartDelaySeconds: 0,
	}
	type createResult struct {
		card OutboundCardMessage
		err  error
	}
	created := make(chan createResult, 2)
	var createWG sync.WaitGroup
	for _, store := range []*ChannelStore{storeA, storeB} {
		createWG.Add(1)
		go func(s *ChannelStore) {
			defer createWG.Done()
			card, err := s.CreateLarkOutboundCardMessage(ctx, params)
			created <- createResult{card: card, err: err}
		}(store)
	}
	createWG.Wait()
	close(created)
	var placeholder OutboundCardMessage
	for result := range created {
		if result.err != nil {
			t.Fatalf("idempotent schedule: %v", result.err)
		}
		if placeholder.ID.Valid && placeholder.ID != result.card.ID {
			t.Fatalf("replicas returned different rows: %v vs %v", placeholder.ID, result.card.ID)
		}
		placeholder = result.card
	}

	type claimResult struct {
		card OutboundCardMessage
		err  error
	}
	claimed := make(chan claimResult, 2)
	var claimWG sync.WaitGroup
	for _, store := range []*ChannelStore{storeA, storeB} {
		claimWG.Add(1)
		go func(s *ChannelStore) {
			defer claimWG.Done()
			card, err := s.ClaimLarkOutboundCardDelivery(ctx, ClaimOutboundCardDeliveryParams{
				TaskID:     util.MustParseUUID(taskID),
				LeaseToken: pgtype.UUID{Bytes: uuid.New(), Valid: true}, LeaseSeconds: 30,
			})
			claimed <- claimResult{card: card, err: err}
		}(store)
	}
	claimWG.Wait()
	close(claimed)
	winners, conflicts := 0, 0
	var first OutboundCardMessage
	for result := range claimed {
		switch {
		case result.err == nil:
			winners++
			first = result.card
		case errors.Is(result.err, pgx.ErrNoRows):
			conflicts++
		default:
			t.Fatalf("claim: %v", result.err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("claim winners=%d conflicts=%d, want 1/1", winners, conflicts)
	}
	if first.InflightRevision.Int64 != 1 || first.InflightSequence.Valid {
		t.Fatalf("initial create/send must not allocate CardKit sequence: %+v", first)
	}
	var err error
	first, err = storeA.SetLarkOutboundInflightPayload(ctx, SetOutboundInflightPayloadParams{
		ID: first.ID, LeaseToken: first.LeaseToken, DesiredRevision: first.DesiredRevision, CardJSON: `{"schema":"2.0"}`,
	})
	if err != nil {
		t.Fatalf("prepare initial payload: %v", err)
	}
	first, err = storeA.SetLarkOutboundCardEntityID(ctx, SetOutboundCardEntityIDParams{
		ID: first.ID, LeaseToken: first.LeaseToken, ChannelCardID: "7371713483664506900",
	})
	if err != nil {
		t.Fatalf("persist card id: %v", err)
	}
	first, err = storeA.SetLarkOutboundCardMessageID(ctx, SetOutboundCardMessageIDParams{
		ID: first.ID, LeaseToken: first.LeaseToken, ChannelCardMessageID: "om_cardkit",
	})
	if err != nil {
		t.Fatalf("persist message id: %v", err)
	}

	// A final event landing while revision 1 is in flight is queued, never
	// allowed to overwrite the in-flight payload or be marked applied early.
	terminal, err := storeB.SetLarkOutboundTerminalDesired(ctx, SetOutboundTerminalDesiredParams{
		TaskID: util.MustParseUUID(taskID), Status: string(CardStatusFinal), TerminalContent: "最终答案",
	})
	if err != nil {
		t.Fatalf("queue terminal: %v", err)
	}
	if terminal.DesiredRevision != 2 || terminal.AppliedRevision != 0 || terminal.Status != string(CardStatusFinal) {
		t.Fatalf("terminal state=%+v", terminal)
	}

	if _, err := storeA.CompleteLarkOutboundCardDelivery(ctx, OutboundDeliveryLeaseParams{ID: first.ID, LeaseToken: first.LeaseToken}); err != nil {
		t.Fatalf("complete revision 1: %v", err)
	}
	second, err := storeB.ClaimLarkOutboundCardDelivery(ctx, ClaimOutboundCardDeliveryParams{
		TaskID:     util.MustParseUUID(taskID),
		LeaseToken: pgtype.UUID{Bytes: uuid.New(), Valid: true}, LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("claim terminal revision: %v", err)
	}
	if second.InflightRevision.Int64 != 2 || second.InflightSequence.Valid || second.Status != string(CardStatusFinal) {
		t.Fatalf("claim must not allocate update sequence: %+v", second)
	}
	second, err = storeB.SetLarkOutboundInflightPayload(ctx, SetOutboundInflightPayloadParams{
		ID: second.ID, LeaseToken: second.LeaseToken, DesiredRevision: second.DesiredRevision, CardJSON: `{"schema":"2.0"}`,
	})
	if err != nil {
		t.Fatalf("prepare terminal payload: %v", err)
	}
	if second.InflightSequence.Int32 != 1 || second.OperationSequence != 1 {
		t.Fatalf("first CardKit update sequence=%d operation=%d, want 1/1", second.InflightSequence.Int32, second.OperationSequence)
	}
}

func TestOutboundDeliveryRetryReusesInflightRevisionSequenceAndPayload(t *testing.T) {
	pool := channelScopeTestDB(t)
	ctx := context.Background()
	store := NewChannelStore(db.New(pool))
	taskID := util.MustParseUUID("5c09e100-0000-4000-8000-000000000012")
	_, _ = pool.Exec(ctx, `DELETE FROM channel_outbound_card_message WHERE task_id = $1`, taskID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM channel_outbound_card_message WHERE task_id = $1`, taskID)
	})
	if _, err := store.CreateLarkOutboundCardMessage(ctx, CreateOutboundCardMessageParams{
		ChatSessionID: util.MustParseUUID("5c09e100-0000-4000-8000-000000000011"),
		TaskID:        taskID, ChannelChatID: "oc_retry", Status: string(CardStatusPending),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	first, err := store.ClaimLarkOutboundCardDelivery(ctx, ClaimOutboundCardDeliveryParams{
		TaskID: taskID, LeaseToken: pgtype.UUID{Bytes: uuid.New(), Valid: true}, LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("claim first: %v", err)
	}
	first, err = store.SetLarkOutboundInflightPayload(ctx, SetOutboundInflightPayloadParams{
		ID: first.ID, LeaseToken: first.LeaseToken, DesiredRevision: first.DesiredRevision, CardJSON: `{"schema":"2.0"}`,
	})
	if err != nil {
		t.Fatalf("set payload: %v", err)
	}
	if _, err := store.FailLarkOutboundCardDelivery(ctx, FailOutboundCardDeliveryParams{
		ID: first.ID, LeaseToken: first.LeaseToken, LastError: "timeout", RetrySeconds: 0,
	}); err != nil {
		t.Fatalf("fail attempt: %v", err)
	}
	second, err := store.ClaimLarkOutboundCardDelivery(ctx, ClaimOutboundCardDeliveryParams{
		TaskID: taskID, LeaseToken: pgtype.UUID{Bytes: uuid.New(), Valid: true}, LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatalf("claim retry: %v", err)
	}
	if second.InflightRevision != first.InflightRevision || second.InflightSequence != first.InflightSequence || second.InflightCardJSON != first.InflightCardJSON {
		t.Fatalf("retry changed immutable inflight operation: first=%+v second=%+v", first, second)
	}
}
