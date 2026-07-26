package handler

import (
	"context"
	"testing"

	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestApplyWorkflowNodeActionsSetsHostStatus(t *testing.T) {
	ctx := context.Background()
	var hostID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Node events host', 'todo', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create host issue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(
			context.Background(),
			`DELETE FROM issue WHERE id = $1`,
			hostID,
		)
	})

	// Explicit node actions apply even in independent mode: the mode only
	// governs the implicit workflow lifecycle.
	instance := db.WorkflowInstance{
		WorkspaceID:    parseUUID(testWorkspaceID),
		HostIssueID:    parseUUID(hostID),
		HostStatusMode: "independent",
	}
	testHandler.applyWorkflowNodeActions(ctx, instance, []workflowdomain.NodeActionDefinition{
		{Kind: "set_host_status", Status: "in_review"},
	})

	var status string
	if err := testPool.QueryRow(
		ctx, `SELECT status FROM issue WHERE id = $1`, hostID,
	).Scan(&status); err != nil {
		t.Fatalf("load host issue: %v", err)
	}
	if status != "in_review" {
		t.Fatalf("host status = %q, want in_review", status)
	}

	testHandler.applyWorkflowNodeEnterActions(ctx, instance, workflowdomain.Definition{
		Nodes: []workflowdomain.NodeDefinition{{
			Key: "triage", Kind: "activity", Name: "Triage",
			OnEnter: []workflowdomain.NodeActionDefinition{{
				Kind: "set_host_status", Status: "in_progress",
			}},
		}},
	}, []db.WorkflowNodeInstance{{NodeKey: "triage"}})

	if err := testPool.QueryRow(
		ctx, `SELECT status FROM issue WHERE id = $1`, hostID,
	).Scan(&status); err != nil {
		t.Fatalf("reload host issue: %v", err)
	}
	if status != "in_progress" {
		t.Fatalf("host status after enter actions = %q, want in_progress", status)
	}
}
