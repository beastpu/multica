package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
)

// deletableWorkflowDefinition is a one-activity workflow whose node carries an
// issue, so deleting it has real children to clean up and a real issue to spare.
func deletableWorkflowDefinition(name string) workflowdomain.Definition {
	return workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          name,
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
}

func cancelWorkflowForTest(t *testing.T, instanceID string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(http.MethodPost,
			"/api/workflow-instances/"+instanceID+"/cancel?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason":          "retiring the workflow",
				"idempotency_key": "workflow-delete-cancel-" + instanceID,
			},
		),
		"instanceId", instanceID,
	)
	testHandler.CancelWorkflowInstance(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("CancelWorkflowInstance status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func deleteWorkflowForTest(t *testing.T, workflowID string) (int, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(
			http.MethodDelete,
			"/api/workflows/"+workflowID+"?workspace_id="+testWorkspaceID,
			nil,
		),
		"id", workflowID,
	)
	testHandler.DeleteWorkflow(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// A finished workflow can be deleted outright. It used to be refused for having
// runs at all, which left archiving as the only way to retire one — a one-way
// hide that never removed anything.
func TestDeleteWorkflow_RemovesFinishedRunsAndSparesTheirIssues(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := deletableWorkflowDefinition("Deletable")
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("deletable definition invalid: %v", err)
	}
	workflowID := createPublishedWorkflowForTest(t, "Deletable workflow", definition)
	hostID := createWorkflowHostForTest(t, "Deletable host")
	started := startWorkflowForTest(
		t, hostID, workflowID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
		}},
		"workflow-delete-start",
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

	// A run still in flight blocks deletion: the reason names the state so the
	// author knows to finish or cancel it, rather than being told to archive.
	status, body := deleteWorkflowForTest(t, workflowID)
	if status != http.StatusConflict {
		t.Fatalf("delete with a live run status = %d, body = %s", status, body)
	}

	cancelWorkflowForTest(t, started.Instance.ID)

	status, body = deleteWorkflowForTest(t, workflowID)
	if status != http.StatusNoContent {
		t.Fatalf("delete after cancelling status = %d, body = %s", status, body)
	}

	// The workflow, its versions and its whole run history are gone.
	var remaining int
	if err := testPool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM workflow WHERE id = $1) +
			(SELECT count(*) FROM workflow_version WHERE workflow_id = $1) +
			(SELECT count(*) FROM workflow_instance WHERE workflow_id = $1) +
			(SELECT count(*) FROM workflow_node_instance WHERE workflow_instance_id = $2) +
			(SELECT count(*) FROM workflow_node_task WHERE workflow_instance_id = $2) +
			(SELECT count(*) FROM workflow_event WHERE workflow_instance_id = $2) +
			(SELECT count(*) FROM workflow_instance_role_assignment
			   WHERE workflow_instance_id = $2)
	`, workflowID, started.Instance.ID).Scan(&remaining); err != nil {
		t.Fatalf("count workflow rows after delete: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("workflow domain rows survived the delete: %d", remaining)
	}

	// The issue the run created is someone's work, not the process's — it stays,
	// unbound from the workflow that no longer exists.
	var issueCount int
	var originType *string
	var originID *string
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) OVER (), origin_type, origin_id::text
		FROM issue WHERE id = $1
	`, nodeIssue).Scan(&issueCount, &originType, &originID); err != nil {
		t.Fatalf("load node issue after delete: %v", err)
	}
	if issueCount != 1 {
		t.Fatalf("node issue was deleted with its workflow")
	}
	if originType != nil || originID != nil {
		t.Fatalf(
			"node issue still points at the deleted workflow: origin_type=%v origin_id=%v",
			originType, originID,
		)
	}
	var metaWorkflow *string
	if err := testPool.QueryRow(ctx, `
		SELECT metadata->'workflow'->>'instance_id' FROM issue WHERE id = $1
	`, nodeIssue).Scan(&metaWorkflow); err != nil {
		t.Fatalf("load node issue metadata: %v", err)
	}
	if metaWorkflow != nil {
		t.Fatalf("node issue metadata still names the deleted run: %v", *metaWorkflow)
	}

	// The host issue is a workspace issue that predates the run; it survives too.
	var hostCount int
	if err := testPool.QueryRow(
		ctx, `SELECT count(*) FROM issue WHERE id = $1`, hostID,
	).Scan(&hostCount); err != nil {
		t.Fatalf("count host issue: %v", err)
	}
	if hostCount != 1 {
		t.Fatalf("host issue was deleted with its workflow")
	}

	// Deleting twice is not an error the second caller has to interpret.
	if status, _ := deleteWorkflowForTest(t, workflowID); status != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", status)
	}
}

// A workflow nothing ever ran stays deletable — the case the old guard covered.
func TestDeleteWorkflow_NeverRun(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)

	workflowID := createPublishedWorkflowForTest(
		t, "Never run workflow", deletableWorkflowDefinition("Never run"),
	)
	if status, body := deleteWorkflowForTest(t, workflowID); status != http.StatusNoContent {
		t.Fatalf("delete never-run workflow status = %d, body = %s", status, body)
	}
}

// Archiving is gone: the route no longer exists on the handler surface.
func TestArchiveWorkflow_RouteIsRetired(t *testing.T) {
	var response struct {
		Workflows []map[string]any `json:"workflows"`
	}
	recorder := httptest.NewRecorder()
	testHandler.ListWorkflows(recorder, newRequest(
		http.MethodGet, "/api/workflows?workspace_id="+testWorkspaceID+"&status=archived", nil,
	))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=archived is no longer a filter: got %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	testHandler.ListWorkflows(recorder, newRequest(
		http.MethodGet, "/api/workflows?workspace_id="+testWorkspaceID, nil,
	))
	if recorder.Code != http.StatusOK {
		t.Fatalf("ListWorkflows status = %d", recorder.Code)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode workflow list: %v", err)
	}
	for _, workflow := range response.Workflows {
		if _, exists := workflow["status"]; exists {
			t.Fatalf("workflow response still carries a status field: %v", workflow)
		}
		if _, exists := workflow["archived_at"]; exists {
			t.Fatalf("workflow response still carries archived_at: %v", workflow)
		}
	}
}
