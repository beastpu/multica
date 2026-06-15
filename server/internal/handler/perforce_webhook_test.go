package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const p4WebhookTestToken = "test-swarm-token"

// enablePerforce opts the test workspace into Perforce (default is off) and
// resets the flag after the test so unrelated tests are unaffected.
func enablePerforce(ctx context.Context, t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(ctx,
		`UPDATE workspace SET settings = COALESCE(settings,'{}'::jsonb) || '{"perforce_enabled":true}'::jsonb WHERE id=$1`,
		testWorkspaceID); err != nil {
		t.Fatalf("enable perforce: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `UPDATE workspace SET settings = settings - 'perforce_enabled' WHERE id=$1`, testWorkspaceID)
	})
}

// seedP4Connection registers a Swarm connection for the test workspace at the
// given URL and cleans it up afterward.
func seedP4Connection(ctx context.Context, t *testing.T, swarmURL string) {
	t.Helper()
	if _, err := testHandler.Queries.UpsertPerforceConnection(ctx, db.UpsertPerforceConnectionParams{
		WorkspaceID: parseUUID(testWorkspaceID),
		SwarmUrl:    swarmURL,
	}); err != nil {
		t.Fatalf("seed perforce connection: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM perforce_connection WHERE workspace_id = $1`, testWorkspaceID)
	})
}

// seedP4Issue creates an in-progress issue the webhook can close and cleans up
// the issue plus any review/link rows it accretes.
func seedP4Issue(ctx context.Context, t *testing.T) IssueResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "p4 webhook test",
		"status": "in_progress",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: %d %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	json.NewDecoder(w.Body).Decode(&created)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM issue_perforce_review WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM perforce_review WHERE workspace_id = $1`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM activity_log WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})
	return created
}

func p4WebhookPayload(swarmURL, desc string, updated int64, commits []int64) map[string]any {
	if commits == nil {
		commits = []int64{}
	}
	return map[string]any{
		"event_type": "review.updated",
		"sent_at":    "2026-06-15T12:00:00Z",
		"swarm":      map[string]any{"url": swarmURL, "branch": "main"},
		"review": map[string]any{
			"id":          500123,
			"state":       "approved",
			"title":       "fix crash",
			"description": desc,
			"author":      "alice",
			"changes":     []int64{500120},
			"commits":     commits,
			"created":     1700000000,
			"updated":     updated,
		},
	}
}

func postP4Webhook(t *testing.T, token string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if b, ok := payload.([]byte); ok {
		body = b
	} else {
		body, _ = json.Marshal(payload)
	}
	r := httptest.NewRequest("POST", "/api/webhooks/p4-swarm", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	testHandler.HandleP4SwarmWebhook(w, r)
	return w
}

func issueStatus(ctx context.Context, t *testing.T, id string) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read issue status: %v", err)
	}
	return status
}

func TestP4SwarmWebhook_NoTokenConfigured503(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", "")
	w := postP4Webhook(t, "anything", p4WebhookPayload("http://swarm.test", "x", 1, nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503", w.Code)
	}
}

func TestP4SwarmWebhook_BadToken401(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	w := postP4Webhook(t, "wrong-token", p4WebhookPayload("http://swarm.test", "x", 1, nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("code = %d, want 401", w.Code)
	}
}

func TestP4SwarmWebhook_MissingFields400(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	// swarm.url omitted.
	payload := map[string]any{
		"event_type": "review.updated",
		"review":     map[string]any{"id": 1, "state": "approved", "updated": 1},
	}
	w := postP4Webhook(t, p4WebhookTestToken, payload)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
}

func TestP4SwarmWebhook_CommittedAdvancesIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	seedP4Connection(ctx, t, "http://swarm.test")
	enablePerforce(ctx, t)
	issue := seedP4Issue(ctx, t)

	desc := "fix crash for " + issue.Identifier + " (no closing keyword needed)"
	// Trailing slash on the payload URL must still match the stored URL.
	w := postP4Webhook(t, p4WebhookTestToken, p4WebhookPayload("http://swarm.test/", desc, 1700000500, []int64{500125}))
	if w.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", w.Code, w.Body.String())
	}

	rows, err := testHandler.Queries.ListReviewsByIssue(ctx, parseUUID(issue.ID))
	if err != nil {
		t.Fatalf("ListReviewsByIssue: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("linked reviews = %d, want 1", len(rows))
	}
	if !rows[0].CommittedCl.Valid || rows[0].CommittedCl.Int32 != 500125 {
		t.Errorf("committed_cl = %+v, want 500125", rows[0].CommittedCl)
	}
	if got := issueStatus(ctx, t, issue.ID); got != "done" {
		t.Errorf("issue status = %q, want done", got)
	}
}

func TestP4SwarmWebhook_NotCommittedDoesNotAdvance(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	seedP4Connection(ctx, t, "http://swarm.test")
	enablePerforce(ctx, t)
	issue := seedP4Issue(ctx, t)

	desc := "wip for " + issue.Identifier
	w := postP4Webhook(t, p4WebhookTestToken, p4WebhookPayload("http://swarm.test", desc, 1700000500, nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", w.Code, w.Body.String())
	}

	rows, err := testHandler.Queries.ListReviewsByIssue(ctx, parseUUID(issue.ID))
	if err != nil {
		t.Fatalf("ListReviewsByIssue: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("linked reviews = %d, want 1 (linked but not advanced)", len(rows))
	}
	if rows[0].CommittedCl.Valid {
		t.Errorf("committed_cl = %+v, want NULL", rows[0].CommittedCl)
	}
	if got := issueStatus(ctx, t, issue.ID); got != "in_progress" {
		t.Errorf("issue status = %q, want in_progress", got)
	}
}

func TestP4SwarmWebhook_NoIssueMatchIgnored(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	seedP4Connection(ctx, t, "http://swarm.test")
	enablePerforce(ctx, t)

	w := postP4Webhook(t, p4WebhookTestToken, p4WebhookPayload("http://swarm.test", "no issue reference here", 1700000500, []int64{500125}))
	if w.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM perforce_review WHERE workspace_id = $1`, testWorkspaceID).Scan(&n); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if n != 0 {
		t.Errorf("stored reviews = %d, want 0 (unmatched review must not be stored)", n)
	}
}

