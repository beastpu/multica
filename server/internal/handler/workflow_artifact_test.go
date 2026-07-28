package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
)

// startArtifactWorkflow boots a one-activity workflow whose node requires a
// document artifact, and returns the instance and node ids.
func startArtifactWorkflow(t *testing.T, key string) (string, string) {
	t.Helper()
	ctx := context.Background()

	var hostID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Artifact host', 'todo', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create host issue: %v", err)
	}

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Artifact delivery",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true, AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "design", Kind: "activity", ActivityMode: "work", Name: "Design",
				OwnerRole: "owner", IssuePolicy: "none",
				Executor: workflowdomain.ExecutorDefinition{Strategies: []workflowdomain.ExecutorStrategy{
					{Kind: "fixed_role", Role: "owner"}, {Kind: "manual"},
				}},
				Artifacts: []workflowdomain.ArtifactRequirement{{
					Key: "design_doc", Name: "Technical design", Required: true,
				}},
				Completion: workflowdomain.CompletionDefinition{
					Mode: "manual", RequiredIssueOutcome: "none",
				},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "design"}, {From: "design", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}

	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template (workspace_id, name, status, created_by)
		VALUES ($1, 'Artifact test template', 'published', $2)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&templateID); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template_version (
			workspace_id, template_id, version, status, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, 'published', $3, 'test', $4, $4, now())
		RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create template version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_template SET latest_published_version_id = $1 WHERE id = $2
	`, versionID, templateID); err != nil {
		t.Fatalf("set published version: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodPost,
		"/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID,
		map[string]any{
			"template_id": templateID,
			"role_assignments": []map[string]any{{
				"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
			}},
			"idempotency_key": "artifact-test-" + key,
		},
	), "id", hostID)
	testHandler.StartIssueWorkflow(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("StartIssueWorkflow status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var started workflowInstanceDetailResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	node := findWorkflowNodeResponse(t, started.Nodes, "design", 1)
	return started.Instance.ID, node.ID
}

func submitArtifact(t *testing.T, nodeID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodPost,
		"/api/workflow-node-instances/"+nodeID+"/artifacts?workspace_id="+testWorkspaceID,
		body,
	), "nodeInstanceId", nodeID)
	testHandler.SubmitWorkflowArtifact(recorder, request)
	return recorder
}

func listNodeArtifacts(t *testing.T, nodeID string) []workflowArtifactResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodGet,
		"/api/workflow-node-instances/"+nodeID+"/artifacts?workspace_id="+testWorkspaceID,
		nil,
	), "nodeInstanceId", nodeID)
	testHandler.ListWorkflowNodeArtifacts(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ListWorkflowNodeArtifacts status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Artifacts []workflowArtifactResponse `json:"artifacts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode artifacts: %v", err)
	}
	return response.Artifacts
}

// Replacing an artifact must retire the old row rather than update it: an
// agent rewriting a sound document during rework should never be able to
// destroy what it replaced.
func TestWorkflowArtifactReplacementKeepsHistory(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	_, nodeID := startArtifactWorkflow(t, "history")

	if recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "design_doc", "content": "First draft.",
	}); recorder.Code != http.StatusOK {
		t.Fatalf("first submit status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "design_doc", "content": "Second draft.",
	}); recorder.Code != http.StatusOK {
		t.Fatalf("second submit status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	live := listNodeArtifacts(t, nodeID)
	if len(live) != 1 {
		t.Fatalf("live artifacts = %d, want exactly 1", len(live))
	}
	if live[0].Content != "Second draft." {
		t.Errorf("live content = %q, want the replacement", live[0].Content)
	}

	var total int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM workflow_artifact
		WHERE workflow_node_instance_id = $1 AND artifact_key = 'design_doc'
	`, nodeID).Scan(&total); err != nil {
		t.Fatalf("count artifact rows: %v", err)
	}
	if total != 2 {
		t.Errorf("stored rows = %d, want 2 (the replacement kept its predecessor)", total)
	}
}

func TestWorkflowArtifactRejectsUndeclaredKey(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	_, nodeID := startArtifactWorkflow(t, "undeclared")

	recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "not_declared", "content": "Something.",
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("submit status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkflowArtifactRejectsOversizedDocument(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	_, nodeID := startArtifactWorkflow(t, "oversized")

	recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "design_doc",
		"content":      strings.Repeat("x", maxWorkflowArtifactContentBytes+1),
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("submit status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "attachment") {
		t.Errorf("rejection should point at the attachment route, got %s", recorder.Body.String())
	}
}

// A document artifact carries content only. Accepting a stray url or
// attachment would leave two carriers disagreeing about what the artifact is.
func TestWorkflowArtifactRejectsMixedCarriers(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	_, nodeID := startArtifactWorkflow(t, "carriers")

	recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "design_doc",
		"content":      "Body.",
		"url":          "https://example.com/pr/1",
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("submit status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
	}
}

// A required artifact gates completion, and a rejection blocks exactly like a
// missing one — otherwise "rejected" would be advisory.
func TestWorkflowArtifactGatesNodeCompletion(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	_, nodeID := startArtifactWorkflow(t, "gate")

	attempt := 0
	complete := func() *httptest.ResponseRecorder {
		attempt++
		recorder := httptest.NewRecorder()
		request := withURLParam(newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+nodeID+"/complete?workspace_id="+testWorkspaceID,
			map[string]any{"idempotency_key": fmt.Sprintf("artifact-gate-complete-%d", attempt)},
		), "nodeInstanceId", nodeID)
		testHandler.CompleteWorkflowNode(recorder, request)
		return recorder
	}

	if recorder := complete(); recorder.Code == http.StatusOK {
		t.Fatal("node completed without its required artifact")
	}

	if recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "design_doc", "content": "The design.",
	}); recorder.Code != http.StatusOK {
		t.Fatalf("submit status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	artifacts := listNodeArtifacts(t, nodeID)
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(artifacts))
	}
	reviewRecorder := httptest.NewRecorder()
	reviewRequest := withURLParams(newRequest(
		http.MethodPost,
		"/api/workflow-node-instances/"+nodeID+"/artifacts/"+artifacts[0].ID+
			"/review?workspace_id="+testWorkspaceID,
		map[string]any{"status": "rejected", "comment": "Missing the migration path."},
	), "nodeInstanceId", nodeID, "artifactId", artifacts[0].ID)
	testHandler.ReviewWorkflowArtifact(reviewRecorder, reviewRequest)
	if reviewRecorder.Code != http.StatusOK {
		t.Fatalf("review status = %d, body = %s", reviewRecorder.Code, reviewRecorder.Body.String())
	}
	if recorder := complete(); recorder.Code == http.StatusOK {
		t.Fatal("node completed with a rejected required artifact")
	}

	if recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "design_doc", "content": "The design, with the migration path.",
	}); recorder.Code != http.StatusOK {
		t.Fatalf("resubmit status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	// Replacing the rejected artifact clears the rejection, because the
	// verdict belonged to content that is no longer live.
	if recorder := complete(); recorder.Code != http.StatusOK {
		t.Fatalf("complete status = %d after resubmission, body = %s", recorder.Code, recorder.Body.String())
	}
}
