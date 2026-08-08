package handler

import (
	"context"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The oscillation seen on WTE-14841, reproduced against the database rather
// than the decision function: an issue carrying an older completed run and a
// live one, with the completed run reconciling. Only the pure rule was covered
// before, and the rule was never the doubtful part — the doubtful part is
// whether the handler's lookup identifies the live run at all.
func TestManagedHostStatusYieldsToTheLiveRun(t *testing.T) {
	ctx := context.Background()

	var hostID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Two runs, one host', 'in_progress', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create host issue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, hostID)
	})

	var workflowID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow (workspace_id, name, created_by)
		VALUES ($1, 'Host authority ' || gen_random_uuid(), $2)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&workflowID); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workflow WHERE id = $1`, workflowID)
	})
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_version (workspace_id, workflow_id, version, created_by)
		VALUES ($1, $2, 1, $3)
		RETURNING id
	`, testWorkspaceID, workflowID, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create workflow version: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(
			context.Background(), `DELETE FROM workflow_version WHERE id = $1`, versionID,
		)
	})

	newRun := func(status string) db.WorkflowInstance {
		t.Helper()
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO workflow_instance (
				workspace_id, workflow_id, workflow_version_id, host_issue_id,
				title, status, host_status_mode, started_by_type, started_by_id,
				started_at
			) VALUES ($1, $2, $3, $4, 'host authority', $5, 'managed', 'member', $6, now())
			RETURNING id
		`, testWorkspaceID, workflowID, versionID, hostID, status, testUserID).Scan(&id); err != nil {
			t.Fatalf("create %s run: %v", status, err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(
				context.Background(), `DELETE FROM workflow_instance WHERE id = $1`, id,
			)
		})
		return db.WorkflowInstance{
			ID:             parseUUID(id),
			WorkspaceID:    parseUUID(testWorkspaceID),
			HostIssueID:    parseUUID(hostID),
			HostStatusMode: "managed",
			Status:         status,
		}
	}

	hostStatus := func() string {
		t.Helper()
		var s string
		if err := testPool.QueryRow(
			ctx, `SELECT status FROM issue WHERE id = $1`, hostID,
		).Scan(&s); err != nil {
			t.Fatalf("load host issue: %v", err)
		}
		return s
	}

	finished := newRun("completed")
	live := newRun("running")

	if err := testHandler.updateManagedWorkflowHostStatus(ctx, finished, "done"); err != nil {
		t.Fatalf("completed run reconcile: %v", err)
	}
	if got := hostStatus(); got != "in_progress" {
		t.Fatalf("a completed run closed a host another run is still working on: %q", got)
	}

	// The live run keeps its say.
	if err := testHandler.updateManagedWorkflowHostStatus(ctx, live, "in_progress"); err != nil {
		t.Fatalf("live run reconcile: %v", err)
	}
	if got := hostStatus(); got != "in_progress" {
		t.Fatalf("live run could not write its own host status: %q", got)
	}

	// Once nothing is live, the finished run closes the issue as it always did.
	if _, err := testPool.Exec(
		ctx, `UPDATE workflow_instance SET status = 'completed' WHERE id = $1`,
		uuidToString(live.ID),
	); err != nil {
		t.Fatalf("complete the live run: %v", err)
	}
	if err := testHandler.updateManagedWorkflowHostStatus(ctx, finished, "done"); err != nil {
		t.Fatalf("completed run reconcile after the live run ended: %v", err)
	}
	if got := hostStatus(); got != "done" {
		t.Fatalf("host not closed once no run is live: %q", got)
	}
}
