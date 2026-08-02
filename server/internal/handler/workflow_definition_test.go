package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

func TestWorkflowWriteFlagIsWorkspaceScoped(t *testing.T) {
	provider := featureflag.NewStaticProvider()
	provider.Set(featureflags.WorkflowsActivityEngine, featureflag.Rule{
		Default: false,
		Allow:   []string{testWorkspaceID},
		AllowBy: "workspace_id",
	})
	previous := testHandler.FeatureFlags
	testHandler.FeatureFlags = featureflag.NewService(provider)
	t.Cleanup(func() {
		testHandler.FeatureFlags = previous
	})

	allowed := httptest.NewRecorder()
	testHandler.CreateWorkflow(
		allowed,
		newRequest(
			http.MethodPost,
			"/api/workflow-templates?workspace_id="+testWorkspaceID,
			nil,
		),
	)
	if allowed.Code != http.StatusBadRequest {
		t.Fatalf(
			"allowed workspace reached status %d, want request validation %d",
			allowed.Code,
			http.StatusBadRequest,
		)
	}

	deniedWorkspaceID := "11111111-1111-1111-1111-111111111111"
	deniedRequest := newRequest(
		http.MethodPost,
		"/api/workflow-templates?workspace_id="+deniedWorkspaceID,
		nil,
	)
	deniedRequest.Header.Set("X-Workspace-ID", deniedWorkspaceID)
	denied := httptest.NewRecorder()
	testHandler.CreateWorkflow(denied, deniedRequest)
	if denied.Code != http.StatusNotFound {
		t.Fatalf(
			"denied workspace status = %d, want %d, body = %s",
			denied.Code,
			http.StatusNotFound,
			denied.Body.String(),
		)
	}
}

