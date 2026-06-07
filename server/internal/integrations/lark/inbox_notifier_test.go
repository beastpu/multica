package lark

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeInboxNotifierQueries struct {
	rows     []db.ListActiveLarkUserBindingsByMemberRow
	err      error
	arg      db.ListActiveLarkUserBindingsByMemberParams
	issue    db.Issue
	issueErr error
	issueArg pgtype.UUID
}

func (f *fakeInboxNotifierQueries) GetIssue(ctx context.Context, id pgtype.UUID) (db.Issue, error) {
	f.issueArg = id
	if f.issueErr != nil {
		return db.Issue{}, f.issueErr
	}
	return f.issue, nil
}

func (f *fakeInboxNotifierQueries) ListActiveLarkUserBindingsByMember(ctx context.Context, arg db.ListActiveLarkUserBindingsByMemberParams) ([]db.ListActiveLarkUserBindingsByMemberRow, error) {
	f.arg = arg
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
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
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.directTextOut) != 1 {
		t.Fatalf("expected one direct text send, got %d", len(api.directTextOut))
	}
	got := api.directTextOut[0]
	if got.OpenID != "ou_actor" {
		t.Fatalf("OpenID = %q, want actor bot binding recipient ou_actor", got.OpenID)
	}
	if got.InstallationID.AppID != "cli_actor" {
		t.Fatalf("AppID = %q, want cli_actor", got.InstallationID.AppID)
	}
	if !strings.Contains(got.Text, "Inbox: Quick create failed") || !strings.Contains(got.Text, "Reply here") {
		t.Fatalf("unexpected notification text: %q", got.Text)
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
			"type":           "status_changed",
			"severity":       "info",
			"issue_id":       uuidString(issueID),
			"title":          "Synced status changed",
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
	if len(api.directTextOut) != 1 {
		t.Fatalf("expected one direct text send, got %d", len(api.directTextOut))
	}
	got := api.directTextOut[0]
	if got.OpenID != "ou_assignee" {
		t.Fatalf("OpenID = %q, want assignee bot binding recipient ou_assignee", got.OpenID)
	}
	if got.InstallationID.AppID != "cli_assignee" {
		t.Fatalf("AppID = %q, want cli_assignee", got.InstallationID.AppID)
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
	if len(api.directTextOut) != 0 {
		t.Fatalf("expected no direct text send for unmatched agent bot, got %d", len(api.directTextOut))
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
