package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// driftTestPool is the shared connection for DB-backed tests in this package.
// It stays nil when no database is reachable so the pure-logic tests in the
// package still run; the DB-backed tests skip themselves instead.
var driftTestPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	if pool, err := pgxpool.New(ctx, dbURL); err == nil {
		if pool.Ping(ctx) == nil {
			driftTestPool = pool
		} else {
			pool.Close()
		}
	}
	code := m.Run()
	if driftTestPool != nil {
		driftTestPool.Close()
	}
	os.Exit(code)
}

// TestReconcileLocalStatusDriftSkipsStaleBindings is the end-to-end guard for
// the reported bug: a user hand-edits a synced issue to "done", but a stale
// binding (whose external_status_label still reads "OPEN" because the work item
// left the mapped-status filter and was never re-fetched) must NOT drag the
// issue back to the mapped "todo". A binding re-synced during the current run
// is still authoritative and is reconciled normally.
func TestReconcileLocalStatusDriftSkipsStaleBindings(t *testing.T) {
	if driftTestPool == nil {
		t.Skip("DATABASE_URL not reachable; skipping DB-backed drift test")
	}
	ctx := context.Background()
	queries := db.New(driftTestPool)
	svc := &FeishuProjectSyncService{Queries: queries}

	suffix := uuid.NewString()[:8]
	ws, err := queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:        "drift-" + suffix,
		Slug:        "drift-" + suffix,
		IssuePrefix: "DR",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = driftTestPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, ws.ID)
	})

	// Integration row only needs to exist as the binding FK target; the status
	// mapping is supplied on the cfg struct passed to reconcile, not read back
	// from this row.
	var integrationID pgtype.UUID
	if err := driftTestPool.QueryRow(ctx,
		`INSERT INTO feishu_project_integration (workspace_id, project_key, plugin_id, plugin_secret)
		 VALUES ($1, $2, 'pid', 'psecret') RETURNING id`,
		ws.ID, "pk-"+suffix,
	).Scan(&integrationID); err != nil {
		t.Fatalf("insert integration: %v", err)
	}

	creator := newDriftUUID()
	staleIssue := createDriftIssue(t, ctx, queries, ws.ID, creator, "done", 1)
	freshIssue := createDriftIssue(t, ctx, queries, ws.ID, creator, "backlog", 2)

	runStart := time.Now()
	// Synced an hour before the run -> stale; manual "done" must survive.
	insertDriftBinding(t, ctx, ws.ID, integrationID, staleIssue.ID, "stale-"+suffix, "OPEN", runStart.Add(-time.Hour))
	// Synced a minute after the run started -> fresh; "backlog" is real drift.
	insertDriftBinding(t, ctx, ws.ID, integrationID, freshIssue.ID, "fresh-"+suffix, "OPEN", runStart.Add(time.Minute))

	cfg := db.FeishuProjectIntegration{
		ID:            integrationID,
		WorkspaceID:   ws.ID,
		WorkItemTypes: feishuTestIssueTypes(`{"OPEN": "todo"}`),
	}

	updated, err := svc.reconcileLocalStatusDrift(ctx, cfg, runStart)
	if err != nil {
		t.Fatalf("reconcileLocalStatusDrift: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1 (only the fresh binding)", updated)
	}
	if got := driftIssueStatus(t, ctx, queries, ws.ID, staleIssue.ID); got != "done" {
		t.Fatalf("stale issue status = %q, want done (manual edit preserved)", got)
	}
	if got := driftIssueStatus(t, ctx, queries, ws.ID, freshIssue.ID); got != "todo" {
		t.Fatalf("fresh issue status = %q, want todo (drift reconciled)", got)
	}
}

// TestReconcileLocalStatusDriftSkipsWhenRunStartUnknown verifies the zero
// run-start guard: with no reliable cutoff we cannot tell which bindings are
// fresh, so reconcile is a no-op and leaves every local status untouched.
func TestReconcileLocalStatusDriftSkipsWhenRunStartUnknown(t *testing.T) {
	if driftTestPool == nil {
		t.Skip("DATABASE_URL not reachable; skipping DB-backed drift test")
	}
	ctx := context.Background()
	queries := db.New(driftTestPool)
	svc := &FeishuProjectSyncService{Queries: queries}

	suffix := uuid.NewString()[:8]
	ws, err := queries.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:        "drift0-" + suffix,
		Slug:        "drift0-" + suffix,
		IssuePrefix: "DZ",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = driftTestPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, ws.ID)
	})

	var integrationID pgtype.UUID
	if err := driftTestPool.QueryRow(ctx,
		`INSERT INTO feishu_project_integration (workspace_id, project_key, plugin_id, plugin_secret)
		 VALUES ($1, $2, 'pid', 'psecret') RETURNING id`,
		ws.ID, "pk-"+suffix,
	).Scan(&integrationID); err != nil {
		t.Fatalf("insert integration: %v", err)
	}

	issue := createDriftIssue(t, ctx, queries, ws.ID, newDriftUUID(), "backlog", 1)
	// Freshly synced just now: would reconcile to "todo" under a real run-start.
	insertDriftBinding(t, ctx, ws.ID, integrationID, issue.ID, "x-"+suffix, "OPEN", time.Now())

	cfg := db.FeishuProjectIntegration{
		ID:            integrationID,
		WorkspaceID:   ws.ID,
		WorkItemTypes: feishuTestIssueTypes(`{"OPEN": "todo"}`),
	}

	updated, err := svc.reconcileLocalStatusDrift(ctx, cfg, time.Time{})
	if err != nil {
		t.Fatalf("reconcileLocalStatusDrift: %v", err)
	}
	if updated != 0 {
		t.Fatalf("updated = %d, want 0 (zero run-start is a no-op)", updated)
	}
	if got := driftIssueStatus(t, ctx, queries, ws.ID, issue.ID); got != "backlog" {
		t.Fatalf("issue status = %q, want backlog (untouched)", got)
	}
}

func newDriftUUID() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}

func createDriftIssue(t *testing.T, ctx context.Context, queries *db.Queries, wsID, creator pgtype.UUID, status string, number int32) db.Issue {
	t.Helper()
	issue, err := queries.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID: wsID,
		Title:       "drift issue",
		Status:      status,
		Priority:    "none",
		CreatorType: "member",
		CreatorID:   creator,
		Number:      number,
	})
	if err != nil {
		t.Fatalf("create issue (status=%s): %v", status, err)
	}
	return issue
}

func insertDriftBinding(t *testing.T, ctx context.Context, wsID, integrationID, issueID pgtype.UUID, workItemID, statusLabel string, lastSyncedAt time.Time) {
	t.Helper()
	if _, err := driftTestPool.Exec(ctx,
		`INSERT INTO feishu_project_issue_binding
		   (workspace_id, integration_id, issue_id, project_key, work_item_type,
		    work_item_id, external_identifier, external_status_label, last_synced_at)
		 VALUES ($1, $2, $3, 'pk', 'issue', $4, $4, $5, $6)`,
		wsID, integrationID, issueID, workItemID, statusLabel, lastSyncedAt,
	); err != nil {
		t.Fatalf("insert binding %s: %v", workItemID, err)
	}
}

func driftIssueStatus(t *testing.T, ctx context.Context, queries *db.Queries, wsID, issueID pgtype.UUID) string {
	t.Helper()
	issue, err := queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issueID, WorkspaceID: wsID})
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	return issue.Status
}