// TestP4SwarmWebhook_StaleEventDoesNotClobber verifies the review.updated
// watermark: a delayed older event must not overwrite the committed state.
func TestP4SwarmWebhook_StaleEventDoesNotClobber(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	seedP4Connection(ctx, t, "http://swarm.test")
	enablePerforce(ctx, t)
	issue := seedP4Issue(ctx, t)
	desc := "fix " + issue.Identifier

	// Newer committed event first → issue done, committed_cl recorded.
	if w := postP4Webhook(t, p4WebhookTestToken, p4WebhookPayload("http://swarm.test", desc, 1700000500, []int64{500125})); w.Code != http.StatusAccepted {
		t.Fatalf("first post code = %d; body=%s", w.Code, w.Body.String())
	}
	// Older event (smaller updated, no commits) arrives late → must be skipped.
	if w := postP4Webhook(t, p4WebhookTestToken, p4WebhookPayload("http://swarm.test", desc, 1700000400, nil)); w.Code != http.StatusAccepted {
		t.Fatalf("stale post code = %d; body=%s", w.Code, w.Body.String())
	}

	rows, err := testHandler.Queries.ListReviewsByIssue(ctx, parseUUID(issue.ID))
	if err != nil {
		t.Fatalf("ListReviewsByIssue: %v", err)
	}
	if len(rows) != 1 || !rows[0].CommittedCl.Valid || rows[0].CommittedCl.Int32 != 500125 {
		t.Fatalf("committed_cl not preserved after stale event: %+v", rows)
	}
	if got := issueStatus(ctx, t, issue.ID); got != "done" {
		t.Errorf("issue status = %q, want done (stale event must not regress)", got)
	}
}
