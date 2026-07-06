package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// seedP4Strategy inserts a create_per_event strategy row (backend config data)
// for the given Swarm URL pointing at the given agent, and cleans up every issue
// it spawns plus the strategy row afterward (before the agent fixture is torn
// down). Strategy rows are plain data — there is no API to create them, so tests
// seed them directly the same way an operator would.
func seedP4Strategy(ctx context.Context, t *testing.T, swarmURL, agentID string) string {
	t.Helper()
	var stratID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO perforce_project_strategy (workspace_id, swarm_url, strategy, default_assignee_id, enabled)
		VALUES ($1, $2, 'create_per_event', $3, TRUE)
		RETURNING id
	`, testWorkspaceID, swarmURL, agentID).Scan(&stratID); err != nil {
		t.Fatalf("seed perforce strategy: %v", err)
	}
	t.Cleanup(func() {
		// Order matters: drop tasks + issues this strategy spawned before the
		// strategy row (whose default_assignee_id RESTRICTs the agent) and
		// before the agent fixture cleanup that runs after this one.
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id IN (SELECT issue_id FROM perforce_strategy_created_issue WHERE strategy_id = $1)`, stratID)
		testPool.Exec(ctx, `DELETE FROM activity_log WHERE issue_id IN (SELECT issue_id FROM perforce_strategy_created_issue WHERE strategy_id = $1)`, stratID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id IN (SELECT issue_id FROM perforce_strategy_created_issue WHERE strategy_id = $1)`, stratID)
		testPool.Exec(ctx, `DELETE FROM perforce_project_strategy WHERE id = $1`, stratID)
	})
	return stratID
}

func countStrategyIssues(ctx context.Context, t *testing.T, stratID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM perforce_strategy_created_issue WHERE strategy_id = $1`, stratID,
	).Scan(&n); err != nil {
		t.Fatalf("count strategy issues: %v", err)
	}
	return n
}

// TestP4SwarmWebhook_CreatePerEvent_CreatesIssue verifies that a review event
// for a project configured with the create_per_event strategy spawns an issue
// assigned to the configured default agent, with the changelist and author
// carried in the description — even though the description references no
// existing issue.
func TestP4SwarmWebhook_CreatePerEvent_CreatesIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	agentID := createHandlerTestAgent(t, "P4 Strategy Agent", []byte("[]"))
	stratID := seedP4Strategy(ctx, t, "http://swarm.strat.test", agentID)

	w := postP4Webhook(t, p4WebhookTestToken,
		p4WebhookPayload("http://swarm.strat.test", "no issue reference here", 1700000500, nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", w.Code, w.Body.String())
	}

	if got := countStrategyIssues(ctx, t, stratID); got != 1 {
		t.Fatalf("created issues = %d, want 1", got)
	}

	var (
		description  string
		assigneeType string
		assigneeID   string
		creatorType  string
	)
	if err := testPool.QueryRow(ctx, `
		SELECT i.description, i.assignee_type, i.assignee_id::text, i.creator_type
		FROM perforce_strategy_created_issue ci
		JOIN issue i ON i.id = ci.issue_id
		WHERE ci.strategy_id = $1
	`, stratID).Scan(&description, &assigneeType, &assigneeID, &creatorType); err != nil {
		t.Fatalf("load created issue: %v", err)
	}

	if assigneeType != "agent" || assigneeID != agentID {
		t.Errorf("assignee = %s/%s, want agent/%s", assigneeType, assigneeID, agentID)
	}
	if creatorType != "agent" {
		t.Errorf("creator_type = %q, want agent", creatorType)
	}
	// The description faithfully displays the webhook fields so the agent has
	// the full review context: changelist, author, state, branch, Swarm URL,
	// review id, plus the original review description.
	for _, want := range []string{
		"500120",                  // shelved CL (from review.changes)
		"alice",                   // author
		"approved",                // state
		"main",                    // swarm branch
		"http://swarm.strat.test", // swarm url
		"500123",                  // review id
		"no issue reference here", // original review description
	} {
		if !strings.Contains(description, want) {
			t.Errorf("description missing %q: %q", want, description)
		}
	}
}

// TestP4SwarmWebhook_CreatePerEvent_RetryDoesNotDuplicate verifies a webhook
// retry (same review.updated) does not create a second issue.
func TestP4SwarmWebhook_CreatePerEvent_RetryDoesNotDuplicate(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	agentID := createHandlerTestAgent(t, "P4 Strategy Agent Retry", []byte("[]"))
	stratID := seedP4Strategy(ctx, t, "http://swarm.strat.retry", agentID)

	payload := p4WebhookPayload("http://swarm.strat.retry", "x", 1700000500, nil)
	if w := postP4Webhook(t, p4WebhookTestToken, payload); w.Code != http.StatusAccepted {
		t.Fatalf("first post code = %d; body=%s", w.Code, w.Body.String())
	}
	// Identical redelivery (same review.updated) must be a no-op.
	if w := postP4Webhook(t, p4WebhookTestToken, payload); w.Code != http.StatusAccepted {
		t.Fatalf("retry post code = %d; body=%s", w.Code, w.Body.String())
	}

	if got := countStrategyIssues(ctx, t, stratID); got != 1 {
		t.Errorf("created issues = %d, want 1 (retry must not double-create)", got)
	}
}

