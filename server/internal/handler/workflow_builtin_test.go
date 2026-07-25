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

func deleteWorkflowTemplatesByName(t *testing.T, names []string) {
	t.Helper()
	ctx := context.Background()
	for _, name := range names {
		if _, err := testPool.Exec(ctx, `
			DELETE FROM workflow_template_version WHERE template_id IN (
				SELECT id FROM workflow_template WHERE workspace_id = $1 AND name = $2
			)
		`, testWorkspaceID, name); err != nil {
			t.Fatalf("delete workflow template versions %q: %v", name, err)
		}
		if _, err := testPool.Exec(ctx, `
			DELETE FROM workflow_template WHERE workspace_id = $1 AND name = $2
		`, testWorkspaceID, name); err != nil {
			t.Fatalf("delete workflow templates %q: %v", name, err)
		}
	}
}

func TestListBuiltinWorkflowTemplates(t *testing.T) {
	recorder := httptest.NewRecorder()
	testHandler.ListBuiltinWorkflowTemplates(
		recorder,
		newRequest(
			http.MethodGet,
			"/api/workflow-templates/builtin?workspace_id="+testWorkspaceID,
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list builtin status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Templates []struct {
			Key         string          `json:"key"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Definition  json.RawMessage `json:"definition"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(response.Templates) != len(workflowdomain.BuiltinTemplates()) {
		t.Fatalf("list returned %d templates", len(response.Templates))
	}
	for _, template := range response.Templates {
		if template.Key == "" || template.Name == "" || template.Description == "" {
			t.Fatalf("builtin template response missing fields: %+v", template)
		}
		if len(template.Definition) == 0 {
			t.Fatalf("builtin template %q response has no definition", template.Key)
		}
	}
}

func TestCreateWorkflowTemplateFromBuiltin(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	builtin, ok := workflowdomain.FindBuiltinTemplate("bug_fix")
	if !ok {
		t.Fatal("builtin template bug_fix not found")
	}
	deleteWorkflowTemplatesByName(t, []string{builtin.Name})

	recorder := httptest.NewRecorder()
	testHandler.CreateWorkflowTemplateFromBuiltin(
		recorder,
		newRequest(
			http.MethodPost,
			"/api/workflow-templates/from-builtin?workspace_id="+testWorkspaceID,
			map[string]any{"key": "bug_fix"},
		),
	)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("from-builtin status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Template struct {
			ID                       string `json:"id"`
			Name                     string `json:"name"`
			Status                   string `json:"status"`
			LatestPublishedVersionID string `json:"latest_published_version_id"`
		} `json:"template"`
		Version struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
			Status  string `json:"status"`
		} `json:"version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode from-builtin response: %v", err)
	}
	if response.Template.Name != builtin.Name {
		t.Fatalf("template name = %q, want %q", response.Template.Name, builtin.Name)
	}
	if response.Template.Status != "published" {
		t.Fatalf("template status = %q, want published", response.Template.Status)
	}
	if response.Version.Status != "published" || response.Version.Version != 1 {
		t.Fatalf(
			"version = %d status %q, want version 1 published",
			response.Version.Version,
			response.Version.Status,
		)
	}
	if response.Template.LatestPublishedVersionID != response.Version.ID {
		t.Fatalf(
			"latest_published_version_id = %q, want %q",
			response.Template.LatestPublishedVersionID,
			response.Version.ID,
		)
	}

	duplicate := httptest.NewRecorder()
	testHandler.CreateWorkflowTemplateFromBuiltin(
		duplicate,
		newRequest(
			http.MethodPost,
			"/api/workflow-templates/from-builtin?workspace_id="+testWorkspaceID,
			map[string]any{"key": "bug_fix"},
		),
	)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want %d", duplicate.Code, http.StatusConflict)
	}

	unknown := httptest.NewRecorder()
	testHandler.CreateWorkflowTemplateFromBuiltin(
		unknown,
		newRequest(
			http.MethodPost,
			"/api/workflow-templates/from-builtin?workspace_id="+testWorkspaceID,
			map[string]any{"key": "does_not_exist"},
		),
	)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown key status = %d, want %d", unknown.Code, http.StatusNotFound)
	}
}
