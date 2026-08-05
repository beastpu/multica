package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

func TestWorkflowDefinitionBytesNormalizesLegacyAuthoringDefinition(t *testing.T) {
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Normalized authoring",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key: "work", Kind: "activity", Name: "Work", OwnerRole: "owner",
				Completion: workflowdomain.CompletionDefinition{Mode: "manual"},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"}, {From: "work", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{
			Policy: "member", ApproverRole: "owner",
		},
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("marshal legacy definition: %v", err)
	}
	raw = append(raw[:len(raw)-1], []byte(`,"applies_to":{"kind":"issue"}}`)...)
	normalized, _, err := workflowDefinitionBytes(raw)
	if err != nil {
		t.Fatalf("normalize workflow definition: %v", err)
	}
	var saved workflowdomain.Definition
	if err := json.Unmarshal(normalized, &saved); err != nil {
		t.Fatalf("decode normalized definition: %v", err)
	}
	if saved.Acceptance != (workflowdomain.AcceptanceDefinition{}) {
		t.Fatalf("saved acceptance = %#v, want none", saved.Acceptance)
	}
	if bytes.Contains(normalized, []byte(`"applies_to"`)) {
		t.Fatalf("saved definition retained applies_to: %s", normalized)
	}
	work := saved.Nodes[1]
	if work.Completion.Mode != "automatic" || work.Reviewer == nil ||
		work.Reviewer.Kind != "role" || work.Reviewer.Role != "owner" ||
		!work.Reviewer.Required {
		t.Fatalf("saved work node = %#v, want required owner role approval", work)
	}
}

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

