package handler

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func workflowNodeWithReason(t *testing.T, status, code string) db.WorkflowNodeInstance {
	t.Helper()
	reasons, err := json.Marshal([]workflowdomain.WaitingReason{{
		Code: code, Message: code,
	}})
	if err != nil {
		t.Fatalf("marshal waiting reason: %v", err)
	}
	return db.WorkflowNodeInstance{Status: status, WaitingReasons: reasons}
}

func workflowTestUUID(last byte) pgtype.UUID {
	var value [16]byte
	value[15] = last
	return pgtype.UUID{Bytes: value, Valid: true}
}

func TestWorkflowRuntimeNextActionUsesSpecificIntervention(t *testing.T) {
	tests := []struct {
		name               string
		status             string
		nodes              []db.WorkflowNodeInstance
		tasks              []db.WorkflowNodeTask
		awaitingAcceptance bool
		wantAction         string
		wantReason         string
	}{
		{
			name:   "materialization failure wins",
			status: "running",
			tasks: []db.WorkflowNodeTask{{
				MaterializationStatus: "failed",
			}},
			wantAction: "retry_materialization",
			wantReason: "materialization_failed",
		},
		{
			name:   "executor setup",
			status: "needs_setup",
			tasks: []db.WorkflowNodeTask{{
				MaterializationStatus: "pending_materialization",
			}},
			wantAction: "configure_executor",
			wantReason: "executor_unresolved",
		},
		{
			name:       "missing role",
			status:     "needs_setup",
			wantAction: "configure_roles",
			wantReason: "missing_required_role",
		},
		{
			name:   "submission",
			status: "running",
			nodes: []db.WorkflowNodeInstance{
				workflowNodeWithReason(t, "waiting", "valid_submission_required"),
			},
			wantAction: "submit_result",
			wantReason: "awaiting_submission",
		},
		{
			name:   "member verdict",
			status: "running",
			nodes: []db.WorkflowNodeInstance{
				workflowNodeWithReason(t, "in_review", "review_required"),
			},
			wantAction: "record_verdict",
			wantReason: "awaiting_verdict",
		},
		{
			// Acceptance left the canvas, so nothing on a node says the run is
			// parked on it — the pending record does.
			name:               "acceptance",
			status:             "running",
			awaitingAcceptance: true,
			wantAction:         "review_acceptance",
			wantReason:         "awaiting_acceptance",
		},
		{
			name:   "manual completion",
			status: "running",
			nodes: []db.WorkflowNodeInstance{
				workflowNodeWithReason(t, "waiting", "manual_completion_required"),
			},
			wantAction: "complete_activity",
			wantReason: "awaiting_manual_completion",
		},
		{
			name:   "blocked recovery",
			status: "running",
			nodes: []db.WorkflowNodeInstance{{
				Status: "blocked", WaitingReasons: []byte("[]"),
			}},
			wantAction: "recover_activity",
			wantReason: "blocked_or_timeout",
		},
		{
			name:       "healthy runtime",
			status:     "running",
			wantAction: "view_current_activity",
			wantReason: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action, reason := workflowRuntimeNextAction(
				test.status,
				test.nodes,
				test.tasks,
				test.awaitingAcceptance,
			)
			if action != test.wantAction || reason != test.wantReason {
				t.Fatalf(
					"workflowRuntimeNextAction() = (%q, %q), want (%q, %q)",
					action,
					reason,
					test.wantAction,
					test.wantReason,
				)
			}
		})
	}
}

func TestWorkflowPersonalizedNextActionOnlyCreatesRealUserTodo(t *testing.T) {
	viewerID := workflowTestUUID(1)
	otherID := workflowTestUUID(2)
	nodeID := workflowTestUUID(3)
	instanceID := workflowTestUUID(4)

	nodeWithReason := func(code string) db.WorkflowNodeInstance {
		node := workflowNodeWithReason(t, "waiting", code)
		node.ID = nodeID
		node.WorkflowInstanceID = instanceID
		return node
	}
	roles := func(role string) workflowViewerNodeRoles {
		return workflowViewerNodeRoles{
			uuidToString(nodeID): {role: {}},
		}
	}

	tests := []struct {
		name               string
		instance           db.WorkflowInstance
		nodes              []db.WorkflowNodeInstance
		tasks              []db.WorkflowNodeTask
		isAdmin            bool
		roles              workflowViewerNodeRoles
		awaitingAcceptance bool
		wantAction         string
	}{
		{
			name: "unrelated member does not receive acceptance todo",
			instance: db.WorkflowInstance{
				ID: instanceID, Status: "running",
			},
			nodes:      []db.WorkflowNodeInstance{nodeWithReason("awaiting_acceptance")},
			wantAction: "view_current_activity",
		},
		{
			// Acceptance judges the run, so it is not gated on holding a node
			// role — canDecideWorkflowAcceptance enforces the approver.
			name: "acceptance reaches whoever is looking",
			instance: db.WorkflowInstance{
				ID: instanceID, Status: "running",
			},
			awaitingAcceptance: true,
			wantAction:         "review_acceptance",
		},
		{
			name: "node owner receives submission todo",
			instance: db.WorkflowInstance{
				ID: instanceID, Status: "running",
			},
			nodes:      []db.WorkflowNodeInstance{nodeWithReason("valid_submission_required")},
			roles:      roles("owner"),
			wantAction: "submit_result",
		},
		{
			name: "workflow starter can complete role setup",
			instance: db.WorkflowInstance{
				ID: instanceID, Status: "needs_setup",
				StartedByType: "member", StartedByID: viewerID,
			},
			wantAction: "configure_roles",
		},
		{
			name: "unrelated member cannot complete role setup",
			instance: db.WorkflowInstance{
				ID: instanceID, Status: "needs_setup",
				StartedByType: "member", StartedByID: otherID,
			},
			wantAction: "view_current_activity",
		},
		{
			name: "admin receives materialization repair todo",
			instance: db.WorkflowInstance{
				ID: instanceID, Status: "running",
			},
			tasks: []db.WorkflowNodeTask{{
				WorkflowNodeInstanceID: nodeID,
				MaterializationStatus:  "failed",
			}},
			isAdmin:    true,
			wantAction: "retry_materialization",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action, _ := workflowPersonalizedNextAction(
				test.instance,
				test.nodes,
				test.tasks,
				viewerID,
				test.isAdmin,
				test.roles,
				test.awaitingAcceptance,
			)
			if action != test.wantAction {
				t.Fatalf(
					"workflowPersonalizedNextAction() = %q, want %q",
					action,
					test.wantAction,
				)
			}
		})
	}
}
