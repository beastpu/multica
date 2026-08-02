package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
)

// createWorkflowNamed posts a minimal valid template and returns the response
// status plus the created id (empty when the create was rejected).
func createWorkflowNamed(t *testing.T, name string) (int, string) {
	t.Helper()
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          strings.TrimSpace(name),
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{{From: "start", To: "end"}},
	}
	recorder := httptest.NewRecorder()
	testHandler.CreateWorkflow(recorder, newRequest(
		http.MethodPost,
		"/api/workflow-templates?workspace_id="+testWorkspaceID,
		map[string]any{"name": name, "description": "", "definition": definition},
	))
	var response struct {
		Template struct {
			ID string `json:"id"`
		} `json:"template"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	return recorder.Code, response.Template.ID
}

func renameTemplate(t *testing.T, id, name string) int {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := newRequest(
		http.MethodPatch,
		"/api/workflow-templates/"+id+"?workspace_id="+testWorkspaceID,
		map[string]any{"name": name},
	)
	testHandler.UpdateWorkflowMetadata(
		recorder, withURLParam(request, "id", id),
	)
	return recorder.Code
}

func archiveTemplateForTest(t *testing.T, id string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := newRequest(
		http.MethodPost,
		"/api/workflow-templates/"+id+"/archive?workspace_id="+testWorkspaceID,
		nil,
	)
	testHandler.ArchiveWorkflow(recorder, withURLParam(request, "id", id))
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"archive template: got %d, body %s", recorder.Code, recorder.Body.String(),
		)
	}
}

func cleanupWorkflowNames(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := testPool.Exec(
			context.Background(),
			`DELETE FROM workflow_template_version WHERE template_id IN (
			   SELECT id FROM workflow_template
			   WHERE workspace_id = $1 AND name = $2)`,
			testWorkspaceID, name,
		); err != nil {
			t.Fatalf("cleanup versions: %v", err)
		}
		if _, err := testPool.Exec(
			context.Background(),
			`DELETE FROM workflow_template WHERE workspace_id = $1 AND name = $2`,
			testWorkspaceID, name,
		); err != nil {
			t.Fatalf("cleanup templates: %v", err)
		}
	}
}

// Two live templates sharing a name is the failure users actually hit: the
// list shows two identical cards and picking the right one when starting a run
// is guesswork.
//
// Archived ones are excluded on purpose. "Archive the old one, create its
// replacement under the same name" is how a template gets revised once runs
// depend on the old version, and forbidding it would push people into names
// like "X (new)" — the exact confusion this rule exists to prevent.
func TestWorkflowNameUniqueAmongLiveTemplates(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowNames(t, "Delivery pipeline")
	t.Cleanup(func() { cleanupWorkflowNames(t, "Delivery pipeline") })

	status, firstID := createWorkflowNamed(t, "Delivery pipeline")
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("first create: got %d, want success", status)
	}

	if status, _ := createWorkflowNamed(t, "Delivery pipeline"); status != http.StatusConflict {
		t.Fatalf("duplicate create: got %d, want %d", status, http.StatusConflict)
	}

	// Padding is invisible to a reader, so it must not buy a second copy.
	if status, _ := createWorkflowNamed(t, "  Delivery pipeline  "); status != http.StatusConflict {
		t.Fatalf(
			"whitespace-padded duplicate: got %d, want %d",
			status, http.StatusConflict,
		)
	}

	archiveTemplateForTest(t, firstID)

	status, _ = createWorkflowNamed(t, "Delivery pipeline")
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("reusing an archived name: got %d, want success", status)
	}
}

// Renaming is the other way to end up with two live templates called the same
// thing, so it carries the same rule.
func TestWorkflowRenameRejectsLiveName(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowNames(t, "Taken name", "Free name")
	t.Cleanup(func() { cleanupWorkflowNames(t, "Taken name", "Free name") })

	createWorkflowNamed(t, "Taken name")
	_, otherID := createWorkflowNamed(t, "Free name")

	if status := renameTemplate(t, otherID, "Taken name"); status != http.StatusConflict {
		t.Fatalf("rename onto a live name: got %d, want %d", status, http.StatusConflict)
	}

	// Renaming to the name it already has is a no-op, not a self-collision.
	if status := renameTemplate(t, otherID, "Free name"); status != http.StatusOK {
		t.Fatalf("rename to its own name: got %d, want %d", status, http.StatusOK)
	}
}
