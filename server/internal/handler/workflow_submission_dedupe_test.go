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

// Two submissions that come to rest as the same record are one handoff.
//
// The caller's idempotency key is computed on what it sent; the row is written
// from what the server made of it. Everything the server normalises therefore
// falls through the check: `true` and `True` are the same stored value and two
// different keys, so a node that had one conclusion showed two — identical
// prose, identical fields, both marked valid, with nothing on screen to say
// which of them a reviewer was judging.
func TestWorkflowSubmission_NormalisedDuplicateIsOneHandoff(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Submission dedupe",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "root_cause", Kind: "activity", Name: "Root cause",
				OwnerRole: "owner", IssuePolicy: "none",
				SubmissionSchema: &workflowdomain.SubmissionSchema{Policy: "single"},
				Outputs: []workflowdomain.OutputField{
					{Key: "root_cause_found", Type: "bool", Required: true},
					{Key: "confidence", Type: "number"},
				},
				// Never delivered, so the node stays open across the submissions
				// below instead of completing on the first one.
				Artifacts: []workflowdomain.ArtifactRequirement{{
					Key: "analysis", Name: "Analysis", Required: true,
				}},
				Completion: workflowdomain.CompletionDefinition{SubmissionRequired: true},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "root_cause"}, {From: "root_cause", To: "end"},
		},
	}
	templateID := createPublishedWorkflowForTest(t, "Submission dedupe template", definition)
	hostID := createWorkflowHostForTest(t, "Submission dedupe host")
	started := startWorkflowForTest(t, hostID, templateID, []map[string]any{{
		"role_key": "owner", "actor_type": "member", "actor_id": testUserID,
	}}, "submission-dedupe-start")
	node := findWorkflowNodeResponse(t, started.Nodes, "root_cause", 1)

	submit := func(payload map[string]any, summary, key string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := withURLParam(newRequest(
			http.MethodPost,
			"/api/workflow-node-instances/"+node.ID+"/submissions?workspace_id="+testWorkspaceID,
			map[string]any{
				"payload": payload, "summary": summary, "idempotency_key": key,
			},
		), "nodeInstanceId", node.ID)
		testHandler.CreateWorkflowNodeSubmission(recorder, request)
		return recorder
	}

	submissionCount := func() int {
		t.Helper()
		var count int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM workflow_node_submission
			WHERE workflow_node_instance_id = $1
		`, node.ID).Scan(&count); err != nil {
			t.Fatalf("count submissions: %v", err)
		}
		return count
	}

	const conclusion = "The button carries no onClick."

	first := submit(
		map[string]any{"root_cause_found": true, "confidence": 0.9},
		conclusion, "handoff-a",
	)
	if first.Code != http.StatusCreated {
		t.Fatalf("first submission status = %d, body = %s", first.Code, first.Body.String())
	}
	var firstBody struct {
		Submission struct {
			ID       string `json:"id"`
			Revision int32  `json:"revision"`
		} `json:"submission"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("decode first submission: %v", err)
	}

	// The same conclusion, spelled the way an agent's shell would spell it, and
	// under a different key because the key was derived from that spelling.
	// strconv.ParseBool takes all of these to one stored value.
	for _, spelling := range []struct {
		key     string
		payload map[string]any
	}{
		{"handoff-b", map[string]any{"root_cause_found": "true", "confidence": 0.9}},
		{"handoff-c", map[string]any{"root_cause_found": "True", "confidence": "0.9"}},
		{"handoff-d", map[string]any{"root_cause_found": "1", "confidence": 0.9}},
	} {
		repeat := submit(spelling.payload, conclusion, spelling.key)
		if repeat.Code != http.StatusCreated && repeat.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", spelling.key, repeat.Code, repeat.Body.String())
		}
		var body struct {
			Submission struct {
				ID       string `json:"id"`
				Revision int32  `json:"revision"`
			} `json:"submission"`
		}
		if err := json.Unmarshal(repeat.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", spelling.key, err)
		}
		if body.Submission.ID != firstBody.Submission.ID {
			t.Fatalf(
				"%s created submission %s (revision %d); the first was %s (revision %d)",
				spelling.key, body.Submission.ID, body.Submission.Revision,
				firstBody.Submission.ID, firstBody.Submission.Revision,
			)
		}
	}
	if count := submissionCount(); count != 1 {
		t.Fatalf("node carries %d submissions for one conclusion, want 1", count)
	}

	// A conclusion that actually changed is a new handoff, not a duplicate.
	// Collapsing on content must not collapse on disagreement.
	changed := submit(
		map[string]any{"root_cause_found": false, "confidence": 0.9},
		conclusion, "handoff-e",
	)
	if changed.Code != http.StatusCreated {
		t.Fatalf("changed submission status = %d, body = %s", changed.Code, changed.Body.String())
	}
	if count := submissionCount(); count != 2 {
		t.Fatalf("a different conclusion did not land: node carries %d submissions, want 2", count)
	}

	// And so is the same fields under a different conclusion.
	reworded := submit(
		map[string]any{"root_cause_found": false, "confidence": 0.9},
		conclusion+" Confirmed against the form model.", "handoff-f",
	)
	if reworded.Code != http.StatusCreated {
		t.Fatalf("reworded submission status = %d, body = %s", reworded.Code, reworded.Body.String())
	}
	if count := submissionCount(); count != 3 {
		t.Fatalf("a reworded conclusion did not land: node carries %d submissions, want 3", count)
	}
}
