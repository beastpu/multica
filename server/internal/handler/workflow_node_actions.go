package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type createWorkflowNodeIssueRequest struct {
	Title          string `json:"title"`
	Description    string `json:"description,omitempty"`
	InitialStatus  string `json:"initial_status,omitempty"`
	Priority       string `json:"priority,omitempty"`
	AssigneeType   string `json:"assignee_type,omitempty"`
	AssigneeID     string `json:"assignee_id,omitempty"`
	Required       bool   `json:"required,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (h *Handler) CreateWorkflowNodeIssue(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	var req createWorkflowNodeIssueRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return
	}
	if req.InitialStatus == "" {
		req.InitialStatus = "todo"
	}
	if !validateIssueEnum(w, "initial_status", req.InitialStatus, validIssueStatuses) {
		return
	}
	if req.Priority == "" {
		req.Priority = "none"
	}
	if !validateIssueEnum(w, "priority", req.Priority, validIssuePriorities) {
		return
	}
	node, instance, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	if instance.Status != "running" ||
		(node.Status != "active" && node.Status != "waiting" && node.Status != "blocked") {
		writeError(w, http.StatusConflict, "workflow node does not accept new issues")
		return
	}
	var nodeDefinition workflowdomain.NodeDefinition
	if err := json.Unmarshal(node.DefinitionSnapshot, &nodeDefinition); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow node snapshot")
		return
	}
	if nodeDefinition.Kind != "activity" || !workflowdomain.AllowsDynamicIssues(nodeDefinition) {
		writeError(w, http.StatusConflict, "workflow node does not allow dynamic issues")
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
	allowed, err := h.canManageWorkflowNode(r.Context(), instance, nodeDefinition, userUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify workflow node permission")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "only the node owner or a workspace admin can create a dynamic issue")
		return
	}
	if (req.AssigneeType == "") != (req.AssigneeID == "") {
		writeError(w, http.StatusBadRequest, "assignee_type and assignee_id must be provided together")
		return
	}
	var requestedAssignee *validatedWorkflowRoleAssignment
	if req.AssigneeType != "" {
		assigneeID, parseOK := parseUUIDOrBadRequest(w, req.AssigneeID, "assignee_id")
		if !parseOK {
			return
		}
		if status, message := h.validateAssigneePair(
			r.Context(),
			r,
			uuidToString(node.WorkspaceID),
			pgtype.Text{String: req.AssigneeType, Valid: true},
			assigneeID,
		); status != 0 {
			writeError(w, status, message)
			return
		}
		requestedAssignee = &validatedWorkflowRoleAssignment{
			ActorType: req.AssigneeType,
			ActorID:   assigneeID,
		}
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.LockWorkflowInstance(
		r.Context(),
		db.LockWorkflowInstanceParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID},
	)
	if err != nil {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	if task, replayed := workflowTaskReplay(
		w, r, qtx, locked, req.IdempotencyKey, "dynamic_issue",
	); replayed {
		if task.ID.Valid {
			writeJSON(w, http.StatusOK, map[string]any{"task": workflowTaskToResponse(task)})
		}
		return
	}
	if locked.Status != "running" {
		writeError(w, http.StatusConflict, "workflow is not running")
		return
	}
	roleRows, err := qtx.ListWorkflowRoleAssignments(
		r.Context(),
		db.ListWorkflowRoleAssignmentsParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow roles")
		return
	}
	roleMap := make(map[string]validatedWorkflowRoleAssignment, len(roleRows))
	for _, role := range roleRows {
		roleMap[role.RoleKey] = validatedWorkflowRoleAssignment{
			RoleKey: role.RoleKey, ActorType: role.ActorType,
			ActorID: role.ActorID, Source: role.Source,
		}
	}
	taskKey := randomID()
	taskDefinition := workflowdomain.IssueTemplate{
		Key: taskKey, Title: req.Title, Description: strings.TrimSpace(req.Description),
		Required: req.Required, InitialStatus: req.InitialStatus, Priority: req.Priority,
	}
	var decision workflowExecutorDecision
	if requestedAssignee != nil {
		decision = workflowExecutorDecision{
			Assignment: requestedAssignee, Strategy: "manual",
			Candidates: workflowExecutorCandidates(*requestedAssignee),
			Reason:     "Executor was selected while declaring a dynamic issue",
			Snapshot: workflowExecutorSnapshot(
				"manual", "", "", "", "",
			),
		}
	} else {
		decision, err = resolveWorkflowTaskExecutor(
			r.Context(), qtx, locked.WorkspaceID, locked,
			nodeDefinition, taskDefinition, roleMap,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve workflow executor")
			return
		}
	}
	snapshot, _ := json.Marshal(taskDefinition)
	task, err := qtx.CreateWorkflowNodeTask(
		r.Context(),
		db.CreateWorkflowNodeTaskParams{
			WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
			WorkflowNodeInstanceID: node.ID, TaskKey: taskKey, Source: "dynamic",
			Required: req.Required, DefinitionSnapshot: snapshot,
			MaterializationStatus: "pending_materialization",
			CreatedByType:         "member", CreatedByID: userUUID,
		},
	)
	if err != nil {
		writeError(w, http.StatusConflict, "dynamic workflow task changed; retry the request")
		return
	}
	resolution, err := createWorkflowExecutorResolution(
		r.Context(), qtx, locked.WorkspaceID, locked, node, task, decision,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record workflow executor resolution")
		return
	}
	updatedInstance := locked
	if decision.Assignment != nil {
		task, err = qtx.GetWorkflowNodeTaskInWorkspace(
			r.Context(),
			db.GetWorkflowNodeTaskInWorkspaceParams{
				ID: task.ID, WorkspaceID: task.WorkspaceID,
			},
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to reload workflow task")
			return
		}
	} else {
		if node.Status != "blocked" {
			reasons := workflowdomain.EncodeWaitingReasons([]workflowdomain.WaitingReason{{
				Code:    "executor_needs_setup",
				Message: "One or more workflow tasks require an executor",
			}})
			node, err = qtx.UpdateWorkflowNodeState(
				r.Context(),
				db.UpdateWorkflowNodeStateParams{
					Status: "blocked", WaitingReasons: reasons, MarkReconciled: true,
					ID: node.ID, WorkspaceID: node.WorkspaceID,
					ExpectedStatus: node.Status,
				},
			)
			if err != nil {
				writeError(w, http.StatusConflict, "workflow node changed; refresh and try again")
				return
			}
		}
		updatedInstance, err = qtx.UpdateWorkflowInstanceState(
			r.Context(),
			db.UpdateWorkflowInstanceStateParams{
				Status: "needs_setup", MarkReconciled: true,
				ID: locked.ID, WorkspaceID: locked.WorkspaceID,
				ExpectedRevision: locked.Revision,
			},
		)
		if err != nil {
			writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
			return
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"action": "dynamic_issue", "task_id": uuidToString(task.ID),
		"resolution_id":   uuidToString(resolution.ID),
		"executor_status": resolution.Status,
	})
	if _, err := qtx.CreateWorkflowEvent(r.Context(), db.CreateWorkflowEventParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		WorkflowNodeInstanceID: node.ID, EventType: "node.task_declared",
		ActorType: "member", ActorID: userUUID,
		IdempotencyKey: req.IdempotencyKey, Payload: payload,
	}); err != nil {
		writeError(w, http.StatusConflict, "dynamic workflow issue has already been declared")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit dynamic workflow issue")
		return
	}
	h.Metrics.RecordWorkflowExecutorResolution(
		decision.Strategy,
		resolution.Status,
	)
	h.Metrics.RecordWorkflowHumanIntervention("declare_dynamic_issue")
	h.recordWorkflowInstanceStatusTransition(
		locked.Status,
		updatedInstance.Status,
	)
	if node.Status == "blocked" {
		h.recordWorkflowNodeTransition(node, "blocked")
	}
	if decision.Assignment != nil {
		_ = h.materializeWorkflowTask(
			r.Context(), node.WorkspaceID, updatedInstance, node, task,
		)
	}
	task, _ = h.Queries.GetWorkflowNodeTaskInWorkspace(
		r.Context(),
		db.GetWorkflowNodeTaskInWorkspaceParams{ID: task.ID, WorkspaceID: task.WorkspaceID},
	)
	h.publishWorkflowTaskUpdated(
		uuidToString(node.WorkspaceID), "member", userID,
		uuidToString(instance.ID), uuidToString(node.ID), uuidToString(task.ID),
	)
	if decision.Assignment == nil {
		h.publishWorkflowInstanceUpdated(
			uuidToString(node.WorkspaceID), "member", userID,
			uuidToString(instance.ID), uuidToString(node.ID),
		)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"task": workflowTaskToResponse(task)})
}

type resolveWorkflowExecutorRequest struct {
	TaskID         string `json:"task_id"`
	ActorType      string `json:"actor_type"`
	ActorID        string `json:"actor_id"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (h *Handler) ResolveWorkflowNodeExecutor(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	var req resolveWorkflowExecutorRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return
	}
	node, instance, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	taskID, ok := parseUUIDOrBadRequest(w, req.TaskID, "task_id")
	if !ok {
		return
	}
	task, err := h.Queries.GetWorkflowNodeTaskInWorkspace(
		r.Context(),
		db.GetWorkflowNodeTaskInWorkspaceParams{ID: taskID, WorkspaceID: node.WorkspaceID},
	)
	if errors.Is(err, pgx.ErrNoRows) || task.WorkflowNodeInstanceID != node.ID {
		writeError(w, http.StatusNotFound, "workflow node task not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow node task")
		return
	}
	actorID, ok := parseUUIDOrBadRequest(w, req.ActorID, "actor_id")
	if !ok {
		return
	}
	if status, message := h.validateAssigneePair(
		r.Context(),
		r,
		uuidToString(node.WorkspaceID),
		pgtype.Text{String: req.ActorType, Valid: true},
		actorID,
	); status != 0 {
		writeError(w, status, message)
		return
	}
	var nodeDefinition workflowdomain.NodeDefinition
	if err := json.Unmarshal(node.DefinitionSnapshot, &nodeDefinition); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow node snapshot")
		return
	}
	version, err := h.Queries.GetWorkflowTemplateVersionInWorkspace(
		r.Context(),
		db.GetWorkflowTemplateVersionInWorkspaceParams{
			ID: instance.TemplateVersionID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow definition")
		return
	}
	definition, err := workflowdomain.ParseDefinition(version.Definition)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow definition")
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
	allowed, err := h.canManageWorkflowNode(r.Context(), instance, nodeDefinition, userUUID)
	if err != nil || !allowed {
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to verify workflow node permission")
		} else {
			writeError(w, http.StatusForbidden, "only the node owner or a workspace admin can resolve an executor")
		}
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.LockWorkflowInstance(
		r.Context(),
		db.LockWorkflowInstanceParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID},
	)
	if err != nil || (locked.Status != "running" && locked.Status != "needs_setup") {
		writeError(w, http.StatusConflict, "workflow does not accept executor changes")
		return
	}
	if event, eventErr := qtx.GetWorkflowEventByIdempotencyKey(
		r.Context(),
		db.GetWorkflowEventByIdempotencyKeyParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
			IdempotencyKey: req.IdempotencyKey,
		},
	); eventErr == nil {
		var payload struct {
			Action       string `json:"action"`
			ResolutionID string `json:"resolution_id"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil && payload.Action == "resolve_executor" {
			resolutionID, parseOK := parseUUIDOrBadRequest(w, payload.ResolutionID, "resolution_id")
			if !parseOK {
				return
			}
			existing, getErr := qtx.GetWorkflowExecutorResolutionInWorkspace(
				r.Context(),
				db.GetWorkflowExecutorResolutionInWorkspaceParams{
					ID: resolutionID, WorkspaceID: locked.WorkspaceID,
				},
			)
			if getErr == nil && existing.WorkflowNodeTaskID == task.ID {
				h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
				writeJSON(w, http.StatusOK, map[string]any{
					"resolution": workflowExecutorResolutionToResponse(existing),
				})
				return
			}
		}
		writeError(w, http.StatusConflict, "idempotency_key was already used for another workflow action")
		return
	}
	definitionSnapshot, _ := json.Marshal(map[string]any{
		"strategy": "manual", "task_id": req.TaskID,
	})
	resolution, err := qtx.CreateWorkflowExecutorResolution(
		r.Context(),
		db.CreateWorkflowExecutorResolutionParams{
			WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
			WorkflowNodeInstanceID: node.ID, WorkflowNodeTaskID: task.ID,
			Strategy: "manual", Status: "resolved",
			ActorType: pgtype.Text{String: req.ActorType, Valid: true},
			ActorID:   actorID, Candidates: []byte("[]"),
			Reason: strings.TrimSpace(req.Reason), DefinitionSnapshot: definitionSnapshot,
			ResolvedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create executor resolution")
		return
	}
	task, err = qtx.SetWorkflowNodeTaskExecutorResolution(
		r.Context(),
		db.SetWorkflowNodeTaskExecutorResolutionParams{
			ExecutorResolutionID: resolution.ID, ID: task.ID, WorkspaceID: task.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to bind executor resolution")
		return
	}
	if task.MaterializationStatus == "failed" {
		task, err = qtx.ResetWorkflowNodeTaskMaterialization(
			r.Context(),
			db.ResetWorkflowNodeTaskMaterializationParams{ID: task.ID, WorkspaceID: task.WorkspaceID},
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to reset workflow task")
			return
		}
	}
	updatedNode := node
	nodeNeedsSetup, err := workflowNodeNeedsExecutorSetup(
		r.Context(), qtx, locked.WorkspaceID, node.ID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check workflow executor setup")
		return
	}
	if !nodeNeedsSetup && workflowNodeBlockedForExecutor(node) {
		updatedNode, err = qtx.UpdateWorkflowNodeState(
			r.Context(),
			db.UpdateWorkflowNodeStateParams{
				Status: "active", WaitingReasons: []byte("[]"), MarkReconciled: true,
				ID: node.ID, WorkspaceID: node.WorkspaceID,
				ExpectedStatus: node.Status,
			},
		)
		if err != nil {
			writeError(w, http.StatusConflict, "workflow node changed; refresh and try again")
			return
		}
	}
	updatedInstance := locked
	if locked.Status == "needs_setup" {
		roleRows, roleErr := qtx.ListWorkflowRoleAssignments(
			r.Context(),
			db.ListWorkflowRoleAssignmentsParams{
				WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
			},
		)
		if roleErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load workflow roles")
			return
		}
		instanceNeedsSetup, setupErr := workflowInstanceNeedsExecutorSetup(
			r.Context(), qtx, locked,
		)
		if setupErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to check workflow setup")
			return
		}
		if workflowInstanceHasRequiredRoles(definition, roleRows) && !instanceNeedsSetup {
			updatedInstance, err = qtx.UpdateWorkflowInstanceState(
				r.Context(),
				db.UpdateWorkflowInstanceStateParams{
					Status: "running", MarkReconciled: true,
					ID: locked.ID, WorkspaceID: locked.WorkspaceID,
					ExpectedRevision: locked.Revision,
				},
			)
			if err != nil {
				writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
				return
			}
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"action": "resolve_executor", "resolution_id": uuidToString(resolution.ID),
		"task_id": uuidToString(task.ID),
	})
	if _, err := qtx.CreateWorkflowEvent(r.Context(), db.CreateWorkflowEventParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		WorkflowNodeInstanceID: node.ID, EventType: "node.executor_resolved",
		ActorType: "member", ActorID: userUUID,
		IdempotencyKey: req.IdempotencyKey, Payload: payload,
	}); err != nil {
		writeError(w, http.StatusConflict, "executor resolution has already been recorded")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit executor resolution")
		return
	}
	h.Metrics.RecordWorkflowExecutorResolution("manual", "resolved")
	h.Metrics.RecordWorkflowHumanIntervention("resolve_executor")
	h.recordWorkflowInstanceStatusTransition(
		locked.Status,
		updatedInstance.Status,
	)
	if updatedInstance.Status == "running" &&
		task.MaterializationStatus == "pending_materialization" {
		_ = h.materializeWorkflowTask(
			r.Context(), node.WorkspaceID, updatedInstance, updatedNode, task,
		)
	}
	if updatedInstance.Status == "running" && h.WorkflowMaterializer != nil {
		h.WorkflowMaterializer.Notify()
	}
	if updatedNode.ID.Valid && updatedNode.Status != node.Status {
		h.publishWorkflowNodeUpdated(
			uuidToString(node.WorkspaceID), "member", userID,
			uuidToString(instance.ID), uuidToString(node.ID),
		)
	}
	if updatedInstance.Status != locked.Status {
		h.publishWorkflowInstanceUpdated(
			uuidToString(node.WorkspaceID), "member", userID,
			uuidToString(instance.ID), uuidToString(node.ID),
		)
	}
	h.publishWorkflowRealtime(
		protocol.EventWorkflowExecutorResolutionUpdated,
		uuidToString(node.WorkspaceID), "member", userID,
		map[string]any{
			"workflow_instance_id":      uuidToString(instance.ID),
			"workflow_node_instance_id": uuidToString(node.ID),
			"workflow_node_task_id":     uuidToString(task.ID),
			"workflow_resolution_id":    uuidToString(resolution.ID),
		},
	)
	writeJSON(w, http.StatusCreated, map[string]any{
		"resolution": workflowExecutorResolutionToResponse(resolution),
	})
}

type workflowNodeTransitionRequest struct {
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (h *Handler) CompleteWorkflowNode(w http.ResponseWriter, r *http.Request) {
	h.transitionWorkflowNode(w, r, "complete")
}

func (h *Handler) SkipWorkflowNode(w http.ResponseWriter, r *http.Request) {
	h.transitionWorkflowNode(w, r, "skip")
}

func (h *Handler) RollbackWorkflowNode(w http.ResponseWriter, r *http.Request) {
	h.transitionWorkflowNode(w, r, "rollback")
}

func (h *Handler) transitionWorkflowNode(
	w http.ResponseWriter,
	r *http.Request,
	action string,
) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	var req workflowNodeTransitionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	if req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return
	}
	node, instance, ok := h.loadWorkflowNode(w, r)
	if !ok {
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
	var nodeDefinition workflowdomain.NodeDefinition
	if err := json.Unmarshal(node.DefinitionSnapshot, &nodeDefinition); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow node snapshot")
		return
	}
	manualCompletion := action == "complete" &&
		workflowdomain.RequiresManualCompletion(nodeDefinition)
	nodeOwnerAction := manualCompletion || action == "rollback"
	if nodeOwnerAction {
		allowed, permissionErr := h.canManageWorkflowNode(
			r.Context(), instance, nodeDefinition, userUUID,
		)
		if permissionErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to verify workflow node permission")
			return
		}
		if !allowed {
			writeError(
				w,
				http.StatusForbidden,
				"only the node owner or a workspace admin can perform this action",
			)
			return
		}
	} else if _, roleOK := h.requireWorkspaceRole(
		w, r, h.resolveWorkspaceID(r), "workspace not found", "owner", "admin",
	); !roleOK {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.LockWorkflowInstance(
		r.Context(),
		db.LockWorkflowInstanceParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID},
	)
	if err != nil {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	if _, eventErr := qtx.GetWorkflowEventByIdempotencyKey(
		r.Context(),
		db.GetWorkflowEventByIdempotencyKeyParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
			IdempotencyKey: req.IdempotencyKey,
		},
	); eventErr == nil {
		tx.Rollback(r.Context())
		h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
		h.writeWorkflowInstanceDetail(w, r, locked, http.StatusOK)
		return
	} else if !errors.Is(eventErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to check workflow idempotency")
		return
	}
	if locked.Status != "running" {
		writeError(w, http.StatusConflict, "workflow is not running")
		return
	}
	version, err := qtx.GetWorkflowTemplateVersionInWorkspace(
		r.Context(),
		db.GetWorkflowTemplateVersionInWorkspaceParams{
			ID: locked.TemplateVersionID, WorkspaceID: locked.WorkspaceID,
		},
	)
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
	nodes, err := qtx.ListWorkflowNodeInstances(
		r.Context(),
		db.ListWorkflowNodeInstancesParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow nodes")
		return
	}
	roleRows, err := qtx.ListWorkflowRoleAssignments(
		r.Context(),
		db.ListWorkflowRoleAssignmentsParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow roles")
		return
	}
	roleMap := make(map[string]validatedWorkflowRoleAssignment, len(roleRows))
	for _, role := range roleRows {
		roleMap[role.RoleKey] = validatedWorkflowRoleAssignment{
			RoleKey: role.RoleKey, ActorType: role.ActorType,
			ActorID: role.ActorID, Source: role.Source,
		}
	}
	var activatedNode db.WorkflowNodeInstance
	var activatedNodes []db.WorkflowNodeInstance
	updated := locked
	switch action {
	case "complete", "skip":
		latest, findErr := latestNodeByKey(nodes, node.NodeKey)
		if findErr != nil || latest.ID != node.ID || !workflowNodeIsOpen(latest) {
			writeError(w, http.StatusConflict, "only an active workflow node can be completed or skipped")
			return
		}
		node = latest
		if manualCompletion {
			revision, revisionErr := qtx.GetNextWorkflowVerdictRevision(
				r.Context(),
				db.GetNextWorkflowVerdictRevisionParams{
					WorkflowNodeInstanceID: node.ID,
					WorkspaceID:            node.WorkspaceID,
				},
			)
			if revisionErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to allocate manual completion revision")
				return
			}
			basis, _ := json.Marshal(map[string]any{
				"kind":   "manual_completion",
				"reason": req.Reason,
			})
			definitionSnapshot, _ := json.Marshal(map[string]any{
				"kind": "manual_completion",
			})
			verdict, verdictErr := qtx.CreateWorkflowVerdict(
				r.Context(),
				db.CreateWorkflowVerdictParams{
					WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
					WorkflowNodeInstanceID: node.ID, Revision: revision,
					Result: "pass", Reason: req.Reason, Evidence: []byte("[]"),
					Basis: basis, EvaluatorType: "member", EvaluatorID: userUUID,
					DefinitionSnapshot: definitionSnapshot,
				},
			)
			if verdictErr != nil {
				writeError(w, http.StatusConflict, "manual completion changed; retry the request")
				return
			}
			if err = qtx.SetWorkflowNodeLatestVerdict(
				r.Context(),
				db.SetWorkflowNodeLatestVerdictParams{
					LatestVerdictID: verdict.ID,
					ID:              node.ID,
					WorkspaceID:     node.WorkspaceID,
				},
			); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to record manual completion")
				return
			}
			break
		}
		targetStatus := "completed"
		if action == "skip" {
			targetStatus = "skipped"
		}
		if _, err := qtx.UpdateWorkflowNodeState(
			r.Context(),
			db.UpdateWorkflowNodeStateParams{
				Status: targetStatus, WaitingReasons: []byte("[]"),
				MarkReconciled: true, ID: node.ID, WorkspaceID: node.WorkspaceID,
				ExpectedStatus: node.Status,
			},
		); err != nil {
			writeError(w, http.StatusConflict, "workflow node changed; refresh and try again")
			return
		}
		for index := range nodes {
			if nodes[index].ID == node.ID {
				nodes[index].Status = targetStatus
			}
		}
		if action == "skip" {
			internalPayload, _ := json.Marshal(map[string]any{
				"reason": req.Reason, "node_key": node.NodeKey,
			})
			if _, err = qtx.CreateWorkflowEvent(r.Context(), db.CreateWorkflowEventParams{
				WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
				WorkflowNodeInstanceID: node.ID, EventType: "node.explicit_skip",
				ActorType: "member", ActorID: userUUID,
				IdempotencyKey: req.IdempotencyKey + ":routing",
				Payload:        internalPayload,
			}); err != nil {
				writeError(w, http.StatusConflict, "workflow node skip has already been recorded")
				return
			}
		}
		propagated, propagateErr := h.propagateWorkflowGraph(
			r.Context(), qtx, locked.WorkspaceID, locked, definition, plan,
			roleMap, nodes, "member", userUUID,
		)
		if propagateErr != nil {
			writeError(w, http.StatusConflict, "failed to advance workflow graph")
			return
		}
		activatedNodes = propagated.Activated
		if len(activatedNodes) > 0 {
			activatedNode = activatedNodes[0]
		}
		targetInstanceStatus := locked.Status
		var result []byte
		if propagated.NeedsSetup {
			targetInstanceStatus = "needs_setup"
		} else if propagated.CanComplete {
			targetInstanceStatus = "completed"
			result, _ = json.Marshal(map[string]any{
				"forced_action": action, "node_key": node.NodeKey,
			})
		}
		updated, err = qtx.UpdateWorkflowInstanceState(
			r.Context(),
			db.UpdateWorkflowInstanceStateParams{
				Status: targetInstanceStatus, Result: result, MarkReconciled: true,
				ID: locked.ID, WorkspaceID: locked.WorkspaceID,
				ExpectedRevision: locked.Revision,
			},
		)
	case "rollback":
		targetDefinition, exists := plan.Node(node.NodeKey)
		if !exists || targetDefinition.Kind != "activity" {
			writeError(w, http.StatusBadRequest, "rollback target must be an activity")
			return
		}
		activeNodes := workflowActiveNodes(latestWorkflowNodesByKey(nodes), plan)
		if len(activeNodes) == 0 {
			writeError(w, http.StatusConflict, "workflow has no current node")
			return
		}
		precedesCurrent := false
		for _, current := range activeNodes {
			precedesCurrent = precedesCurrent || plan.IsAncestor(node.NodeKey, current.NodeKey)
		}
		if !precedesCurrent {
			writeError(w, http.StatusConflict, "rollback target must precede the current node")
			return
		}
		latestTarget, findErr := latestNodeByKey(nodes, targetDefinition.Key)
		if findErr != nil {
			writeError(w, http.StatusInternalServerError, "rollback target not found")
			return
		}
		affected := plan.Descendants(node.NodeKey)
		affected[node.NodeKey] = struct{}{}
		for _, candidate := range nodes {
			if _, exists := affected[candidate.NodeKey]; !exists {
				continue
			}
			switch candidate.Status {
			case "active", "waiting", "blocked", "completed", "skipped":
				if _, updateErr := qtx.UpdateWorkflowNodeState(
					r.Context(),
					db.UpdateWorkflowNodeStateParams{
						Status: "superseded", WaitingReasons: []byte("[]"),
						ID: candidate.ID, WorkspaceID: locked.WorkspaceID,
						ExpectedStatus: candidate.Status,
					},
				); updateErr != nil {
					writeError(w, http.StatusConflict, "workflow path changed; refresh and try again")
					return
				}
			}
		}
		snapshot, _ := json.Marshal(targetDefinition)
		activatedNode, err = qtx.CreateWorkflowNodeInstance(
			r.Context(),
			db.CreateWorkflowNodeInstanceParams{
				WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
				NodeKey: targetDefinition.Key, NodeKind: targetDefinition.Kind,
				Attempt:      latestTarget.Attempt + 1,
				NameSnapshot: targetDefinition.Name, DisplayOrder: latestTarget.DisplayOrder,
				DefinitionSnapshot: snapshot, Status: "active",
			},
		)
		needsSetup := false
		if err == nil {
			needsSetup, err = createWorkflowNodeActivationRecords(
				r.Context(), qtx, locked.WorkspaceID, locked,
				activatedNode, targetDefinition, definition, roleMap,
			)
		}
		if err == nil && needsSetup {
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
		}
		if err == nil {
			targetStatus := "running"
			if needsSetup {
				targetStatus = "needs_setup"
			}
			updated, err = qtx.UpdateWorkflowInstanceState(
				r.Context(),
				db.UpdateWorkflowInstanceStateParams{
					Status: targetStatus, MarkReconciled: true,
					ID: locked.ID, WorkspaceID: locked.WorkspaceID,
					ExpectedRevision: locked.Revision,
				},
			)
		}
	}
	if err != nil {
		writeError(w, http.StatusConflict, "failed to apply workflow node transition")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"action": action, "node_key": node.NodeKey, "node_instance_id": uuidToString(node.ID),
		"activated_node_instance_id": uuidToString(activatedNode.ID), "reason": req.Reason,
		"manual_completion": manualCompletion,
	})
	if _, err := qtx.CreateWorkflowEvent(r.Context(), db.CreateWorkflowEventParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		WorkflowNodeInstanceID: node.ID, EventType: "node." + action,
		ActorType: "member", ActorID: userUUID,
		IdempotencyKey: req.IdempotencyKey, Payload: payload,
	}); err != nil {
		writeError(w, http.StatusConflict, "workflow node action has already been recorded")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow node transition")
		return
	}
	h.Metrics.RecordWorkflowHumanIntervention(action)
	h.recordWorkflowInstanceStatusTransition(locked.Status, updated.Status)
	switch {
	case manualCompletion:
		h.Metrics.RecordWorkflowVerdict("member", "pass")
	case action == "complete":
		h.recordWorkflowNodeTransition(node, "completed")
	case action == "skip":
		h.recordWorkflowNodeTransition(node, "skipped")
	}
	if updated.Status == "completed" {
		_ = h.updateManagedWorkflowHostStatus(r.Context(), updated, "done")
	}
	if action == "rollback" && activatedNode.ID.Valid && len(activatedNodes) == 0 {
		activatedNodes = append(activatedNodes, activatedNode)
	}
	h.recordWorkflowNodesActivated(r.Context(), activatedNodes)
	for _, activated := range activatedNodes {
		h.materializeWorkflowNodeTasks(
			r.Context(), locked.WorkspaceID, updated, activated,
		)
	}
	if action == "complete" || action == "skip" {
		reconciled, _ := h.reconcileWorkflowInstance(
			r.Context(), locked.WorkspaceID, updated.ID, "member", userUUID,
			"forced-transition:"+uuidToString(node.ID),
		)
		if reconciled.ID.Valid {
			updated = reconciled
		}
	}
	h.publishWorkflowNodeUpdated(
		uuidToString(locked.WorkspaceID), "member", userID,
		uuidToString(locked.ID), uuidToString(node.ID),
	)
	h.publishWorkflowInstanceUpdated(
		uuidToString(locked.WorkspaceID), "member", userID,
		uuidToString(locked.ID), uuidToString(activatedNode.ID),
	)
	h.writeWorkflowInstanceDetail(w, r, updated, http.StatusOK)
}

type workflowTaskActionRequest struct {
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (h *Handler) RetryWorkflowNodeTask(w http.ResponseWriter, r *http.Request) {
	h.changeWorkflowNodeTask(w, r, "retry")
}

func (h *Handler) DetachWorkflowNodeTask(w http.ResponseWriter, r *http.Request) {
	h.changeWorkflowNodeTask(w, r, "detach")
}

func (h *Handler) changeWorkflowNodeTask(w http.ResponseWriter, r *http.Request, action string) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	var req workflowTaskActionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	task, node, instance, ok := h.loadWorkflowTask(w, r)
	if !ok {
		return
	}
	if _, roleOK := h.requireWorkspaceRole(
		w, r, h.resolveWorkspaceID(r), "workspace not found", "owner", "admin",
	); !roleOK {
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
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.LockWorkflowInstance(
		r.Context(),
		db.LockWorkflowInstanceParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID},
	)
	if err != nil {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	if existing, replayed := workflowTaskReplay(
		w, r, qtx, locked, req.IdempotencyKey, action,
	); replayed {
		if existing.ID.Valid {
			h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
			writeJSON(w, http.StatusOK, map[string]any{"task": workflowTaskToResponse(existing)})
		}
		return
	}
	switch action {
	case "retry":
		if task.MaterializationStatus != "failed" && task.MaterializationStatus != "materializing" {
			writeError(w, http.StatusConflict, "only failed or stuck materialization can be retried")
			return
		}
		task, err = qtx.ResetWorkflowNodeTaskMaterialization(
			r.Context(),
			db.ResetWorkflowNodeTaskMaterializationParams{ID: task.ID, WorkspaceID: task.WorkspaceID},
		)
	case "detach":
		if !task.IssueID.Valid {
			writeError(w, http.StatusConflict, "workflow task has no issue to detach")
			return
		}
		if err = qtx.ClearWorkflowIssueOrigin(
			r.Context(),
			db.ClearWorkflowIssueOriginParams{
				IssueID: task.IssueID, WorkspaceID: task.WorkspaceID,
				WorkflowNodeTaskID: task.ID,
			},
		); err == nil {
			task, err = qtx.DetachWorkflowNodeTask(
				r.Context(),
				db.DetachWorkflowNodeTaskParams{ID: task.ID, WorkspaceID: task.WorkspaceID},
			)
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update workflow task")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"action": action, "task_id": uuidToString(task.ID),
		"reason": strings.TrimSpace(req.Reason),
	})
	if _, err := qtx.CreateWorkflowEvent(r.Context(), db.CreateWorkflowEventParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		WorkflowNodeInstanceID: node.ID, EventType: "node.task_" + action,
		ActorType: "member", ActorID: userUUID,
		IdempotencyKey: req.IdempotencyKey, Payload: payload,
	}); err != nil {
		writeError(w, http.StatusConflict, "workflow task action has already been recorded")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow task action")
		return
	}
	h.Metrics.RecordWorkflowHumanIntervention(action + "_task")
	h.Metrics.RecordWorkflowOperation("materialization_"+action, "requested")
	if action == "retry" {
		_ = h.materializeWorkflowTask(
			r.Context(), task.WorkspaceID, instance, node, task,
		)
		task, _ = h.Queries.GetWorkflowNodeTaskInWorkspace(
			r.Context(),
			db.GetWorkflowNodeTaskInWorkspaceParams{ID: task.ID, WorkspaceID: task.WorkspaceID},
		)
	} else {
		_, _ = h.reconcileWorkflowInstance(
			r.Context(), task.WorkspaceID, instance.ID, "member", userUUID,
			"task-detach:"+uuidToString(task.ID),
		)
	}
	h.publishWorkflowTaskUpdated(
		uuidToString(task.WorkspaceID), "member", userID,
		uuidToString(instance.ID), uuidToString(node.ID), uuidToString(task.ID),
	)
	writeJSON(w, http.StatusOK, map[string]any{"task": workflowTaskToResponse(task)})
}

func (h *Handler) loadWorkflowTask(
	w http.ResponseWriter,
	r *http.Request,
) (db.WorkflowNodeTask, db.WorkflowNodeInstance, db.WorkflowInstance, bool) {
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return db.WorkflowNodeTask{}, db.WorkflowNodeInstance{}, db.WorkflowInstance{}, false
	}
	taskID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "taskId"), "workflow_node_task_id")
	if !ok {
		return db.WorkflowNodeTask{}, db.WorkflowNodeInstance{}, db.WorkflowInstance{}, false
	}
	task, err := h.Queries.GetWorkflowNodeTaskInWorkspace(
		r.Context(),
		db.GetWorkflowNodeTaskInWorkspaceParams{ID: taskID, WorkspaceID: workspaceID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workflow node task not found")
		return db.WorkflowNodeTask{}, db.WorkflowNodeInstance{}, db.WorkflowInstance{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow node task")
		return db.WorkflowNodeTask{}, db.WorkflowNodeInstance{}, db.WorkflowInstance{}, false
	}
	node, err := h.Queries.GetWorkflowNodeInstanceInWorkspace(
		r.Context(),
		db.GetWorkflowNodeInstanceInWorkspaceParams{ID: task.WorkflowNodeInstanceID, WorkspaceID: workspaceID},
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "workflow node instance not found")
		return db.WorkflowNodeTask{}, db.WorkflowNodeInstance{}, db.WorkflowInstance{}, false
	}
	instance, err := h.Queries.GetWorkflowInstanceInWorkspace(
		r.Context(),
		db.GetWorkflowInstanceInWorkspaceParams{ID: task.WorkflowInstanceID, WorkspaceID: workspaceID},
	)
	if err != nil {
		writeError(w, http.StatusNotFound, "workflow instance not found")
		return db.WorkflowNodeTask{}, db.WorkflowNodeInstance{}, db.WorkflowInstance{}, false
	}
	return task, node, instance, true
}

func workflowTaskReplay(
	w http.ResponseWriter,
	r *http.Request,
	q *db.Queries,
	instance db.WorkflowInstance,
	idempotencyKey string,
	action string,
) (db.WorkflowNodeTask, bool) {
	event, err := q.GetWorkflowEventByIdempotencyKey(
		r.Context(),
		db.GetWorkflowEventByIdempotencyKeyParams{
			WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
			IdempotencyKey: idempotencyKey,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.WorkflowNodeTask{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check workflow idempotency")
		return db.WorkflowNodeTask{}, true
	}
	var payload struct {
		Action string `json:"action"`
		TaskID string `json:"task_id"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil || payload.Action != action {
		writeError(w, http.StatusConflict, "idempotency_key was already used for another workflow action")
		return db.WorkflowNodeTask{}, true
	}
	taskID, ok := parseUUIDOrBadRequest(w, payload.TaskID, "workflow_node_task_id")
	if !ok {
		return db.WorkflowNodeTask{}, true
	}
	task, err := q.GetWorkflowNodeTaskInWorkspace(
		r.Context(),
		db.GetWorkflowNodeTaskInWorkspaceParams{
			ID: taskID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusConflict, "workflow task replay target no longer exists")
		return db.WorkflowNodeTask{}, true
	}
	return task, true
}

func (h *Handler) canManageWorkflowNode(
	ctx context.Context,
	instance db.WorkflowInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	userID pgtype.UUID,
) (bool, error) {
	allowed, err := h.canSubmitWorkflowNode(ctx, instance, nodeDefinition, userID)
	if err != nil || allowed {
		return allowed, err
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(
		ctx,
		db.GetMemberByUserAndWorkspaceParams{
			UserID: userID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		return false, err
	}
	return roleAllowed(member.Role, "owner", "admin"), nil
}

func workflowExecutorResolutionToResponse(row db.WorkflowExecutorResolution) map[string]any {
	return map[string]any{
		"id": rowID(row.ID), "workflow_node_instance_id": rowID(row.WorkflowNodeInstanceID),
		"workflow_node_task_id": uuidToPtr(row.WorkflowNodeTaskID),
		"strategy":              row.Strategy, "status": row.Status,
		"actor_type": textToPtr(row.ActorType), "actor_id": uuidToPtr(row.ActorID),
		"candidates": json.RawMessage(row.Candidates), "reason": row.Reason,
		"resolved_at": timestampToPtr(row.ResolvedAt), "created_at": timestampToString(row.CreatedAt),
	}
}

func rowID(value pgtype.UUID) string {
	return uuidToString(value)
}
