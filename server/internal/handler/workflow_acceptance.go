package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) ListWorkflowAcceptances(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadWorkflowInstance(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListWorkflowAcceptances(r.Context(), db.ListWorkflowAcceptancesParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow acceptances")
		return
	}
	items := make([]workflowAcceptanceResponse, len(rows))
	for i, row := range rows {
		items[i] = workflowAcceptanceToResponse(row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"acceptances": items})
}

type decideWorkflowAcceptanceRequest struct {
	Status              string          `json:"status"`
	Reason              string          `json:"reason"`
	ReworkTargetNodeKey string          `json:"rework_target_node_key,omitempty"`
	Evidence            json.RawMessage `json:"evidence,omitempty"`
	IdempotencyKey      string          `json:"idempotency_key"`
}

func (h *Handler) DecideWorkflowAcceptance(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	var req decideWorkflowAcceptanceRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch req.Status {
	case "approved":
	case "rejected", "changes_requested":
		if strings.TrimSpace(req.Reason) == "" || strings.TrimSpace(req.ReworkTargetNodeKey) == "" {
			writeError(w, http.StatusBadRequest, "reason and rework_target_node_key are required")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "status must be approved, rejected, or changes_requested")
		return
	}
	evidence, ok := normalizeWorkflowJSONArray(w, req.Evidence, "evidence")
	if !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return
	}
	instance, ok := h.loadWorkflowInstance(w, r)
	if !ok {
		return
	}
	version, err := h.Queries.GetWorkflowVersionInWorkspace(r.Context(), db.GetWorkflowVersionInWorkspaceParams{
		ID: instance.WorkflowVersionID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow definition")
		return
	}
	definition, err := workflowdomain.ParseDefinition(version.Definition)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	plan, err := workflowdomain.BuildGraphPlan(definition)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if definition.Acceptance.Policy != "member" {
		writeError(w, http.StatusConflict, "workflow does not use member acceptance")
		return
	}
	if req.Status != "approved" &&
		!stringInSlice(req.ReworkTargetNodeKey, plan.AcceptanceReworkTargets()) {
		writeError(w, http.StatusBadRequest, "rework target must be an activity in this workflow")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user_id")
	if !ok {
		return
	}
	if allowed, err := h.canDecideWorkflowAcceptance(r.Context(), instance, definition, userUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify workflow acceptance permission")
		return
	} else if !allowed {
		writeError(w, http.StatusForbidden, "only the configured approver or a workspace admin can decide acceptance")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.LockWorkflowInstance(r.Context(), db.LockWorkflowInstanceParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	if existing, err := qtx.GetWorkflowAcceptanceByIdempotencyKey(r.Context(), db.GetWorkflowAcceptanceByIdempotencyKeyParams{
		WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID, IdempotencyKey: idempotencyKey,
	}); err == nil {
		tx.Rollback(r.Context())
		h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
		writeJSON(w, http.StatusOK, map[string]any{"acceptance": workflowAcceptanceToResponse(existing)})
		return
	}
	if locked.Status != "running" {
		writeError(w, http.StatusConflict, "workflow is not running")
		return
	}
	pending, err := qtx.GetLatestWorkflowAcceptance(r.Context(), db.GetLatestWorkflowAcceptanceParams{
		WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) || pending.Status != "pending" {
		writeError(w, http.StatusConflict, "workflow has no pending acceptance")
		return
	}
	nodes, err := qtx.ListWorkflowNodeInstances(r.Context(), db.ListWorkflowNodeInstancesParams{
		WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow nodes")
		return
	}
	revision, err := qtx.GetNextWorkflowAcceptanceRevision(r.Context(), db.GetNextWorkflowAcceptanceRevisionParams{
		WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate acceptance revision")
		return
	}
	var reworkTarget pgtype.Text
	if req.Status != "approved" {
		reworkTarget = pgtype.Text{String: req.ReworkTargetNodeKey, Valid: true}
	}
	decision, err := qtx.CreateWorkflowAcceptance(r.Context(), db.CreateWorkflowAcceptanceParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		Revision: revision, Status: req.Status,
		DecidedByType: pgtype.Text{String: "member", Valid: true}, DecidedByID: userUUID,
		Reason: strings.TrimSpace(req.Reason), ReworkTargetNodeKey: reworkTarget,
		Evidence: evidence, IdempotencyKey: idempotencyKey,
		DecidedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusConflict, "acceptance decision has already been recorded")
		return
	}
	var activatedNode db.WorkflowNodeInstance
	eventType := "acceptance.approved"
	if req.Status != "approved" {
		eventType = "acceptance.rework_requested"
		targetDefinition, exists := plan.Node(req.ReworkTargetNodeKey)
		if !exists || targetDefinition.Kind != "activity" {
			writeError(w, http.StatusBadRequest, "invalid rework target")
			return
		}

		targetNode, err := latestNodeByKey(nodes, targetDefinition.Key)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "rework target node not found")
			return
		}
		affected := plan.Descendants(targetDefinition.Key)
		affected[targetDefinition.Key] = struct{}{}
		for _, node := range nodes {
			if _, exists := affected[node.NodeKey]; !exists {
				continue
			}
			switch node.Status {
			case "completed", "active", "in_review", "waiting", "blocked", "skipped":
				if _, err := qtx.UpdateWorkflowNodeState(r.Context(), db.UpdateWorkflowNodeStateParams{
					Status: "superseded", WaitingReasons: []byte("[]"), ID: node.ID,
					WorkspaceID: locked.WorkspaceID, ExpectedStatus: node.Status,
				}); err != nil {
					writeError(w, http.StatusConflict, "workflow path changed; refresh and try again")
					return
				}
			}
		}
		snapshot, _ := json.Marshal(targetDefinition)
		activatedNode, err = qtx.CreateWorkflowNodeInstance(r.Context(), db.CreateWorkflowNodeInstanceParams{
			WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
			NodeKey: targetDefinition.Key, NodeKind: targetDefinition.Kind, Attempt: targetNode.Attempt + 1,
			NameSnapshot: targetDefinition.Name, DisplayOrder: targetNode.DisplayOrder,
			DefinitionSnapshot: snapshot, Status: "active",
		})
		if err != nil {
			writeError(w, http.StatusConflict, "failed to create rework attempt")
			return
		}
		assignments, err := qtx.ListWorkflowRoleAssignments(r.Context(), db.ListWorkflowRoleAssignmentsParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load workflow roles")
			return
		}
		roleMap := make(map[string]validatedWorkflowRoleAssignment, len(assignments))
		for _, assignment := range assignments {
			roleMap[assignment.RoleKey] = validatedWorkflowRoleAssignment{
				RoleKey: assignment.RoleKey, ActorType: assignment.ActorType,
				ActorID: assignment.ActorID, Source: assignment.Source,
			}
		}
		needsSetup, activationErr := createWorkflowNodeActivationRecords(
			r.Context(), qtx, locked.WorkspaceID, locked,
			activatedNode, targetDefinition, definition, roleMap,
		)
		if activationErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to activate rework node")
			return
		}
		if needsSetup {
			reasons := workflowdomain.EncodeWaitingReasons([]workflowdomain.WaitingReason{{
				Code:    "executor_needs_setup",
				Message: "One or more workflow tasks require an executor",
			}})
			activatedNode, err = qtx.UpdateWorkflowNodeState(
				r.Context(),
				db.UpdateWorkflowNodeStateParams{
					Status: "blocked", WaitingReasons: reasons, MarkReconciled: true,
					ID: activatedNode.ID, WorkspaceID: locked.WorkspaceID,
					ExpectedStatus: "active",
				},
			)
			if err != nil {
				writeError(w, http.StatusConflict, "failed to pause rework for executor setup")
				return
			}
		}
	}
	targetInstanceStatus := locked.Status
	if activatedNode.ID.Valid && workflowNodeBlockedForExecutor(activatedNode) {
		targetInstanceStatus = "needs_setup"
	}
	updated, err := qtx.UpdateWorkflowInstanceState(r.Context(), db.UpdateWorkflowInstanceStateParams{
		Status: targetInstanceStatus, MarkReconciled: true, ID: locked.ID,
		WorkspaceID: locked.WorkspaceID, ExpectedRevision: locked.Revision,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"acceptance_id": uuidToString(decision.ID), "status": decision.Status,
		"reason": decision.Reason, "rework_target_node_key": textToPtr(decision.ReworkTargetNodeKey),
	})
	if _, err := qtx.CreateWorkflowEvent(r.Context(), db.CreateWorkflowEventParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		WorkflowNodeInstanceID: activatedNode.ID, EventType: eventType,
		ActorType: "member", ActorID: userUUID, IdempotencyKey: idempotencyKey, Payload: payload,
	}); err != nil {
		writeError(w, http.StatusConflict, "acceptance decision has already been recorded")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit acceptance decision")
		return
	}
	h.Metrics.RecordWorkflowAcceptance(decision.Status)
	if decision.Status == "approved" {
		if decision.Revision == 2 {
			h.Metrics.RecordWorkflowAcceptance("first_pass")
		} else {
			h.Metrics.RecordWorkflowAcceptance("approved_after_rework")
		}
	} else {
		h.Metrics.RecordWorkflowAcceptance("rework")
	}
	h.Metrics.RecordWorkflowHumanIntervention("acceptance")
	h.recordWorkflowInstanceStatusTransition(locked.Status, updated.Status)
	if activatedNode.ID.Valid {
		h.recordWorkflowNodesActivated(
			r.Context(),
			[]db.WorkflowNodeInstance{activatedNode},
		)
		h.applyWorkflowNodeEnterActions(
			r.Context(), updated, definition,
			[]db.WorkflowNodeInstance{activatedNode},
		)
		h.materializeWorkflowNodeTasks(r.Context(), locked.WorkspaceID, updated, activatedNode)
	}
	if req.Status == "approved" {
		if reconciled, reconcileErr := h.reconcileWorkflowInstance(
			r.Context(), locked.WorkspaceID, locked.ID, "member", userUUID, "acceptance:"+uuidToString(decision.ID),
		); reconcileErr == nil {
			updated = reconciled
		}
	}
	h.publishWorkflowRealtime(
		protocol.EventWorkflowAcceptanceUpdated,
		uuidToString(locked.WorkspaceID), "member", userID,
		map[string]any{
			"workflow_instance_id":      uuidToString(locked.ID),
			"workflow_node_instance_id": uuidToString(activatedNode.ID),
			"workflow_acceptance_id":    uuidToString(decision.ID),
		},
	)
	h.publishWorkflowInstanceUpdated(
		uuidToString(locked.WorkspaceID), "member", userID,
		uuidToString(locked.ID), uuidToString(activatedNode.ID),
	)
	writeJSON(w, http.StatusCreated, map[string]any{
		"acceptance": workflowAcceptanceToResponse(decision),
		"instance":   h.workflowInstanceToRuntimeResponse(r.Context(), updated),
	})
}

func (h *Handler) canDecideWorkflowAcceptance(
	ctx context.Context,
	instance db.WorkflowInstance,
	definition workflowdomain.Definition,
	userID pgtype.UUID,
) (bool, error) {
	assignments, err := h.Queries.ListWorkflowRoleAssignments(ctx, db.ListWorkflowRoleAssignmentsParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		if assignment.RoleKey == definition.Acceptance.ApproverRole &&
			assignment.ActorType == "member" && assignment.ActorID == userID {
			return true, nil
		}
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID: userID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		return false, err
	}
	return roleAllowed(member.Role, "owner", "admin"), nil
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
