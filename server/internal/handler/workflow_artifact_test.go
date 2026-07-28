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

// startLinkArtifactWorkflow boots a node whose required artifact is a link.
func startLinkArtifactWorkflow(t *testing.T, key string) (string, string) {
	t.Helper()
	instanceID, nodeID := startArtifactWorkflow(t, "link-"+key)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE workflow_node_instance
		SET definition_snapshot = jsonb_set(
			definition_snapshot::jsonb,
			'{artifacts}',
			'[{"key":"review_pr","name":"Review PR","kind":"link","required":true}]'::jsonb
		)::text::bytea
		WHERE id = $1
	`, nodeID); err != nil {
		// definition_snapshot is stored as jsonb in this schema; fall back to a
		// direct assignment when the cast above does not apply.
		if _, err2 := testPool.Exec(context.Background(), `
			UPDATE workflow_node_instance
			SET definition_snapshot = jsonb_set(
				definition_snapshot, '{artifacts}',
				'[{"key":"review_pr","name":"Review PR","kind":"link","required":true}]'::jsonb
			)
			WHERE id = $1
		`, nodeID); err2 != nil {
			t.Fatalf("rewrite node artifacts: %v / %v", err, err2)
		}
	}
	return instanceID, nodeID
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

// startHandoffWorkflow boots a two-activity run whose first node owes a handoff
// summary, so the gate and the upstream view can both be exercised.
func startHandoffWorkflow(t *testing.T, key string) (string, string, string) {
	t.Helper()
	ctx := context.Background()

	var hostID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Handoff host', 'todo', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create host issue: %v", err)
	}

	activity := func(nodeKey, name string, handoff bool) workflowdomain.NodeDefinition {
		return workflowdomain.NodeDefinition{
			Key: nodeKey, Kind: "activity", ActivityMode: "work", Name: name,
			OwnerRole: "owner", IssuePolicy: "none",
			Executor: workflowdomain.ExecutorDefinition{Strategies: []workflowdomain.ExecutorStrategy{
				{Kind: "fixed_role", Role: "owner"}, {Kind: "manual"},
			}},
			Completion: workflowdomain.CompletionDefinition{
				Mode: "manual", RequiredIssueOutcome: "none", HandoffRequired: handoff,
			},
		}
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Handoff delivery",
		AppliesTo:     workflowdomain.AppliesTo{Kind: "issue"},
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true, AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			activity("review", "Review", true),
			activity("design", "Design", false),
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "review"}, {From: "review", To: "design"},
			{From: "design", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}

	definitionJSON, _ := json.Marshal(definition)
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template (workspace_id, name, status, created_by)
		VALUES ($1, 'Handoff test template', 'published', $2) RETURNING id
	`, testWorkspaceID, testUserID).Scan(&templateID); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_template_version (
			workspace_id, template_id, version, status, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, 'published', $3, 'test', $4, $4, now()) RETURNING id
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
		http.MethodPost, "/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID,
		map[string]any{
			"template_id": templateID,
			"role_assignments": []map[string]any{{
				"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
			}},
			"idempotency_key": "handoff-test-" + key,
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
	review := findWorkflowNodeResponse(t, started.Nodes, "review", 1)
	design := findWorkflowNodeResponse(t, started.Nodes, "design", 1)
	return started.Instance.ID, review.ID, design.ID
}

func submitHandoff(t *testing.T, nodeID, summary, key string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodPost,
		"/api/workflow-node-instances/"+nodeID+"/submissions?workspace_id="+testWorkspaceID,
		map[string]any{"summary": summary, "idempotency_key": key},
	), "nodeInstanceId", nodeID)
	testHandler.CreateWorkflowNodeSubmission(recorder, request)
	return recorder
}

