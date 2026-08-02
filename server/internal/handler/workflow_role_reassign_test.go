package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func createWorkflowMemberForTest(t *testing.T, name, email string) string {
	t.Helper()
	ctx := context.Background()
	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, name, email).Scan(&userID); err != nil {
		t.Fatalf("create member %q: %v", name, err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
	`, testWorkspaceID, userID); err != nil {
		t.Fatalf("add member %q: %v", name, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = testPool.Exec(
			bg,
			`DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`,
			testWorkspaceID, userID,
		)
		_, _ = testPool.Exec(bg, `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

func updateWorkflowRolesForTest(
	t *testing.T,
	instanceID string,
	assignments []map[string]any,
	idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-instances/"+instanceID+"/roles?workspace_id="+testWorkspaceID,
			map[string]any{
				"role_assignments": assignments,
				"idempotency_key":  idempotencyKey,
			},
		),
		"instanceId",
		instanceID,
	)
	testHandler.UpdateWorkflowInstanceRoles(recorder, request)
	return recorder
}

func nodeOwnerParticipantsForTest(
	t *testing.T,
	nodeInstanceID string,
) []db.WorkflowNodeParticipant {
	t.Helper()
	rows, err := testHandler.Queries.ListWorkflowNodeParticipants(
		context.Background(),
		db.ListWorkflowNodeParticipantsParams{
			WorkflowNodeInstanceID: parseUUID(nodeInstanceID),
			WorkspaceID:            parseUUID(testWorkspaceID),
		},
	)
	if err != nil {
		t.Fatalf("list node participants: %v", err)
	}
	owners := make([]db.WorkflowNodeParticipant, 0, len(rows))
	for _, row := range rows {
		if row.Role == "owner" {
			owners = append(owners, row)
		}
	}
	return owners
}

func TestWorkflowRolesCanBeReassignedWhileRunning(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)

	first := createWorkflowMemberForTest(
		t, "Workflow first owner", "workflow-reassign-first@multica.ai",
	)
	second := createWorkflowMemberForTest(
		t, "Workflow second owner", "workflow-reassign-second@multica.ai",
	)

	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Role reassign",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{Key: "work", Kind: "activity", Name: "Work", OwnerRole: "owner"},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	if err := workflowdomain.ValidateDefinition(definition); err != nil {
		t.Fatalf("definition invalid: %v", err)
	}
	templateID := createPublishedWorkflowForTest(
		t, "Role reassign template", definition,
	)
	hostID := createWorkflowHostForTest(t, "Workflow role reassign host")
	started := startWorkflowForTest(
		t, hostID, templateID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": first,
		}},
		"role-reassign-start",
	)
	if started.Instance.Status != "running" {
		t.Fatalf("instance status = %q, want running", started.Instance.Status)
	}
	work := findWorkflowNodeResponse(t, started.Nodes, "work", 1)

	owners := nodeOwnerParticipantsForTest(t, work.ID)
	if len(owners) != 1 || uuidToString(owners[0].ActorID) != first {
		t.Fatalf("initial owner participants = %+v, want %s", owners, first)
	}

	recorder := updateWorkflowRolesForTest(
		t, started.Instance.ID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": second,
		}},
		"role-reassign-running",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"reassign while running status = %d, body = %s",
			recorder.Code, recorder.Body.String(),
		)
	}

	owners = nodeOwnerParticipantsForTest(t, work.ID)
	if len(owners) != 1 || uuidToString(owners[0].ActorID) != second {
		t.Fatalf(
			"owner participants after reassign = %+v, want single %s",
			owners, second,
		)
	}

	// Replaying the same idempotency key must not duplicate participants.
	replay := updateWorkflowRolesForTest(
		t, started.Instance.ID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": second,
		}},
		"role-reassign-running",
	)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status = %d, body = %s", replay.Code, replay.Body.String())
	}
	if owners = nodeOwnerParticipantsForTest(t, work.ID); len(owners) != 1 {
		t.Fatalf("owner participants after replay = %+v, want one", owners)
	}
}

func TestWorkflowRolesRejectedAfterInstanceIsTerminal(t *testing.T) {
	withFeatureFlag(t, testHandler, featureflags.WorkflowsActivityEngine, true)
	cleanupWorkflowRuntimeTest(t)

	owner := createWorkflowMemberForTest(
		t, "Workflow terminal owner", "workflow-reassign-terminal@multica.ai",
	)
	definition := workflowdomain.Definition{
		SchemaVersion: workflowdomain.DefinitionSchemaVersion,
		Name:          "Role reassign terminal",
		Roles: []workflowdomain.RoleDefinition{{
			Key: "owner", Name: "Owner", Required: true,
			AllowedActorTypes: []string{"member"},
		}},
		Nodes: []workflowdomain.NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{Key: "work", Kind: "activity", Name: "Work", OwnerRole: "owner"},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []workflowdomain.EdgeDefinition{
			{From: "start", To: "work"},
			{From: "work", To: "end"},
		},
		Acceptance: workflowdomain.AcceptanceDefinition{Policy: "none"},
	}
	templateID := createPublishedWorkflowForTest(
		t, "Role reassign terminal template", definition,
	)
	hostID := createWorkflowHostForTest(t, "Workflow role reassign terminal host")
	started := startWorkflowForTest(
		t, hostID, templateID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": owner,
		}},
		"role-reassign-terminal-start",
	)

	cancel := httptest.NewRecorder()
	cancelRequest := withURLParam(
		newRequest(
			http.MethodPost,
			"/api/workflow-instances/"+started.Instance.ID+
				"/cancel?workspace_id="+testWorkspaceID,
			map[string]any{
				"reason":          "no longer needed",
				"idempotency_key": "role-reassign-terminal-cancel",
			},
		),
		"instanceId",
		started.Instance.ID,
	)
	testHandler.CancelWorkflowInstance(cancel, cancelRequest)
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", cancel.Code, cancel.Body.String())
	}

	recorder := updateWorkflowRolesForTest(
		t, started.Instance.ID,
		[]map[string]any{{
			"role_key": "owner", "actor_type": "member", "actor_id": owner,
		}},
		"role-reassign-after-cancel",
	)
	if recorder.Code != http.StatusConflict {
		t.Fatalf(
			"reassign after cancel status = %d, want %d, body = %s",
			recorder.Code, http.StatusConflict, recorder.Body.String(),
		)
	}
}
