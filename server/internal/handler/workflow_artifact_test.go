package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true, AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "design", Kind: "activity", Name: "Design",
				OwnerRole: "owner", IssuePolicy: "none",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
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
		INSERT INTO workflow (workspace_id, name, status, created_by)
		VALUES ($1, $3, 'published', $2)
		RETURNING id
	`, testWorkspaceID, testUserID, "Artifact test template "+t.Name()).Scan(&templateID); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_version (
			workspace_id, workflow_id, version, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, $3, 'test', $4, $4, now())
		RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create template version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow SET latest_published_version_id = $1 WHERE id = $2
	`, versionID, templateID); err != nil {
		t.Fatalf("set published version: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodPost,
		"/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID,
		map[string]any{
			"workflow_id": templateID,
			"role_assignments": []map[string]any{{
				"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
			}},
			"idempotency_key": "artifact-test-" + key + "-" + t.Name(),
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

func configureHumanCriticArtifactWorkflow(t *testing.T, instanceID, nodeID string) {
	t.Helper()
	ctx := context.Background()
	var snapshot []byte
	if err := testPool.QueryRow(ctx, `
		SELECT definition_snapshot FROM workflow_node_instance WHERE id = $1
	`, nodeID).Scan(&snapshot); err != nil {
		t.Fatalf("load node definition snapshot: %v", err)
	}
	var definition workflowdomain.NodeDefinition
	if err := json.Unmarshal(snapshot, &definition); err != nil {
		t.Fatalf("decode node definition snapshot: %v", err)
	}
	definition.Reviewer = &workflowdomain.ReviewerDefinition{
		Kind: "owner", Required: true,
	}
	definition.Completion.Mode = "automatic"
	snapshot, _ = json.Marshal(definition)
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_node_instance SET definition_snapshot = $2 WHERE id = $1
	`, nodeID, snapshot); err != nil {
		t.Fatalf("configure Human Critic node: %v", err)
	}
	var versionID string
	var versionDefinitionJSON []byte
	if err := testPool.QueryRow(ctx, `
		SELECT version.id, version.definition
		FROM workflow_instance instance
		JOIN workflow_version version ON version.id = instance.workflow_version_id
		WHERE instance.id = $1
	`, instanceID).Scan(&versionID, &versionDefinitionJSON); err != nil {
		t.Fatalf("load workflow version definition: %v", err)
	}
	var versionDefinition workflowdomain.Definition
	if err := json.Unmarshal(versionDefinitionJSON, &versionDefinition); err != nil {
		t.Fatalf("decode workflow version definition: %v", err)
	}
	for index := range versionDefinition.Nodes {
		if versionDefinition.Nodes[index].Key != "design" {
			continue
		}
		versionDefinition.Nodes[index].Reviewer = definition.Reviewer
		versionDefinition.Nodes[index].Completion = definition.Completion
	}
	versionDefinitionJSON, _ = json.Marshal(versionDefinition)
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_version SET definition = $2 WHERE id = $1
	`, versionID, versionDefinitionJSON); err != nil {
		t.Fatalf("configure Human Critic workflow version: %v", err)
	}
}

// A Human Critic's node verdict is the atomic review action for the delivery:
// the artifact statuses and the node verdict must describe the same snapshot.
func TestWorkflowHumanCriticVerdictReviewsCurrentArtifacts(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	instanceID, nodeID := startArtifactWorkflow(t, "human-critic")
	configureHumanCriticArtifactWorkflow(t, instanceID, nodeID)
	ctx := context.Background()

	if recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "design_doc", "content": "The reviewed design.",
	}); recorder.Code != http.StatusOK {
		t.Fatalf("submit status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	postWorkflowSubmissionPayload(
		t, nodeID, "human-critic-delivery", map[string]any{},
	)
	if node := latestWorkflowNodeForTest(t, instanceID, "design"); node.Status != "in_review" {
		t.Fatalf("node status = %q, want in_review before Critic verdict", node.Status)
	}
	if recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "design_doc", "content": "A hidden replacement.",
	}); recorder.Code != http.StatusConflict {
		t.Fatalf(
			"replace during review status = %d, want 409; body = %s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	postWorkflowVerdict(t, nodeID, "human-critic-verdict")

	artifacts := listNodeArtifacts(t, nodeID)
	if len(artifacts) != 1 || artifacts[0].ReviewStatus != "approved" {
		t.Fatalf("Human Critic artifact result = %#v, want one approved artifact", artifacts)
	}
	verdicts, err := testHandler.Queries.ListWorkflowVerdicts(
		ctx,
		db.ListWorkflowVerdictsParams{
			WorkflowNodeInstanceID: parseUUID(nodeID),
			WorkspaceID:            parseUUID(testWorkspaceID),
		},
	)
	if err != nil || len(verdicts) == 0 {
		t.Fatalf("load Human Critic verdicts: count=%d err=%v", len(verdicts), err)
	}
	var basis struct {
		ArtifactIDs []string `json:"artifact_ids"`
	}
	if err := json.Unmarshal(verdicts[0].Basis, &basis); err != nil {
		t.Fatalf("decode Human Critic verdict basis: %v", err)
	}
	if len(basis.ArtifactIDs) != 1 || basis.ArtifactIDs[0] != artifacts[0].ID {
		t.Fatalf("Human Critic artifact snapshot = %#v", basis.ArtifactIDs)
	}
	instance, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{
			ID: parseUUID(instanceID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || instance.Status != "completed" {
		t.Fatalf("instance status = %q, want completed, err=%v", instance.Status, err)
	}
}

func TestWorkflowHumanCriticRejectionStartsRework(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	instanceID, nodeID := startArtifactWorkflow(t, "human-critic-rework")
	configureHumanCriticArtifactWorkflow(t, instanceID, nodeID)
	ctx := context.Background()

	if recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "design_doc", "content": "A design that needs revision.",
	}); recorder.Code != http.StatusOK {
		t.Fatalf("submit status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	postWorkflowSubmissionPayload(
		t, nodeID, "human-critic-rework-delivery", map[string]any{},
	)
	postWorkflowVerdictResult(
		t, nodeID, "fail", "Add the rollback design.", "human-critic-rework-verdict",
	)

	rejectedArtifacts := listNodeArtifacts(t, nodeID)
	if len(rejectedArtifacts) != 1 || rejectedArtifacts[0].ReviewStatus != "rejected" {
		t.Fatalf("rejected artifacts = %#v, want one rejected artifact", rejectedArtifacts)
	}
	previous, err := testHandler.Queries.GetWorkflowNodeInstanceInWorkspace(
		ctx,
		db.GetWorkflowNodeInstanceInWorkspaceParams{
			ID: parseUUID(nodeID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || previous.Status != "superseded" {
		t.Fatalf("rejected node status = %q, want superseded, err=%v", previous.Status, err)
	}
	rework := latestWorkflowNodeForTest(t, instanceID, "design")
	if rework.Attempt != 2 || uuidToString(rework.ID) == nodeID || rework.Status != "active" {
		t.Fatalf("rework node = %#v, want a new active attempt 2", rework)
	}
	instance, err := testHandler.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{
			ID: parseUUID(instanceID), WorkspaceID: parseUUID(testWorkspaceID),
		},
	)
	if err != nil || instance.Status != "running" {
		t.Fatalf("rework instance status = %q, want running, err=%v", instance.Status, err)
	}
}

// startHandoffWorkflow boots a two-activity run so the upstream view has a
// predecessor whose conclusion it can carry.
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

	activity := func(nodeKey, name string) workflowdomain.NodeDefinition {
		return workflowdomain.NodeDefinition{
			Key: nodeKey, Kind: "activity", Name: name,
			OwnerRole: "owner", IssuePolicy: "none",
			Executor: &workflowdomain.ExecutorDefinition{
				Kind: "role", Role: "owner",
				Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
			},
			Completion: workflowdomain.CompletionDefinition{
				Mode: "manual", RequiredIssueOutcome: "none",
			},
		}
	}
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Handoff delivery",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true, AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			activity("review", "Review"),
			activity("design", "Design"),
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
		INSERT INTO workflow (workspace_id, name, status, created_by)
		VALUES ($1, $3, 'published', $2) RETURNING id
	`, testWorkspaceID, testUserID, "Handoff test template "+t.Name()).Scan(&templateID); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_version (
			workspace_id, workflow_id, version, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, $3, 'test', $4, $4, now()) RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create template version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow SET latest_published_version_id = $1 WHERE id = $2
	`, versionID, templateID); err != nil {
		t.Fatalf("set published version: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodPost, "/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID,
		map[string]any{
			"workflow_id": templateID,
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

// A predecessor that ran but whose executor wrote no conclusion used to hand
// downstream nothing at all. Its execution output and its issue are the only
// record of what happened, so both travel with the upstream entry — the output
// in its own field, never merged into summary, because the handoff gate treats
// an authored conclusion and a platform extract as different things.
func TestWorkflowUpstreamFallsBackToWorkerOutputAndIssues(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	instanceID, reviewID, designID := startHandoffWorkflow(t, "upstream-fallback")

	var agentID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent WHERE workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("find seeded agent: %v", err)
	}

	var executionTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_node_task (
			workspace_id, workflow_instance_id, workflow_node_instance_id,
			task_key, source, required, materialization_status, created_by_type
		) VALUES ($1, $2, $3, 'run', 'execution', true, 'materialized', 'system')
		RETURNING id
	`, testWorkspaceID, instanceID, reviewID).Scan(&executionTaskID); err != nil {
		t.Fatalf("create execution task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, status, priority, workflow_node_task_id, result
		) VALUES ($1, $2, 'completed', 0, $3, $4)
	`, agentID, testRuntimeID, executionTaskID,
		`{"output":"Reviewed the pricing rules; two edge cases stay open."}`,
	); err != nil {
		t.Fatalf("create completed agent task: %v", err)
	}

	var workTaskID, workIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_node_task (
			workspace_id, workflow_instance_id, workflow_node_instance_id,
			task_key, source, required, materialization_status, created_by_type
		) VALUES ($1, $2, $3, 'work', 'template', true, 'materialized', 'system')
		RETURNING id
	`, testWorkspaceID, instanceID, reviewID).Scan(&workTaskID); err != nil {
		t.Fatalf("create work task: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id,
			number, position, origin_type, origin_id
		) VALUES ($1, 'Upstream review work', 'done', 'none', 'member', $2, $3, 0, 'workflow', $4)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t), workTaskID).Scan(&workIssueID); err != nil {
		t.Fatalf("create work issue: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow_node_task SET issue_id = $1 WHERE id = $2
	`, workIssueID, workTaskID); err != nil {
		t.Fatalf("bind work issue: %v", err)
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
			NodeKey      string   `json:"node_key"`
			Summary      string   `json:"summary"`
			WorkerOutput string   `json:"worker_output"`
			Issues       []string `json:"issues"`
		} `json:"upstream"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode upstream: %v", err)
	}
	if len(response.Upstream) != 1 {
		t.Fatalf("upstream entries = %d, want exactly the direct predecessor", len(response.Upstream))
	}
	entry := response.Upstream[0]
	if entry.Summary != "" {
		t.Errorf("summary = %q, want empty: nobody wrote a conclusion", entry.Summary)
	}
	if !strings.Contains(entry.WorkerOutput, "two edge cases stay open") {
		t.Errorf("worker_output = %q, want the completed execution's output", entry.WorkerOutput)
	}
	if len(entry.Issues) != 1 {
		t.Fatalf("issues = %v, want the predecessor's issue", entry.Issues)
	}
	if !strings.HasSuffix(entry.Issues[0], "-"+strconv.Itoa(workIssueNumber(t, workIssueID))) {
		t.Errorf("issue identifier = %q, want the work issue's identifier", entry.Issues[0])
	}
}

// Once the executor writes a conclusion, that is the handoff. Carrying the raw
// transcript alongside it would bury the conclusion in a downstream brief that
// has its own instructions to fit.
// A node that needs a verdict but declares no schema gets a submission
// synthesised for it, carrying a canned summary. Downstream must never read
// that as the executor's conclusion — the summary field is reserved for text a
// person or agent actually wrote, and the platform's record travels only
// through the worker-output fallback.
func TestWorkflowUpstreamHidesSystemAuthoredSummary(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	_, reviewID, designID := startHandoffWorkflow(t, "system-upstream")

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
			Summary string `json:"summary"`
		} `json:"upstream"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode upstream: %v", err)
	}
	if len(response.Upstream) != 1 {
		t.Fatalf("upstream entries = %d, want exactly the direct predecessor", len(response.Upstream))
	}
	if response.Upstream[0].Summary != "" {
		t.Errorf("summary = %q, want empty: the platform wrote it, not the executor",
			response.Upstream[0].Summary)
	}
}

func TestWorkflowUpstreamOmitsWorkerOutputOnceHandoffExists(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	instanceID, reviewID, designID := startHandoffWorkflow(t, "upstream-authored")

	var agentID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent WHERE workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("find seeded agent: %v", err)
	}
	var executionTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_node_task (
			workspace_id, workflow_instance_id, workflow_node_instance_id,
			task_key, source, required, materialization_status, created_by_type
		) VALUES ($1, $2, $3, 'run', 'execution', true, 'materialized', 'system')
		RETURNING id
	`, testWorkspaceID, instanceID, reviewID).Scan(&executionTaskID); err != nil {
		t.Fatalf("create execution task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (
			agent_id, runtime_id, status, priority, workflow_node_task_id, result
		) VALUES ($1, $2, 'completed', 0, $3, $4)
	`, agentID, testRuntimeID, executionTaskID,
		`{"output":"verbose transcript nobody downstream needs"}`,
	); err != nil {
		t.Fatalf("create completed agent task: %v", err)
	}

	if recorder := submitHandoff(
		t, reviewID, "Pricing rules reviewed; two edge cases deferred.", "handoff-authored",
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
			Summary      string `json:"summary"`
			WorkerOutput string `json:"worker_output"`
		} `json:"upstream"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode upstream: %v", err)
	}
	if len(response.Upstream) != 1 {
		t.Fatalf("upstream entries = %d, want exactly the direct predecessor", len(response.Upstream))
	}
	if !strings.Contains(response.Upstream[0].Summary, "two edge cases deferred") {
		t.Errorf("summary = %q, want the authored handoff", response.Upstream[0].Summary)
	}
	if response.Upstream[0].WorkerOutput != "" {
		t.Errorf("worker_output = %q, want empty once a conclusion exists",
			response.Upstream[0].WorkerOutput)
	}
}

func workIssueNumber(t *testing.T, issueID string) int {
	t.Helper()
	var number int
	if err := testPool.QueryRow(context.Background(), `
		SELECT number FROM issue WHERE id = $1
	`, issueID).Scan(&number); err != nil {
		t.Fatalf("read issue number: %v", err)
	}
	return number
}

// Most executors meet the work as a node child issue — from a notification, the
// issue list, or a phone — never as the run. What upstream concluded is not
// written onto that issue and must not be, so the issue has to be able to read
// it.
func TestIssueWorkflowNodeServesUpstreamConclusion(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()
	instanceID, reviewID, _ := startHandoffWorkflow(t, "issue-node-context")

	if recorder := submitHandoff(
		t, reviewID, "Scope confirmed; pricing edge cases stay open.", "issue-node-context",
	); recorder.Code != http.StatusCreated {
		t.Fatalf("submit handoff status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id,
			number, position, metadata
		) VALUES ($1, 'Design work', 'todo', 'none', 'member', $2, $3, 0,
			jsonb_build_object('workflow', jsonb_build_object(
				'instance_id', $4::text, 'node_key', 'design', 'host_issue', 'MUL-1'
			)))
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t), instanceID).Scan(&issueID); err != nil {
		t.Fatalf("create node child issue: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodGet,
		"/api/issues/"+issueID+"/workflow-node?workspace_id="+testWorkspaceID,
		nil,
	), "id", issueID)
	testHandler.GetIssueWorkflowNode(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("workflow-node status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		NodeKey  string `json:"node_key"`
		Upstream []struct {
			NodeKey string `json:"node_key"`
			Summary string `json:"summary"`
		} `json:"upstream"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode workflow-node: %v", err)
	}
	if response.NodeKey != "design" {
		t.Errorf("node_key = %q, want the node the issue belongs to", response.NodeKey)
	}
	if len(response.Upstream) != 1 || response.Upstream[0].NodeKey != "review" {
		t.Fatalf("upstream = %+v, want the direct predecessor", response.Upstream)
	}
	if !strings.Contains(response.Upstream[0].Summary, "pricing edge cases") {
		t.Errorf("upstream summary = %q, want the predecessor's conclusion",
			response.Upstream[0].Summary)
	}
}