// A node that owes a conclusion is not finished without one — otherwise the
// next node inherits nothing and the requirement is decorative.
func TestWorkflowHandoffSummaryGatesCompletion(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	_, reviewID, _ := startHandoffWorkflow(t, "gate")

	complete := func(n int) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := withURLParam(newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+reviewID+"/complete?workspace_id="+testWorkspaceID,
			map[string]any{"idempotency_key": fmt.Sprintf("handoff-gate-%d", n)},
		), "nodeInstanceId", reviewID)
		testHandler.CompleteWorkflowNode(recorder, request)
		return recorder
	}

	if recorder := complete(1); recorder.Code == http.StatusOK {
		t.Fatal("node completed without its handoff summary")
	}
	if recorder := submitHandoff(
		t, reviewID, "Scope confirmed; first release is linear only.", "handoff-gate-summary",
	); recorder.Code != http.StatusCreated {
		t.Fatalf("submit handoff status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder := complete(2); recorder.Code != http.StatusOK {
		t.Fatalf("complete status = %d after handoff, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestWorkflowHandoffSummaryRejectsOverlongText(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	_, reviewID, _ := startHandoffWorkflow(t, "overlong")

	recorder := submitHandoff(
		t, reviewID,
		strings.Repeat("借", workflowdomain.MaxHandoffSummaryChars+1),
		"handoff-overlong",
	)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("submit status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
	}
	// The cap counts characters, not bytes: a multi-byte summary well under the
	// limit in runes must not be rejected for its encoding.
	if recorder := submitHandoff(
		t, reviewID, strings.Repeat("借", 100), "handoff-multibyte",
	); recorder.Code != http.StatusCreated {
		t.Fatalf("multi-byte summary rejected: %d %s", recorder.Code, recorder.Body.String())
	}
}

// Downstream sees its direct predecessor's conclusion and artifact index, and
// nothing else — the index carries no bodies.
func TestWorkflowUpstreamReturnsPredecessorHandoff(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	_, reviewID, designID := startHandoffWorkflow(t, "upstream")

	if recorder := submitHandoff(
		t, reviewID, "Scope confirmed; dynamic decomposition deferred.", "handoff-upstream",
	); recorder.Code != http.StatusCreated {
		t.Fatalf("submit handoff status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodGet,
		"/api/workflow-node-instances/"+designID+"/upstream?workspace_id="+testWorkspaceID,
		nil,
	), "nodeInstanceId", designID)
	testHandler.GetWorkflowNodeUpstream(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("upstream status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Upstream []struct {
			NodeKey   string `json:"node_key"`
			Summary   string `json:"summary"`
			Artifacts []struct {
				ID string `json:"id"`
			} `json:"artifacts"`
		} `json:"upstream"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode upstream: %v", err)
	}
	if len(response.Upstream) != 1 {
		t.Fatalf("upstream entries = %d, want exactly the direct predecessor", len(response.Upstream))
	}
	if response.Upstream[0].NodeKey != "review" {
		t.Errorf("upstream node = %q, want review", response.Upstream[0].NodeKey)
	}
	if !strings.Contains(response.Upstream[0].Summary, "Scope confirmed") {
		t.Errorf("upstream summary = %q, want the submitted handoff", response.Upstream[0].Summary)
	}
	if !strings.Contains(recorder.Body.String(), `"artifacts"`) {
		t.Error("upstream response should carry an artifact index")
	}
	if strings.Contains(recorder.Body.String(), `"content"`) {
		t.Error("upstream index must not carry artifact bodies")
	}
}

// A node needing a verdict but declaring no schema gets a submission
// synthesised for it, carrying a canned summary. That record must not satisfy
// the handoff gate: downstream would inherit a conclusion the platform wrote,
// which is exactly what requiring a handoff is meant to prevent.
func TestWorkflowHandoffGateRejectsSystemAuthoredSummary(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	_, reviewID, _ := startHandoffWorkflow(t, "system-summary")

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO workflow_node_submission (
			workspace_id, workflow_instance_id, workflow_node_instance_id,
			revision, status, payload, summary, evidence, submitted_by_type,
			schema_version
		)
		SELECT workspace_id, workflow_instance_id, id, 1, 'valid', '{}'::jsonb,
		       'All required issues are done', '[]'::jsonb, 'system', 1
		FROM workflow_node_instance WHERE id = $1
	`, reviewID); err != nil {
		t.Fatalf("insert system submission: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE workflow_node_instance SET latest_submission_id = (
			SELECT id FROM workflow_node_submission
			WHERE workflow_node_instance_id = $1 ORDER BY revision DESC LIMIT 1
		) WHERE id = $1
	`, reviewID); err != nil {
		t.Fatalf("link system submission: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodPost,
		"/api/workflow-node-instances/"+reviewID+"/complete?workspace_id="+testWorkspaceID,
		map[string]any{"idempotency_key": "handoff-system-summary"},
	), "nodeInstanceId", reviewID)
	testHandler.CompleteWorkflowNode(recorder, request)
	if recorder.Code == http.StatusOK {
		t.Fatal("node completed on a summary the platform wrote for it")
	}
}

// A link artifact is agent-submitted and later rendered as a clickable
// address, so its scheme is an injection boundary.
func TestWorkflowArtifactRejectsNonBrowsableLink(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	_, nodeID := startLinkArtifactWorkflow(t, "scheme")

	for _, hostile := range []string{
		"javascript:alert(1)",
		"data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==",
		"file:///etc/passwd",
		"not a url at all",
	} {
		recorder := submitArtifact(t, nodeID, map[string]any{
			"artifact_key": "review_pr", "url": hostile,
		})
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("url %q accepted with status %d", hostile, recorder.Code)
		}
	}
	if recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "review_pr", "url": "https://git.example.com/pr/7",
	}); recorder.Code != http.StatusOK {
		t.Fatalf("https link rejected: %d %s", recorder.Code, recorder.Body.String())
	}
}

// An api verdict must fail closed. An endpoint that times out, errors, or
// answers in an unrecognised shape has approved nothing, and treating silence
// as approval would let an unreachable check wave work through.
func TestAPIWorkflowVerdictFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "a clean pass is honoured",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"result":"pass","reason":"CI green"}`))
			},
			want: "pass",
		},
		{
			name: "an explicit fail is honoured",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"result":"fail","reason":"coverage dropped"}`))
			},
			want: "fail",
		},
		{
			name: "a server error blocks",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			want: "blocked",
		},
		{
			name: "an unreadable body blocks",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("not json"))
			},
			want: "blocked",
		},
		{
			name: "an unknown result blocks",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"result":"maybe"}`))
			},
			want: "blocked",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			got, reason := testHandler.evaluateAPIWorkflowVerdict(
				context.Background(), server.URL,
			)
			if got != test.want {
				t.Errorf("evaluateAPIWorkflowVerdict() = %q (%s), want %q", got, reason, test.want)
			}
			if reason == "" {
				t.Error("a verdict must carry a reason")
			}
		})
	}
}

func TestAPIWorkflowVerdictBlocksOnUnreachableEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close() // Nothing is listening now.

	got, reason := testHandler.evaluateAPIWorkflowVerdict(context.Background(), url)
	if got != "blocked" {
		t.Errorf("evaluateAPIWorkflowVerdict() = %q (%s), want blocked", got, reason)
	}
}
