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

// Filtering the issue list by workflow silently did nothing: the client sends
// workflow_workflow_id and the handler read workflow_template_id, so the
// predicate was never added — the filter chip counted 1 and the list came back
// whole. The column it filters on was renamed too (template_id -> workflow_id
// in migration 315), so reading the right param against the old column would
// have traded a silent no-op for a 500.
func TestListIssues_FiltersByWorkflow(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Issue filter",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "owner", IssuePolicy: "auto",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("issue filter definition invalid: %v", err)
	}
	workflowID := createPublishedWorkflowForTest(t, "Issue filter workflow", definition)
	hostID := createWorkflowHostForTest(t, "Issue filter host")
	started := startWorkflowForTest(
		t, hostID, workflowID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
		}},
		"issue-workflow-filter-start",
	)
	work := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
	for range 4 {
		worked, err := NewWorkflowMaterializer(testHandler).ProcessNext(ctx)
		if err != nil {
			t.Fatalf("materialise workflow tasks: %v", err)
		}
		if !worked {
			break
		}
	}
	nodeIssue := workflowNodeIssueForTest(t, work.ID)

	// An ordinary workspace issue: what an ignored filter drags into the list.
	token := fmt.Sprintf("issue-workflow-filter-%d", time.Now().UnixNano())
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

	list := func(query string) (int, []string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		testHandler.ListIssues(recorder, newRequest(
			http.MethodGet,
			"/api/issues?workspace_id="+testWorkspaceID+"&limit=500"+query,
			nil,
		))
		if recorder.Code != http.StatusOK {
			return recorder.Code, nil
		}
		var response struct {
			Issues []IssueResponse `json:"issues"`
		}
		if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
			t.Fatalf("decode issue list: %v", err)
		}
		ids := make([]string, 0, len(response.Issues))
		for _, issue := range response.Issues {
			ids = append(ids, issue.ID)
		}
		return recorder.Code, ids
	}
	contains := func(ids []string, want string) bool {
		for _, id := range ids {
			if id == want {
				return true
			}
		}
		return false
	}

	// Unfiltered spans both, so the narrowing assertion below means something.
	if status, ids := list(""); status != http.StatusOK ||
		!contains(ids, nodeIssue) || !contains(ids, outsiderID) {
		t.Fatalf(
			"unfiltered list does not span both issues (status=%d); the filter assertion would pass vacuously: %v",
			status, ids,
		)
	}

	status, filtered := list("&workflow_workflow_id=" + workflowID)
	if status != http.StatusOK {
		t.Fatalf("workflow_workflow_id filter status = %d", status)
	}
	if !contains(filtered, nodeIssue) {
		t.Fatalf("workflow filter dropped the run's own issue %s: %v", nodeIssue, filtered)
	}
	if contains(filtered, outsiderID) {
		t.Fatalf("workflow filter kept an unrelated workspace issue: %v", filtered)
	}

	if status, _ := list("&workflow_workflow_id=not-a-uuid"); status != http.StatusBadRequest {
		t.Fatalf("invalid workflow_workflow_id status = %d, want 400", status)
	}
}

// The Issues page reads every view — list, board, swimlane and table — through
// the table endpoint, so a workflow filter it cannot express is a filter the
// page never applies. It carried no workflow facet at all: the chip counted 1
// and the rows came back whole.
func TestIssueTableFilters_NarrowToWorkflow(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Table filter",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work",
				OwnerRole: "owner", IssuePolicy: "auto",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "end"},
		},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("table filter definition invalid: %v", err)
	}
	workflowID := createPublishedWorkflowForTest(t, "Table filter workflow", definition)
	hostID := createWorkflowHostForTest(t, "Table filter host")
	started := startWorkflowForTest(
		t, hostID, workflowID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
		}},
		"table-workflow-filter-start",
	)
	work := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
	for range 4 {
		worked, err := NewWorkflowMaterializer(testHandler).ProcessNext(ctx)
		if err != nil {
			t.Fatalf("materialise workflow tasks: %v", err)
		}
		if !worked {
			break
		}
	}
	nodeIssue := workflowNodeIssueForTest(t, work.ID)

	token := fmt.Sprintf("table-workflow-filter-%d", time.Now().UnixNano())
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

	rows := func(filters map[string]any) (int, []string) {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := newRequest(
			http.MethodPost,
			"/api/issues/table/rows?workspace_id="+testWorkspaceID,
			map[string]any{
				"query": map[string]any{
					"scope":   map[string]any{"kind": "workspace"},
					"filters": filters,
					"sort":    map[string]any{"field": "created_at", "direction": "desc"},
				},
				"group":     map[string]any{"kind": "none"},
				"hierarchy": map[string]any{"enabled": false},
				"page":      map[string]any{"limit": 100},
			},
		)
		testHandler.ListIssueTableRows(recorder, request)
		if recorder.Code != http.StatusOK {
			return recorder.Code, nil
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
		return recorder.Code, ids
	}
	contains := func(ids []string, want string) bool {
		for _, id := range ids {
			if id == want {
				return true
			}
		}
		return false
	}

	if status, ids := rows(map[string]any{}); status != http.StatusOK ||
		!contains(ids, nodeIssue) || !contains(ids, outsiderID) {
		t.Fatalf(
			"unfiltered table does not span both issues (status=%d); the assertions would pass vacuously: %v",
			status, ids,
		)
	}

	status, byWorkflow := rows(map[string]any{"workflow_id": workflowID})
	if status != http.StatusOK {
		t.Fatalf("workflow_id filter status = %d", status)
	}
	if !contains(byWorkflow, nodeIssue) || contains(byWorkflow, outsiderID) {
		t.Fatalf("workflow_id filter did not narrow to the run: %v", byWorkflow)
	}

	status, onlyWorkflow := rows(map[string]any{"workflow_issues_only": true})
	if status != http.StatusOK {
		t.Fatalf("workflow_issues_only status = %d", status)
	}
	if !contains(onlyWorkflow, nodeIssue) || contains(onlyWorkflow, outsiderID) {
		t.Fatalf("workflow_issues_only kept a non-workflow issue: %v", onlyWorkflow)
	}

	status, byActivity := rows(map[string]any{
		"workflow_instance_id":  started.Instance.ID,
		"workflow_activity_key": "work",
	})
	if status != http.StatusOK {
		t.Fatalf("activity filter status = %d", status)
	}
	if !contains(byActivity, nodeIssue) || contains(byActivity, outsiderID) {
		t.Fatalf("activity filter did not narrow to the node: %v", byActivity)
	}

	if status, _ := rows(map[string]any{"workflow_id": "not-a-uuid"}); status != http.StatusBadRequest {
		t.Fatalf("invalid workflow_id status = %d, want 400", status)
	}
}