func TestWorkflowPermissionsImmutabilityAndDraftConcurrency(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	var memberID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Workflow template member', 'workflow-template-member@multica.ai')
		RETURNING id
	`).Scan(&memberID); err != nil {
		t.Fatalf("create workflow template member: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, memberID); err != nil {
		t.Fatalf("add workflow template member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(
			context.Background(),
			`DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`,
			testWorkspaceID,
			memberID,
		)
		_, _ = testPool.Exec(
			context.Background(),
			`DELETE FROM "user" WHERE id = $1`,
			memberID,
		)
	})

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Template lifecycle",
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{Key: "work", Kind: "activity", Name: "Work"},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("template lifecycle definition invalid: %v", err)
	}

	forbidden := httptest.NewRecorder()
	forbiddenRequest := newRequest(
		http.MethodPost,
		"/api/workflow-templates?workspace_id="+testWorkspaceID,
		map[string]any{
			"name":       "Forbidden template",
			"definition": definition,
		},
	)
	forbiddenRequest.Header.Set("X-User-ID", memberID)
	testHandler.CreateWorkflow(forbidden, forbiddenRequest)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf(
			"member template create status = %d, want %d, body = %s",
			forbidden.Code,
			http.StatusForbidden,
			forbidden.Body.String(),
		)
	}

	create := httptest.NewRecorder()
	testHandler.CreateWorkflow(
		create,
		newRequest(
			http.MethodPost,
			"/api/workflow-templates?workspace_id="+testWorkspaceID,
			map[string]any{
				"name":           "Lifecycle template",
				"definition":     definition,
				"change_summary": "Version one",
			},
		),
	)
	if create.Code != http.StatusCreated {
		t.Fatalf(
			"CreateWorkflow status = %d, body = %s",
			create.Code,
			create.Body.String(),
		)
	}
	var created struct {
		Workflow workflowResponse                `json:"workflow"`
		Draft    workflowWorkflowVersionResponse `json:"draft"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created workflow template: %v", err)
	}

	publish := httptest.NewRecorder()
	publishRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-templates/"+created.Workflow.ID+
				"/publish?workspace_id="+testWorkspaceID,
			nil,
		),
		"id",
		created.Workflow.ID,
	)
	testHandler.PublishWorkflow(publish, publishRequest)
	if publish.Code != http.StatusOK {
		t.Fatalf(
			"PublishWorkflow status = %d, body = %s",
			publish.Code,
			publish.Body.String(),
		)
	}

	var publishedDefinition []byte
	if err := testPool.QueryRow(ctx, `
		SELECT definition
		FROM workflow_version
		WHERE workflow_id = $1 AND status = 'published' AND version = 1
	`, created.Workflow.ID).Scan(&publishedDefinition); err != nil {
		t.Fatalf("load published workflow definition: %v", err)
	}

	createDraft := httptest.NewRecorder()
	createDraftRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-templates/"+created.Workflow.ID+
				"/draft?workspace_id="+testWorkspaceID,
			nil,
		),
		"id",
		created.Workflow.ID,
	)
	testHandler.CreateWorkflowDraft(createDraft, createDraftRequest)
	if createDraft.Code != http.StatusCreated {
		t.Fatalf(
			"CreateWorkflowDraft status = %d, body = %s",
			createDraft.Code,
			createDraft.Body.String(),
		)
	}
	var draft workflowWorkflowVersionResponse
	if err := json.Unmarshal(createDraft.Body.Bytes(), &draft); err != nil {
		t.Fatalf("decode workflow template draft: %v", err)
	}

	updatedDefinition := definition
	updatedDefinition.Name = "Template lifecycle v2"
	updateDraft := func(revision int64, summary string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := withURLParam(
			newRequest(
				http.MethodPut,
				"/api/workflow-templates/"+created.Workflow.ID+
					"/draft?workspace_id="+testWorkspaceID,
				map[string]any{
					"definition":     updatedDefinition,
					"change_summary": summary,
					"revision":       revision,
				},
			),
			"id",
			created.Workflow.ID,
		)
		testHandler.UpdateWorkflowDraft(recorder, request)
		return recorder
	}
	firstUpdate := updateDraft(draft.Revision, "Version two")
	if firstUpdate.Code != http.StatusOK {
		t.Fatalf(
			"UpdateWorkflowDraft status = %d, body = %s",
			firstUpdate.Code,
			firstUpdate.Body.String(),
		)
	}
	staleUpdate := updateDraft(draft.Revision, "Stale overwrite")
	if staleUpdate.Code != http.StatusConflict {
		t.Fatalf(
			"stale workflow draft status = %d, want %d, body = %s",
			staleUpdate.Code,
			http.StatusConflict,
			staleUpdate.Body.String(),
		)
	}
	var conflict struct {
		LatestRevision int64                           `json:"latest_revision"`
		Draft          workflowWorkflowVersionResponse `json:"draft"`
	}
	if err := json.Unmarshal(staleUpdate.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode workflow draft conflict: %v", err)
	}
	if conflict.LatestRevision != draft.Revision+1 ||
		conflict.Draft.Revision != draft.Revision+1 {
		t.Fatalf("workflow draft conflict = %#v", conflict)
	}

	var publishedAfter []byte
	if err := testPool.QueryRow(ctx, `
		SELECT definition
		FROM workflow_version
		WHERE workflow_id = $1 AND status = 'published' AND version = 1
	`, created.Workflow.ID).Scan(&publishedAfter); err != nil {
		t.Fatalf("reload published workflow definition: %v", err)
	}
	if string(publishedAfter) != string(publishedDefinition) {
		t.Fatalf(
			"published definition changed while editing draft: before=%s after=%s",
			publishedDefinition,
			publishedAfter,
		)
	}

	memberGet := httptest.NewRecorder()
	memberGetRequest := withURLParam(
		newRequest(
			http.MethodGet,
			"/api/workflow-templates/"+created.Workflow.ID+
				"?workspace_id="+testWorkspaceID,
			nil,
		),
		"id",
		created.Workflow.ID,
	)
	memberGetRequest.Header.Set("X-User-ID", memberID)
	testHandler.GetWorkflow(memberGet, memberGetRequest)
	if memberGet.Code != http.StatusOK {
		t.Fatalf(
			"member workflow template read status = %d, body = %s",
			memberGet.Code,
			memberGet.Body.String(),
		)
	}
	var memberDetail struct {
		Versions []workflowWorkflowVersionResponse `json:"versions"`
	}
	if err := json.Unmarshal(memberGet.Body.Bytes(), &memberDetail); err != nil {
		t.Fatalf("decode member workflow template detail: %v", err)
	}
	if len(memberDetail.Versions) != 1 ||
		memberDetail.Versions[0].Status != "published" {
		t.Fatalf("member-visible workflow versions = %#v", memberDetail.Versions)
	}

	archive := httptest.NewRecorder()
	archiveRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-templates/"+created.Workflow.ID+
				"/archive?workspace_id="+testWorkspaceID,
			nil,
		),
		"id",
		created.Workflow.ID,
	)
	testHandler.ArchiveWorkflow(archive, archiveRequest)
	if archive.Code != http.StatusOK {
		t.Fatalf(
			"ArchiveWorkflow status = %d, body = %s",
			archive.Code,
			archive.Body.String(),
		)
	}

	archivedMutations := []struct {
		name   string
		method string
		path   string
		body   any
		call   func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "metadata", method: http.MethodPatch,
			path: "/api/workflow-templates/" + created.Workflow.ID,
			body: map[string]any{"name": "Changed after archive"},
			call: testHandler.UpdateWorkflowMetadata,
		},
		{
			name: "draft", method: http.MethodPost,
			path: "/api/workflow-templates/" + created.Workflow.ID + "/draft",
			call: testHandler.CreateWorkflowDraft,
		},
		{
			name: "update draft", method: http.MethodPut,
			path: "/api/workflow-templates/" + created.Workflow.ID + "/draft",
			body: map[string]any{
				"definition": updatedDefinition, "change_summary": "Archived",
				"revision": draft.Revision + 1,
			},
			call: testHandler.UpdateWorkflowDraft,
		},
		{
			name: "publish", method: http.MethodPost,
			path: "/api/workflow-templates/" + created.Workflow.ID + "/publish",
			call: testHandler.PublishWorkflow,
		},
	}
	for _, mutation := range archivedMutations {
		t.Run("archived rejects "+mutation.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := withURLParam(
				newRequest(
					mutation.method,
					mutation.path+"?workspace_id="+testWorkspaceID,
					mutation.body,
				),
				"id",
				created.Workflow.ID,
			)
			mutation.call(recorder, request)
			if recorder.Code != http.StatusConflict {
				t.Fatalf(
					"%s status = %d, want %d, body = %s",
					mutation.name,
					recorder.Code,
					http.StatusConflict,
					recorder.Body.String(),
				)
			}
		})
	}
}