// TestP4SwarmWebhook_CreatePerEvent_NewEventCreatesAnother verifies that a
// genuinely newer event (Swarm bumped review.updated) creates a second issue —
// the create_per_event contract is one issue per distinct event.
func TestP4SwarmWebhook_CreatePerEvent_NewEventCreatesAnother(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	agentID := createHandlerTestAgent(t, "P4 Strategy Agent Multi", []byte("[]"))
	stratID := seedP4Strategy(ctx, t, "http://swarm.strat.multi", agentID)

	if w := postP4Webhook(t, p4WebhookTestToken,
		p4WebhookPayload("http://swarm.strat.multi", "first", 1700000500, nil)); w.Code != http.StatusAccepted {
		t.Fatalf("first post code = %d; body=%s", w.Code, w.Body.String())
	}
	if w := postP4Webhook(t, p4WebhookTestToken,
		p4WebhookPayload("http://swarm.strat.multi", "second", 1700000900, nil)); w.Code != http.StatusAccepted {
		t.Fatalf("second post code = %d; body=%s", w.Code, w.Body.String())
	}

	if got := countStrategyIssues(ctx, t, stratID); got != 2 {
		t.Errorf("created issues = %d, want 2 (each distinct event creates an issue)", got)
	}
}

// TestP4SwarmWebhook_CreatePerEvent_IgnoresOtherEventTypes verifies the
// capability only reacts to review.created / review.updated: any other Swarm
// event_type is acknowledged (202) but creates no issue.
func TestP4SwarmWebhook_CreatePerEvent_IgnoresOtherEventTypes(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	agentID := createHandlerTestAgent(t, "P4 Strategy Agent Evt", []byte("[]"))
	stratID := seedP4Strategy(ctx, t, "http://swarm.strat.evt", agentID)

	for _, evt := range []string{"review.commented", "review.archived", "review.tested", ""} {
		payload := p4WebhookPayload("http://swarm.strat.evt", "x", 1700000500, nil)
		payload["event_type"] = evt
		if w := postP4Webhook(t, p4WebhookTestToken, payload); w.Code != http.StatusAccepted {
			t.Fatalf("event_type=%q code = %d, want 202; body=%s", evt, w.Code, w.Body.String())
		}
	}
	if got := countStrategyIssues(ctx, t, stratID); got != 0 {
		t.Errorf("created issues = %d, want 0 (non created/updated events must not create)", got)
	}
}

// TestP4SwarmWebhook_CreatePerEvent_CreatedEventCreatesIssue verifies
// review.created is an accepted event type (companion to review.updated).
func TestP4SwarmWebhook_CreatePerEvent_CreatedEventCreatesIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	agentID := createHandlerTestAgent(t, "P4 Strategy Agent Created", []byte("[]"))
	stratID := seedP4Strategy(ctx, t, "http://swarm.strat.created", agentID)

	payload := p4WebhookPayload("http://swarm.strat.created", "x", 1700000500, nil)
	payload["event_type"] = "review.created"
	if w := postP4Webhook(t, p4WebhookTestToken, payload); w.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	if got := countStrategyIssues(ctx, t, stratID); got != 1 {
		t.Errorf("created issues = %d, want 1 (review.created must create)", got)
	}
}

// TestP4SwarmWebhook_CreatePerEvent_RealPartopiaPayload locks the exact issue a
// real production Swarm payload (the partopia project) produces, so any future
// change to the field rendering is caught. The body below is the verbatim
// webhook payload: the submitter is sent as a structured review.author field,
// and there is no review.title — so the title falls back to "CL <cl> by
// <author>". If the payload later grows a title field, that is a different
// payload and belongs in its own test.
func TestP4SwarmWebhook_CreatePerEvent_RealPartopiaPayload(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	ctx := context.Background()
	t.Setenv("MULTICA_P4_SWARM_WEBHOOK_TOKEN", p4WebhookTestToken)
	agentID := createHandlerTestAgent(t, "Partopia Agent", []byte("[]"))
	stratID := seedP4Strategy(ctx, t, "http://partopia-swarm.lilithgame.com", agentID)

	body := []byte(`{
  "event_type": "review.updated",
  "swarm": {
    "url": "http://partopia-swarm.lilithgame.com",
    "branch": "main"
  },
  "review": {
    "id": 141630,
    "state": "needsReview",
    "description": "AI-REVIEW test description",
    "author": "wangjiajie",
    "changes": [141629],
    "commits": [],
    "created": 1,
    "updated": 1
  }
}`)
	if w := postP4Webhook(t, p4WebhookTestToken, body); w.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202; body=%s", w.Code, w.Body.String())
	}

	var title, description string
	if err := testPool.QueryRow(ctx, `
		SELECT i.title, i.description
		FROM perforce_strategy_created_issue ci
		JOIN issue i ON i.id = ci.issue_id
		WHERE ci.strategy_id = $1
	`, stratID).Scan(&title, &description); err != nil {
		t.Fatalf("load created issue: %v", err)
	}

	if want := "CL 141629 by wangjiajie"; title != want {
		t.Errorf("title = %q, want %q", title, want)
	}
	wantDesc := `Created from a Perforce / Helix Swarm review event.

- Review: #141630
- State: needsReview
- Author: wangjiajie
- Shelved CL: 141629
- Branch: main
- Swarm: http://partopia-swarm.lilithgame.com

---
AI-REVIEW test description`
	if description != wantDesc {
		t.Errorf("description mismatch:\n got: %q\nwant: %q", description, wantDesc)
	}
}
