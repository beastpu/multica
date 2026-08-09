package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
)

// The board in the workflow workbench asks for a node's issues and must not be
// answered with the workspace's.
//
// The workbench header and the board read the same screen from two different
// endpoints: the header goes through ListIssues, which has honoured
// workflow_instance_id and workflow_activity all along, and the board goes
// through /api/issues/table/*, which had no way to say "workflow" at all. The
// scope degraded to the whole workspace, so a run with 191 issues rendered
// columns holding 1074 — and it read as a real board, not as an error.
func TestIssueTableScope_WorkflowNarrowsToTheNode(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	// A node only materialises its carrier issue once an executor resolves, so
	// the activities carry a role and the run is started with it assigned.
	activity := func(key, name string) workflowdomain.NodeDefinition {
		return workflowdomain.NodeDefinition{
			Key: key, Kind: "activity", Name: name,
			OwnerRole: "owner", IssuePolicy: "auto",
			Executor: &workflowdomain.ExecutorDefinition{
				Kind: "role", Role: "owner",
				Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
			},
		}
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Table scope",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			activity("triage", "Triage"),
			activity("fix", "Fix"),
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "triage"},
			{From: "triage", To: "fix"},
			{From: "fix", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("table scope definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(t, "Table scope template", definition)
	hostID := createWorkflowHostForTest(t, "Table scope host")
	started := startWorkflowForTest(
		t, hostID, templateID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
		}},
		"table-scope-start",
	)
	triage := findWorkflowNodeResponse(t, started.Nodes, "triage", 1)

	// A node's issue is created by the materialiser, not by the start call, so
	// the carrier the scope filters on does not exist until it has run.
	for range 4 {
		worked, err := NewWorkflowMaterializer(testHandler).ProcessNext(ctx)
		if err != nil {
			t.Fatalf("materialise workflow tasks: %v", err)
		}
		if !worked {
			break
		}
	}
	triageIssue := workflowNodeIssueForTest(t, triage.ID)

	// An issue with no connection to the run. Present in the workspace, and the
	// thing a degraded scope drags into the board.
	token := fmt.Sprintf("table-scope-%d", time.Now().UnixNano())
	var outsiderID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, number, title, status, creator_type, creator_id)
		VALUES (
			$1,
			(SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1),
			$2, 'todo', 'member', $3
		)
		RETURNING id
	`, testWorkspaceID, token, testUserID).Scan(&outsiderID); err != nil {
		t.Fatalf("create unrelated issue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, outsiderID)
	})

	rowIDs := func(scope map[string]any) []string {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := newRequest(
			http.MethodPost,
			"/api/issues/table/rows?workspace_id="+testWorkspaceID,
			map[string]any{
				"query": map[string]any{
					"scope":   scope,
					"filters": map[string]any{},
					"sort":    map[string]any{"field": "created_at", "direction": "desc"},
				},
				"group":     map[string]any{"kind": "none"},
				"hierarchy": map[string]any{"enabled": false},
				"page":      map[string]any{"limit": 100},
			},
		)
		testHandler.ListIssueTableRows(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("table rows status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var response struct {
			Rows []struct {
				Issue struct {
					ID string `json:"id"`
				} `json:"issue"`
			} `json:"rows"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode table rows: %v", err)
		}
		ids := make([]string, 0, len(response.Rows))
		for _, row := range response.Rows {
			ids = append(ids, row.Issue.ID)
		}
		return ids
	}

	contains := func(ids []string, want string) bool {
		for _, id := range ids {
			if id == want {
				return true
			}
		}
		return false
	}

	// The workspace scope is what the workflow scope used to degrade into, so
	// the assertions below only mean something if it really does reach wider.
	baseline := rowIDs(map[string]any{"kind": "workspace"})
	if !contains(baseline, triageIssue) || !contains(baseline, outsiderID) {
		t.Fatalf(
			"workspace baseline does not span both issues; the narrowing assertions would pass vacuously: %v",
			baseline,
		)
	}

	// Scoped to one activity: that activity's issue, and nothing the run does
	// elsewhere.
	scoped := rowIDs(map[string]any{
		"kind":         "workflow",
		"instance_id":  started.Instance.ID,
		"activity_key": "triage",
	})
	if !contains(scoped, triageIssue) {
		t.Fatalf("activity scope dropped its own issue %s: %v", triageIssue, scoped)
	}
	if contains(scoped, outsiderID) {
		t.Fatalf("activity scope reached an unrelated workspace issue: %v", scoped)
	}
	if contains(scoped, hostID) {
		t.Fatalf("activity scope returned the host issue, which belongs to no activity: %v", scoped)
	}

	// Scoped to the run with no activity: the run's issues, still not the
	// workspace's. This is the workbench's "all workflow issues" tab.
	runScoped := rowIDs(map[string]any{
		"kind":        "workflow",
		"instance_id": started.Instance.ID,
	})
	if !contains(runScoped, triageIssue) {
		t.Fatalf("run scope dropped an activity issue %s: %v", triageIssue, runScoped)
	}
	if contains(runScoped, outsiderID) {
		t.Fatalf("run scope reached an unrelated workspace issue: %v", runScoped)
	}
}

// workflowNodeIssueForTest reads the carrier issue a node's task was
// materialised onto.
func workflowNodeIssueForTest(t *testing.T, nodeInstanceID string) string {
	t.Helper()
	var issueID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT issue_id::text FROM workflow_node_task
		WHERE workflow_node_instance_id = $1 AND issue_id IS NOT NULL
		LIMIT 1
	`, nodeInstanceID).Scan(&issueID); err != nil {
		t.Fatalf("workflow issue for node %s: %v", nodeInstanceID, err)
	}
	return issueID
}

// A malformed workflow scope must be refused, not silently widened. Answering
// it with the workspace is how the board came to show 1074 issues for a run
// that had 191.
func TestIssueTableScope_WorkflowRejectsAMissingInstance(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	recorder := httptest.NewRecorder()
	request := newRequest(
		http.MethodPost,
		"/api/issues/table/rows?workspace_id="+testWorkspaceID,
		map[string]any{
			"query": map[string]any{
				"scope":   map[string]any{"kind": "workflow"},
				"filters": map[string]any{},
				"sort":    map[string]any{"field": "created_at", "direction": "desc"},
			},
			"group":     map[string]any{"kind": "none"},
			"hierarchy": map[string]any{"enabled": false},
			"page":      map[string]any{"limit": 50},
		},
	)
	testHandler.ListIssueTableRows(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf(
			"workflow scope without an instance: status = %d, want 400, body = %s",
			recorder.Code, recorder.Body.String(),
		)
	}
}