func TestWorkflowPermissionsImmutabilityAndSaveValidation(t *testing.T) {
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
		Version  workflowWorkflowVersionResponse `json:"version"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created workflow template: %v", err)
	}

	// Creating a workflow makes it runnable. There is no second step: the
	// definition validated on the way in, so version 1 is already live.
	if created.Workflow.Status != "published" || created.Version.Version != 1 {
		t.Fatalf(
			"created workflow status = %q, version = %d, want published version 1",
			created.Workflow.Status, created.Version.Version,
		)
	}

	var publishedDefinition []byte
	if err := testPool.QueryRow(ctx, `
		SELECT definition
		FROM workflow_version
		WHERE workflow_id = $1 AND version = 1
	`, created.Workflow.ID).Scan(&publishedDefinition); err != nil {
		t.Fatalf("load published workflow definition: %v", err)
	}

	save := func(body any) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		testHandler.SaveWorkflowDefinition(recorder, withURLParam(
			newRequest(
				http.MethodPut,
				"/api/workflows/"+created.Workflow.ID+
					"/definition?workspace_id="+testWorkspaceID,
				body,
			),
			"id",
			created.Workflow.ID,
		))
		return recorder
	}

	// A definition that does not validate is refused outright. The author is
	// in the editor with the error in front of them, so there is nothing to
	// gain by storing a version nobody can run.
	broken := definition
	broken.Nodes = []workflowdomain.NodeDefinition{
		{Key: "start", Kind: "start", Name: "Start"},
		{Key: "work", Kind: "activity", Name: "Work", OwnerRole: "nobody"},
		{Key: "end", Kind: "end", Name: "End"},
	}
	rejected := save(map[string]any{
		"definition": broken, "change_summary": "Broken",
	})
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf(
			"invalid save status = %d, want %d, body = %s",
			rejected.Code, http.StatusBadRequest, rejected.Body.String(),
		)
	}
	var versionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM workflow_version WHERE workflow_id = $1
	`, created.Workflow.ID).Scan(&versionCount); err != nil {
		t.Fatalf("count workflow versions: %v", err)
	}
	if versionCount != 1 {
		t.Fatalf("rejected save left %d versions, want 1", versionCount)
	}

	updatedDefinition := definition
	updatedDefinition.Name = "Template lifecycle v2"
	accepted := save(map[string]any{
		"definition": updatedDefinition, "change_summary": "Version two",
	})
	if accepted.Code != http.StatusOK {
		t.Fatalf(
			"SaveWorkflowDefinition status = %d, body = %s",
			accepted.Code, accepted.Body.String(),
		)
	}
	var savedVersion workflowWorkflowVersionResponse
	if err := json.Unmarshal(accepted.Body.Bytes(), &struct {
		Version *workflowWorkflowVersionResponse `json:"version"`
	}{Version: &savedVersion}); err != nil {
		t.Fatalf("decode saved workflow version: %v", err)
	}
	if savedVersion.Version != 2 {
		t.Fatalf("saved version = %d, want 2", savedVersion.Version)
	}

	// A second editor still holding version 1 saves after this one landed.
	// The version numbers do not collide — it would get 3 — so the unique
	// index never fires and its definition would quietly become live over a
	// version it never saw. Saying which version it started from is what
	// turns that into a conflict.
	stale := save(map[string]any{
		"definition":      updatedDefinition,
		"change_summary":  "Third",
		"base_version_id": created.Version.ID,
	})
	if stale.Code != http.StatusConflict {
		t.Fatalf(
			"stale save status = %d, want %d, body = %s",
			stale.Code, http.StatusConflict, stale.Body.String(),
		)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM workflow_version WHERE workflow_id = $1
	`, created.Workflow.ID).Scan(&versionCount); err != nil {
		t.Fatalf("count workflow versions: %v", err)
	}
	if versionCount != 2 {
		t.Fatalf("refused stale save left %d versions, want 2", versionCount)
	}

	// Saving from the version that is actually live still works.
	current := save(map[string]any{
		"definition":      updatedDefinition,
		"change_summary":  "Fourth",
		"base_version_id": savedVersion.ID,
	})
	if current.Code != http.StatusOK {
		t.Fatalf(
			"current save status = %d, body = %s",
			current.Code, current.Body.String(),
		)
	}

	// Version 1 is what any run started before this edit is still executing,
	// so saving must not have touched it.
	var publishedAfter []byte
	if err := testPool.QueryRow(ctx, `
		SELECT definition
		FROM workflow_version
		WHERE workflow_id = $1 AND version = 1
	`, created.Workflow.ID).Scan(&publishedAfter); err != nil {
		t.Fatalf("reload published workflow definition: %v", err)
	}
	if string(publishedAfter) != string(publishedDefinition) {
		t.Fatalf(
			"version 1 changed while saving version 2: before=%s after=%s",
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
	// Every stored version is runnable, so there is nothing left to hide from
	// a member: they see the same history an admin does — the three that were
	// actually written, with the rejected and the stale save absent because
	// neither ever became a version.
	if len(memberDetail.Versions) != 3 {
		t.Fatalf(
			"member-visible workflow versions = %d, want 3",
			len(memberDetail.Versions),
		)
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
			name: "save definition", method: http.MethodPut,
			path: "/api/workflows/" + created.Workflow.ID + "/definition",
			body: map[string]any{
				"definition": updatedDefinition, "change_summary": "Archived",
			},
			call: testHandler.SaveWorkflowDefinition,
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

// Deleting is for a workflow that was never used — the junk a workspace
// accumulates while learning the editor. Once a run exists the workflow is
// load-bearing: runs name it and its version by id, and no foreign key stops
// those rows from outliving it, so the delete has to refuse and say why.
func TestDeleteWorkflowKeepsWorkflowsThatHaveRun(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)
	ctx := context.Background()

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Deletable",
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

	create := func(t *testing.T, name string) workflowResponse {
		t.Helper()
		recorder := httptest.NewRecorder()
		testHandler.CreateWorkflow(recorder, newRequest(
			http.MethodPost,
			"/api/workflows?workspace_id="+testWorkspaceID,
			map[string]any{"name": name, "definition": definition},
		))
		if recorder.Code != http.StatusCreated {
			t.Fatalf("CreateWorkflow status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		var created struct {
			Workflow workflowResponse `json:"workflow"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode created workflow: %v", err)
		}
		return created.Workflow
	}
	deleteWorkflow := func(id string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		testHandler.DeleteWorkflow(recorder, withURLParam(
			newRequest(
				http.MethodDelete,
				"/api/workflows/"+id+"?workspace_id="+testWorkspaceID,
				nil,
			),
			"id",
			id,
		))
		return recorder
	}
	countRows := func(t *testing.T, query, id string) int {
		t.Helper()
		var count int
		if err := testPool.QueryRow(ctx, query, id).Scan(&count); err != nil {
			t.Fatalf("count rows: %v", err)
		}
		return count
	}

	unused := create(t, "Never run workflow")
	if recorder := deleteWorkflow(unused.ID); recorder.Code != http.StatusNoContent {
		t.Fatalf(
			"delete unused workflow status = %d, want %d, body = %s",
			recorder.Code, http.StatusNoContent, recorder.Body.String(),
		)
	}
	if count := countRows(t, `SELECT count(*) FROM workflow WHERE id = $1`, unused.ID); count != 0 {
		t.Fatalf("deleted workflow left %d rows, want 0", count)
	}
	// The versions go with it. Left behind they are unreachable rows keyed to
	// a workflow nothing can load.
	if count := countRows(
		t, `SELECT count(*) FROM workflow_version WHERE workflow_id = $1`, unused.ID,
	); count != 0 {
		t.Fatalf("deleted workflow left %d versions, want 0", count)
	}

	used := create(t, "Already run workflow")
	var versionID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM workflow_version WHERE workflow_id = $1 ORDER BY version DESC LIMIT 1
	`, used.ID).Scan(&versionID); err != nil {
		t.Fatalf("load workflow version: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO workflow_instance (
			workspace_id, workflow_id, workflow_version_id, title,
			status, host_status_mode, started_by_type
		) VALUES ($1, $2, $3, 'Run 1', 'running', 'independent', 'system')
	`, testWorkspaceID, used.ID, versionID); err != nil {
		t.Fatalf("insert workflow instance: %v", err)
	}

	refused := deleteWorkflow(used.ID)
	if refused.Code != http.StatusConflict {
		t.Fatalf(
			"delete workflow with runs status = %d, want %d, body = %s",
			refused.Code, http.StatusConflict, refused.Body.String(),
		)
	}
	if count := countRows(t, `SELECT count(*) FROM workflow WHERE id = $1`, used.ID); count != 1 {
		t.Fatalf("refused delete removed the workflow, rows = %d, want 1", count)
	}
	// A refused delete must not take the versions with it either — the
	// workflow survives, and a workflow whose versions are gone is worse than
	// one that was never deleted.
	if count := countRows(
		t, `SELECT count(*) FROM workflow_version WHERE workflow_id = $1`, used.ID,
	); count != 1 {
		t.Fatalf("refused delete left %d versions, want 1", count)
	}

	missing := deleteWorkflow("00000000-0000-0000-0000-0000000000ff")
	if missing.Code != http.StatusNotFound {
		t.Fatalf(
			"delete missing workflow status = %d, want %d, body = %s",
			missing.Code, http.StatusNotFound, missing.Body.String(),
		)
	}
}
