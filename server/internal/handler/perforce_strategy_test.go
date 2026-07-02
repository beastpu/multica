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
	if !strings.Contains(description, "500120") {
		t.Errorf("description missing changelist 500120: %q", description)
	}
	if !strings.Contains(description, "alice") {
		t.Errorf("description missing author alice: %q", description)
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
