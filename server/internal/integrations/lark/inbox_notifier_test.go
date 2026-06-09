package lark

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeInboxNotifierQueries struct {
	rows         []db.ListActiveLarkUserBindingsByMemberRow
	err          error
	arg          db.ListActiveLarkUserBindingsByMemberParams
	issue        db.Issue
	issueErr     error
	issueArg     pgtype.UUID
	workspace    db.Workspace
	workspaceErr error
	workspaceArg pgtype.UUID
	claims       map[string]bool
	claimCalls   int
	claimArg     db.ClaimLarkInboxNotificationDeliveryParams
	issueCard    db.LarkInboxIssueCard
	issueCardErr error
	issueCardArg db.GetLarkInboxIssueCardParams
	cardItems    []db.InboxItem
	cardItemsArg db.ListLarkInboxIssueCardItemsParams
	touchCardID  pgtype.UUID
	upsertArg    db.UpsertLarkInboxIssueCardParams
}

func (f *fakeInboxNotifierQueries) GetIssue(ctx context.Context, id pgtype.UUID) (db.Issue, error) {
	f.issueArg = id
	if f.issueErr != nil {
		return db.Issue{}, f.issueErr
	}
	return f.issue, nil
}

func (f *fakeInboxNotifierQueries) GetWorkspace(ctx context.Context, id pgtype.UUID) (db.Workspace, error) {
	f.workspaceArg = id
	if f.workspaceErr != nil {
		return db.Workspace{}, f.workspaceErr
	}
	return f.workspace, nil
}