// An ordinary issue is not a node of anything, and the panel that asks must be
// able to tell that apart from a failure.
func TestIssueWorkflowNodeIsNotFoundForOrdinaryIssues(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)

	var issueID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Ordinary issue', 'todo', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := withURLParam(newRequest(
		http.MethodGet,
		"/api/issues/"+issueID+"/workflow-node?workspace_id="+testWorkspaceID,
		nil,
	), "id", issueID)
	testHandler.GetIssueWorkflowNode(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", recorder.Code, recorder.Body.String())
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

// Delivering an artifact and making that delivery visible to people used to be
// two actions: attach the file to a comment so humans could read it, then
// submit the same content so the node could advance. Forgetting the second one
// silently blocked the run; doing both duplicated the content. One action now
// leaves the record on the issue too.
func TestWorkflowArtifactSubmitLeavesIssueTrace(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	instanceID, nodeID := startArtifactWorkflow(t, "design_doc")

	var hostID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT host_issue_id FROM workflow_instance WHERE id = $1`, instanceID,
	).Scan(&hostID); err != nil {
		t.Fatalf("resolve host issue: %v", err)
	}

	recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "design_doc",
		"content":      "Interfaces, data shapes, and how it was verified.",
		"issue_id":     hostID,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("submit: got %d, body %s", recorder.Code, recorder.Body.String())
	}

	var content, authorType string
	if err := testPool.QueryRow(context.Background(), `
		SELECT content, author_type FROM comment
		WHERE issue_id = $1 ORDER BY created_at DESC LIMIT 1
	`, hostID).Scan(&content, &authorType); err != nil {
		t.Fatalf("read trace comment: %v", err)
	}
	if authorType != "system" {
		t.Errorf("author_type = %q, want system", authorType)
	}
	for _, want := range []string{"design_doc", "Interfaces, data shapes"} {
		if !strings.Contains(content, want) {
			t.Errorf("trace missing %q:\n%s", want, content)
		}
	}
}

// The artifact is what gates the node and it is already committed by then, so
// a submission without an issue must still succeed — older CLIs send none.
func TestWorkflowArtifactSubmitWithoutIssueStillSucceeds(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	instanceID, nodeID := startArtifactWorkflow(t, "design_doc")

	var hostID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT host_issue_id FROM workflow_instance WHERE id = $1`, instanceID,
	).Scan(&hostID); err != nil {
		t.Fatalf("resolve host issue: %v", err)
	}

	recorder := submitArtifact(t, nodeID, map[string]any{
		"artifact_key": "design_doc",
		"content":      "No issue supplied.",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("submit: got %d, body %s", recorder.Code, recorder.Body.String())
	}

	var comments int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM comment WHERE issue_id = $1`, hostID,
	).Scan(&comments); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if comments != 0 {
		t.Errorf("comments = %d, want 0 when no issue was supplied", comments)
	}
}

// A node that defers its issues to runtime used to activate and complete in
// the same breath: with no tasks, "all required issues are done" is true of
// the empty set, so the work the node stood for silently never happened. This
// is the shape a user hit by switching an activity to runtime decomposition —
// the editor cleared its issue templates, and the node stopped meaning
// anything.
func TestDynamicNodeWaitsToBeDecomposed(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	ctx := context.Background()

	var hostID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id, number, position
		) VALUES ($1, 'Decomposition host', 'todo', 'none', 'member', $2, $3, 0)
		RETURNING id
	`, testWorkspaceID, testUserID, nextWorkspaceIssueNumber(t)).Scan(&hostID); err != nil {
		t.Fatalf("create host issue: %v", err)
	}

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Decomposition " + t.Name(),
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity",
				Name: "Work", OwnerRole: "owner",
				// Runtime decomposition with an automatic completion mode —
				// exactly what the editor produced before this was retired.
				IssuePolicy: "dynamic",
				Executor: &workflowdomain.ExecutorDefinition{
					Kind: "role", Role: "owner",
					Fallback: &workflowdomain.ExecutorDefinition{Kind: "manual"},
				},
				Completion: workflowdomain.CompletionDefinition{Mode: "automatic"},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"}, {From: "work", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}

	definitionJSON, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}
	var templateID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow (workspace_id, name, status, created_by)
		VALUES ($1, $3, 'published', $2)
		RETURNING id
	`, testWorkspaceID, testUserID, "Decomposition "+t.Name()).Scan(&templateID); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workflow_version (
			workspace_id, workflow_id, version, definition,
			definition_checksum, created_by, published_by, published_at
		) VALUES ($1, $2, 1, $3, 'test', $4, $4, now())
		RETURNING id
	`, testWorkspaceID, templateID, definitionJSON, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("create template version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE workflow SET latest_published_version_id = $1 WHERE id = $2
	`, versionID, templateID); err != nil {
		t.Fatalf("set published version: %v", err)
	}

	recorder := httptest.NewRecorder()
	testHandler.StartIssueWorkflow(recorder, withURLParam(newRequest(
		http.MethodPost,
		"/api/issues/"+hostID+"/workflow?workspace_id="+testWorkspaceID,
		map[string]any{
			"workflow_id": templateID,
			"role_assignments": []map[string]any{{
				"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
			}},
			"idempotency_key": "decomposition-" + t.Name(),
		},
	), "id", hostID))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("start workflow: got %d, body %s", recorder.Code, recorder.Body.String())
	}

	var started workflowInstanceDetailResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	node := findWorkflowNodeResponse(t, started.Nodes, "work", 1)
	if node.Status == "completed" {
		t.Fatal("node completed with nothing to do; it must wait to be decomposed")
	}

	// The start response predates the first completion check, so the reasons
	// are only written once the instance is reconciled.
	reconcileWorkflowForTest(t, started.Instance.ID, "decomposition-"+t.Name())

	var reasonsJSON []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT waiting_reasons FROM workflow_node_instance WHERE id = $1`, node.ID,
	).Scan(&reasonsJSON); err != nil {
		t.Fatalf("read waiting reasons: %v", err)
	}

	var reasons []workflowdomain.WaitingReason
	if err := json.Unmarshal(reasonsJSON, &reasons); err != nil {
		t.Fatalf("decode waiting reasons: %v", err)
	}
	var sawReason bool
	for _, reason := range reasons {
		if reason.Code == "awaiting_decomposition" {
			sawReason = true
		}
	}
	if !sawReason {
		t.Errorf("expected awaiting_decomposition, got %+v", reasons)
	}
}