func (f *fakeInboxNotifierQueries) ListActiveLarkUserBindingsByMember(ctx context.Context, arg db.ListActiveLarkUserBindingsByMemberParams) ([]db.ListActiveLarkUserBindingsByMemberRow, error) {
	f.arg = arg
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func (f *fakeInboxNotifierQueries) ClaimLarkInboxNotificationDelivery(ctx context.Context, arg db.ClaimLarkInboxNotificationDeliveryParams) (bool, error) {
	f.claimCalls++
	f.claimArg = arg
	if f.claims == nil {
		f.claims = map[string]bool{}
	}
	key := uuidString(arg.InboxItemID) + "|" + uuidString(arg.InstallationID) + "|" + arg.LarkOpenID
	if f.claims[key] {
		return false, nil
	}
	f.claims[key] = true
	return true, nil
}

func (f *fakeInboxNotifierQueries) GetLarkInboxIssueCard(ctx context.Context, arg db.GetLarkInboxIssueCardParams) (db.LarkInboxIssueCard, error) {
	f.issueCardArg = arg
	if f.issueCardErr != nil {
		return db.LarkInboxIssueCard{}, f.issueCardErr
	}
	return f.issueCard, nil
}

func (f *fakeInboxNotifierQueries) ListLarkInboxIssueCardItems(ctx context.Context, arg db.ListLarkInboxIssueCardItemsParams) ([]db.InboxItem, error) {
	f.cardItemsArg = arg
	return f.cardItems, nil
}

func (f *fakeInboxNotifierQueries) TouchLarkInboxIssueCard(ctx context.Context, id pgtype.UUID) error {
	f.touchCardID = id
	return nil
}

func (f *fakeInboxNotifierQueries) UpsertLarkInboxIssueCard(ctx context.Context, arg db.UpsertLarkInboxIssueCardParams) (db.LarkInboxIssueCard, error) {
	f.upsertArg = arg
	return db.LarkInboxIssueCard{
		ID:                mustUUID("cccccccc-cccc-cccc-cccc-cccccccccccc"),
		WorkspaceID:       arg.WorkspaceID,
		RecipientID:       arg.RecipientID,
		IssueID:           arg.IssueID,
		InstallationID:    arg.InstallationID,
		LarkOpenID:        arg.LarkOpenID,
		LarkCardMessageID: arg.LarkCardMessageID,
	}, nil
}

func TestInboxNotifierSendsDMViaActorAgentBot(t *testing.T) {
	workspaceID := mustUUID("11111111-1111-1111-1111-111111111111")
	userID := mustUUID("22222222-2222-2222-2222-222222222222")
	otherAgentID := mustUUID("33333333-3333-3333-3333-333333333333")
	actorAgentID := mustUUID("44444444-4444-4444-4444-444444444444")
	q := &fakeInboxNotifierQueries{
		rows: []db.ListActiveLarkUserBindingsByMemberRow{
			inboxBindingRow(workspaceID, userID, otherAgentID, "cli_other", "ou_other"),
			inboxBindingRow(workspaceID, userID, actorAgentID, "cli_actor", "ou_actor"),
		},
	}
	api := &stubAPIClientWithRecorder{configured: true}
	notifier := NewInboxNotifier(q, stubCredentialsResolver{secret: "secret"}, api, InboxNotifierConfig{})

	err := notifier.notify(context.Background(), map[string]any{
		"item": map[string]any{
			"id":             "55555555-5555-5555-5555-555555555555",
			"workspace_id":   uuidString(workspaceID),
			"recipient_type": "member",
			"recipient_id":   uuidString(userID),
			"type":           "quick_create_failed",
			"severity":       "action_required",
			"title":          "Quick create failed",
			"body":           "agent exited with code 1",
			"actor_type":     "agent",
			"actor_id":       uuidString(actorAgentID),
		},
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if q.arg.WorkspaceID != workspaceID || q.arg.MulticaUserID != userID {
		t.Fatalf("binding lookup arg = %+v", q.arg)
	}
	if q.claimCalls != 1 || q.claimArg.LarkOpenID != "ou_actor" {
		t.Fatalf("unexpected delivery claim calls=%d arg=%+v", q.claimCalls, q.claimArg)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.directCardsOut) != 1 {
		t.Fatalf("expected one direct card send, got %d", len(api.directCardsOut))
	}
	got := api.directCardsOut[0]
	if got.OpenID != "ou_actor" {
		t.Fatalf("OpenID = %q, want actor bot binding recipient ou_actor", got.OpenID)
	}
	if got.InstallationID.AppID != "cli_actor" {
		t.Fatalf("AppID = %q, want cli_actor", got.InstallationID.AppID)
	}
	if !strings.Contains(got.CardJSON, `"title":{"content":"Quick create failed"`) ||
		!strings.Contains(got.CardJSON, `"tag":"lark_md"`) ||
		!strings.Contains(got.CardJSON, "Quick create failed") ||
		!strings.Contains(got.CardJSON, "agent exited with code 1") {
		t.Fatalf("unexpected notification card: %q", got.CardJSON)
	}
}

func TestInboxNotifierSkipsDuplicateDeliveryClaim(t *testing.T) {
	workspaceID := mustUUID("11111111-1111-1111-1111-111111111111")
	userID := mustUUID("22222222-2222-2222-2222-222222222222")
	actorAgentID := mustUUID("44444444-4444-4444-4444-444444444444")
	itemID := "55555555-5555-5555-5555-555555555555"
	q := &fakeInboxNotifierQueries{
		rows: []db.ListActiveLarkUserBindingsByMemberRow{
			inboxBindingRow(workspaceID, userID, actorAgentID, "cli_actor", "ou_actor"),
		},
	}
	api := &stubAPIClientWithRecorder{configured: true}
	notifier := NewInboxNotifier(q, stubCredentialsResolver{secret: "secret"}, api, InboxNotifierConfig{})
	payload := map[string]any{
		"item": map[string]any{
			"id":             itemID,
			"workspace_id":   uuidString(workspaceID),
			"recipient_type": "member",
			"recipient_id":   uuidString(userID),
			"type":           "quick_create_failed",
			"severity":       "action_required",
			"title":          "Quick create failed",
			"actor_type":     "agent",
			"actor_id":       uuidString(actorAgentID),
		},
	}

	if err := notifier.notify(context.Background(), payload); err != nil {
		t.Fatalf("first notify: %v", err)
	}
	if err := notifier.notify(context.Background(), payload); err != nil {
		t.Fatalf("duplicate notify: %v", err)
	}

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.directCardsOut) != 1 {
		t.Fatalf("duplicate delivery claim should send one card, got %d", len(api.directCardsOut))
	}
	if q.claimCalls != 2 {
		t.Fatalf("expected both attempts to claim delivery, got %d", q.claimCalls)
	}
}

func TestInboxNotifierFallsBackToAssigneeAgentBot(t *testing.T) {
	workspaceID := mustUUID("11111111-1111-1111-1111-111111111111")
	userID := mustUUID("22222222-2222-2222-2222-222222222222")
	otherAgentID := mustUUID("33333333-3333-3333-3333-333333333333")
	assigneeAgentID := mustUUID("44444444-4444-4444-4444-444444444444")
	issueID := mustUUID("55555555-5555-5555-5555-555555555555")
	q := &fakeInboxNotifierQueries{
		rows: []db.ListActiveLarkUserBindingsByMemberRow{
			inboxBindingRow(workspaceID, userID, otherAgentID, "cli_other", "ou_other"),
			inboxBindingRow(workspaceID, userID, assigneeAgentID, "cli_assignee", "ou_assignee"),
		},
		issue: db.Issue{
			ID:           issueID,
			AssigneeType: pgtype.Text{String: "agent", Valid: true},
			AssigneeID:   assigneeAgentID,
		},
	}
	api := &stubAPIClientWithRecorder{configured: true}
	notifier := NewInboxNotifier(q, stubCredentialsResolver{secret: "secret"}, api, InboxNotifierConfig{})

	err := notifier.notify(context.Background(), map[string]any{
		"item": map[string]any{
			"id":             "66666666-6666-6666-6666-666666666666",
			"workspace_id":   uuidString(workspaceID),
			"recipient_type": "member",
			"recipient_id":   uuidString(userID),
			"type":           "new_comment",
			"severity":       "info",
			"issue_id":       uuidString(issueID),
			"title":          "Issue updated",
			"body":           "Agent finished the work",
			"actor_type":     "system",
		},
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if q.issueArg != issueID {
		t.Fatalf("GetIssue arg = %v, want %v", q.issueArg, issueID)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.directCardsOut) != 1 {
		t.Fatalf("expected one direct card send, got %d", len(api.directCardsOut))
	}
	got := api.directCardsOut[0]
	if got.OpenID != "ou_assignee" {
		t.Fatalf("OpenID = %q, want assignee bot binding recipient ou_assignee", got.OpenID)
	}
	if got.InstallationID.AppID != "cli_assignee" {
		t.Fatalf("AppID = %q, want cli_assignee", got.InstallationID.AppID)
	}
}

func TestInboxNotifierSendsMergedIssueCardForFirstLifecycleItem(t *testing.T) {
	workspaceID := mustUUID("11111111-1111-1111-1111-111111111111")
	userID := mustUUID("22222222-2222-2222-2222-222222222222")
	actorAgentID := mustUUID("44444444-4444-4444-4444-444444444444")
	issueID := mustUUID("55555555-5555-5555-5555-555555555555")
	q := &fakeInboxNotifierQueries{
		rows: []db.ListActiveLarkUserBindingsByMemberRow{
			inboxBindingRow(workspaceID, userID, actorAgentID, "cli_actor", "ou_actor"),
		},
		issue: db.Issue{ID: issueID, Number: 113, Title: "查询今天上海天气"},
		workspace: db.Workspace{
			ID:          workspaceID,
			Slug:        "all",
			IssuePrefix: "All",
		},
		issueCardErr: pgx.ErrNoRows,
	}
	api := &stubAPIClientWithRecorder{configured: true}
	notifier := NewInboxNotifier(q, stubCredentialsResolver{secret: "secret"}, api, InboxNotifierConfig{
		PublicURL: "https://multica.lilithgames.com",
	})

	err := notifier.notify(context.Background(), map[string]any{
		"item": map[string]any{
			"id":             "66666666-6666-6666-6666-666666666666",
			"workspace_id":   uuidString(workspaceID),
			"recipient_type": "member",
			"recipient_id":   uuidString(userID),
			"type":           "quick_create_done",
			"severity":       "info",
			"issue_id":       uuidString(issueID),
			"title":          "查询今天上海天气",
			"actor_type":     "agent",
			"actor_id":       uuidString(actorAgentID),
		},
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if q.cardItemsArg.WorkspaceID != workspaceID || q.cardItemsArg.RecipientID != userID || q.cardItemsArg.IssueID != issueID {
		t.Fatalf("unexpected card items arg: %+v", q.cardItemsArg)
	}
	if got := strings.Join(q.cardItemsArg.Types, ","); got != "quick_create_done,status_changed,new_comment" {
		t.Fatalf("card item types = %q", got)
	}
	if q.upsertArg.LarkCardMessageID != "lark-direct-card-msg-id" || q.upsertArg.IssueID != issueID || q.upsertArg.LarkOpenID != "ou_actor" {
		t.Fatalf("unexpected issue card upsert arg: %+v", q.upsertArg)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.directCardsOut) != 1 {
		t.Fatalf("expected one direct card send, got %d", len(api.directCardsOut))
	}
	card := api.directCardsOut[0].CardJSON
	for _, want := range []string{`"update_multi":true`, "[All-113] 查询今天上海天气", "✅ Issue 已创建", "在 Multica 中查看"} {
		if !strings.Contains(card, want) {
			t.Fatalf("merged card missing %q: %s", want, card)
		}
	}
}

func TestInboxNotifierPatchesExistingMergedIssueCard(t *testing.T) {
	workspaceID := mustUUID("11111111-1111-1111-1111-111111111111")
	userID := mustUUID("22222222-2222-2222-2222-222222222222")
	actorAgentID := mustUUID("44444444-4444-4444-4444-444444444444")
	issueID := mustUUID("55555555-5555-5555-5555-555555555555")
	cardID := mustUUID("77777777-7777-7777-7777-777777777777")
	q := &fakeInboxNotifierQueries{
		rows: []db.ListActiveLarkUserBindingsByMemberRow{
			inboxBindingRow(workspaceID, userID, actorAgentID, "cli_actor", "ou_actor"),
		},
		issue: db.Issue{ID: issueID, Number: 113},
		workspace: db.Workspace{
			ID:          workspaceID,
			Slug:        "all",
			IssuePrefix: "All",
		},
		issueCard: db.LarkInboxIssueCard{
			ID:                cardID,
			WorkspaceID:       workspaceID,
			RecipientID:       userID,
			IssueID:           issueID,
			InstallationID:    mustUUID("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
			LarkOpenID:        "ou_actor",
			LarkCardMessageID: "om_existing_card",
		},
		cardItems: []db.InboxItem{
			{
				ID:            mustUUID("66666666-6666-6666-6666-666666666666"),
				WorkspaceID:   workspaceID,
				RecipientType: "member",
				RecipientID:   userID,
				Type:          "quick_create_done",
				Severity:      "info",
				IssueID:       issueID,
				Title:         "查询今天上海天气",
			},
		},
	}
	api := &stubAPIClientWithRecorder{configured: true}
	notifier := NewInboxNotifier(q, stubCredentialsResolver{secret: "secret"}, api, InboxNotifierConfig{
		PublicURL: "https://multica.lilithgames.com",
	})

	err := notifier.notify(context.Background(), map[string]any{
		"item": map[string]any{
			"id":             "88888888-8888-8888-8888-888888888888",
			"workspace_id":   uuidString(workspaceID),
			"recipient_type": "member",
			"recipient_id":   uuidString(userID),
			"type":           "new_comment",
			"severity":       "info",
			"issue_id":       uuidString(issueID),
			"title":          "查询今天上海天气",
			"body":           "今天上海天气：白天阵雨转多云。",
			"actor_type":     "agent",
			"actor_id":       uuidString(actorAgentID),
		},
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if q.touchCardID != cardID {
		t.Fatalf("TouchLarkInboxIssueCard id = %v, want %v", q.touchCardID, cardID)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.directCardsOut) != 0 {
		t.Fatalf("existing card should be patched, not re-sent; got sends=%d", len(api.directCardsOut))
	}
	if len(api.patches) != 1 {
		t.Fatalf("expected one card patch, got %d", len(api.patches))
	}
	patch := api.patches[0]
	if patch.LarkCardMessageID != "om_existing_card" {
		t.Fatalf("patch message id = %q", patch.LarkCardMessageID)
	}
	for _, want := range []string{"✅ Issue 已创建", "💬 Agent 评论", "今天上海天气：白天阵雨转多云。"} {
		if !strings.Contains(patch.CardJSON, want) {
			t.Fatalf("patched card missing %q: %s", want, patch.CardJSON)
		}
	}
}

func TestInboxNotifierSkipsWhenNoAgentBotMatches(t *testing.T) {
	workspaceID := mustUUID("11111111-1111-1111-1111-111111111111")
	userID := mustUUID("22222222-2222-2222-2222-222222222222")
	otherAgentID := mustUUID("33333333-3333-3333-3333-333333333333")
	actorAgentID := mustUUID("44444444-4444-4444-4444-444444444444")
	q := &fakeInboxNotifierQueries{
		rows: []db.ListActiveLarkUserBindingsByMemberRow{
			inboxBindingRow(workspaceID, userID, otherAgentID, "cli_other", "ou_other"),
		},
	}
	api := &stubAPIClientWithRecorder{configured: true}
	notifier := NewInboxNotifier(q, stubCredentialsResolver{secret: "secret"}, api, InboxNotifierConfig{})

	err := notifier.notify(context.Background(), map[string]any{
		"item": map[string]any{
			"id":             "55555555-5555-5555-5555-555555555555",
			"workspace_id":   uuidString(workspaceID),
			"recipient_type": "member",
			"recipient_id":   uuidString(userID),
			"type":           "quick_create_failed",
			"severity":       "action_required",
			"title":          "Quick create failed",
			"actor_type":     "agent",
			"actor_id":       uuidString(actorAgentID),
		},
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.directCardsOut) != 0 {
		t.Fatalf("expected no direct card send for unmatched agent bot, got %d", len(api.directCardsOut))
	}
}

func TestInboxNotifierSkipsNonMemberRecipients(t *testing.T) {
	q := &fakeInboxNotifierQueries{}
	api := &stubAPIClientWithRecorder{configured: true}
	notifier := NewInboxNotifier(q, stubCredentialsResolver{secret: "secret"}, api, InboxNotifierConfig{})

	err := notifier.notify(context.Background(), map[string]any{
		"item": map[string]any{
			"workspace_id":   "11111111-1111-1111-1111-111111111111",
			"recipient_type": "agent",
			"recipient_id":   "22222222-2222-2222-2222-222222222222",
			"title":          "Ignored",
		},
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if q.arg.WorkspaceID.Valid {
		t.Fatalf("non-member recipient should not query bindings")
	}
}

func TestInboxNotifierSendsNewCommentAsMarkdownCard(t *testing.T) {
	workspaceID := mustUUID("11111111-1111-1111-1111-111111111111")
	userID := mustUUID("22222222-2222-2222-2222-222222222222")
	actorAgentID := mustUUID("44444444-4444-4444-4444-444444444444")
	q := &fakeInboxNotifierQueries{
		rows: []db.ListActiveLarkUserBindingsByMemberRow{
			inboxBindingRow(workspaceID, userID, actorAgentID, "cli_actor", "ou_actor"),
		},
		workspace: db.Workspace{ID: workspaceID, Slug: "tide-server", IssuePrefix: "TID"},
	}
	api := &stubAPIClientWithRecorder{configured: true}
	notifier := NewInboxNotifier(q, stubCredentialsResolver{secret: "secret"}, api, InboxNotifierConfig{
		PublicURL: "https://multica.lilithgames.com",
	})

	err := notifier.notify(context.Background(), map[string]any{
		"item": map[string]any{
			"id":             "55555555-5555-5555-5555-555555555555",
			"workspace_id":   uuidString(workspaceID),
			"recipient_type": "member",
			"recipient_id":   uuidString(userID),
			"type":           "new_comment",
			"severity":       "info",
			"issue_id":       "66666666-6666-6666-6666-666666666666",
			"title":          "Issue updated",
			"body":           "**结果**\n- 已清理\n- 保留 `tide`",
			"actor_type":     "agent",
			"actor_id":       uuidString(actorAgentID),
		},
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.directCardsOut) != 1 {
		t.Fatalf("expected one direct card send for new_comment, got %d", len(api.directCardsOut))
	}
	card := api.directCardsOut[0].CardJSON
	for _, want := range []string{`"tag":"lark_md"`, "**💬 Agent 评论**", "**结果**", "在 Multica 中查看", "https://multica.lilithgames.com/tide-server/issues/66666666-6666-6666-6666-666666666666"} {
		if !strings.Contains(card, want) {
			t.Fatalf("new_comment card missing %q: %s", want, card)
		}
	}
}

func TestInboxNotifierRejectsMissingInboxItemID(t *testing.T) {
	workspaceID := mustUUID("11111111-1111-1111-1111-111111111111")
	userID := mustUUID("22222222-2222-2222-2222-222222222222")
	q := &fakeInboxNotifierQueries{}
	api := &stubAPIClientWithRecorder{configured: true}
	notifier := NewInboxNotifier(q, stubCredentialsResolver{secret: "secret"}, api, InboxNotifierConfig{})

	err := notifier.notify(context.Background(), map[string]any{
		"item": map[string]any{
			"workspace_id":   uuidString(workspaceID),
			"recipient_type": "member",
			"recipient_id":   uuidString(userID),
			"title":          "Missing id",
		},
	})
	if err == nil {
		t.Fatal("expected missing inbox item id error")
	}
	if q.arg.WorkspaceID.Valid {
		t.Fatalf("missing inbox item id should not query bindings")
	}
}

func inboxBindingRow(workspaceID, userID, agentID pgtype.UUID, appID, openID string) db.ListActiveLarkUserBindingsByMemberRow {
	return db.ListActiveLarkUserBindingsByMemberRow{
		LarkUserBinding: db.LarkUserBinding{
			WorkspaceID:    workspaceID,
			MulticaUserID:  userID,
			InstallationID: mustUUID("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
			LarkOpenID:     openID,
		},
		LarkInstallation: db.LarkInstallation{
			ID:          mustUUID("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			AppID:       appID,
			Status:      string(InstallationActive),
		},
	}
}
