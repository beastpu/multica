package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const workflowProposedTasksPayloadKey = "_workflow_proposed_tasks"

type confirmWorkflowProposedTasksRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

func proposedTasksFromSubmission(
	submission db.WorkflowNodeSubmission,
) ([]workflowdomain.IssueTemplate, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(submission.Payload, &payload); err != nil {
		return nil, errors.New("submission payload is invalid")
	}
	raw, exists := payload[workflowProposedTasksPayloadKey]
	if !exists {
		return nil, nil
	}
	var tasks []workflowdomain.IssueTemplate
	if err := json.Unmarshal(raw, &tasks); err != nil {
		return nil, errors.New("submission proposed_tasks are invalid")
	}
	return workflowdomain.NormalizeProposedTasks(tasks)
}

func workflowTasksResponse(tasks []db.WorkflowNodeTask) []workflowTaskResponse {
	items := make([]workflowTaskResponse, len(tasks))
	for index, task := range tasks {
		items[index] = workflowTaskToResponse(task)
	}
	return items
}

func (h *Handler) ConfirmWorkflowSubmissionTasks(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	var req confirmWorkflowProposedTasksRequest
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
	if instance.Status != "running" || !workflowNodeIsOpen(node) {
		writeError(w, http.StatusConflict, "workflow node does not accept proposed tasks")
		return
	}
	var nodeDefinition workflowdomain.NodeDefinition
	if err := json.Unmarshal(node.DefinitionSnapshot, &nodeDefinition); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow node snapshot")
		return
	}
	if !workflowdomain.AllowsDynamicIssues(nodeDefinition) {
		writeError(w, http.StatusConflict, "workflow node does not allow dynamic tasks")
		return
	}
	submissionID, ok := parseUUIDOrBadRequest(
		w,
		chi.URLParam(r, "submissionId"),
		"workflow_submission_id",
	)
	if !ok {
		return
	}
	submission, err := h.Queries.GetWorkflowSubmissionInWorkspace(
		r.Context(),
		db.GetWorkflowSubmissionInWorkspaceParams{
			ID: submissionID, WorkspaceID: node.WorkspaceID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) ||
		submission.WorkflowNodeInstanceID != node.ID {
		writeError(w, http.StatusNotFound, "workflow submission not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow submission")
		return
	}
	if submission.Status != "valid" {
		writeError(w, http.StatusConflict, "only a valid submission can create tasks")
		return
	}
	proposedTasks, err := proposedTasksFromSubmission(submission)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(proposedTasks) == 0 {
		writeError(w, http.StatusConflict, "submission has no proposed_tasks")
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
	allowed, err := h.canManageWorkflowNode(
		r.Context(),
		instance,
		node,
		nodeDefinition,
		userUUID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify workflow node permission")
		return
	}
	if !allowed {
		writeError(
			w,
			http.StatusForbidden,
			"only the node owner or a workspace admin can confirm proposed tasks",
		)
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
		db.LockWorkflowInstanceParams{
			ID: instance.ID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil || locked.Status != "running" {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	if event, replayErr := qtx.GetWorkflowEventByIdempotencyKey(
		r.Context(),
		db.GetWorkflowEventByIdempotencyKeyParams{
			WorkflowInstanceID: locked.ID,
			WorkspaceID:        locked.WorkspaceID,
			IdempotencyKey:     req.IdempotencyKey,
		},
	); replayErr == nil {
		var eventPayload struct {
			TaskIDs []string `json:"task_ids"`
		}
		if json.Unmarshal(event.Payload, &eventPayload) == nil {
			tasks := make([]db.WorkflowNodeTask, 0, len(eventPayload.TaskIDs))
			for _, taskIDText := range eventPayload.TaskIDs {
				taskID, parseErr := util.ParseUUID(taskIDText)
				if parseErr != nil {
					continue
				}
				task, getErr := qtx.GetWorkflowNodeTaskInWorkspace(
					r.Context(),
					db.GetWorkflowNodeTaskInWorkspaceParams{
						ID: taskID, WorkspaceID: locked.WorkspaceID,
					},
				)
				if getErr == nil && task.WorkflowNodeInstanceID == node.ID {
					tasks = append(tasks, task)
				}
			}
			tx.Rollback(r.Context())
			h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
			writeJSON(w, http.StatusOK, map[string]any{
				"tasks": workflowTasksResponse(tasks), "replayed": true,
			})
			return
		}
	}
	currentNode, err := qtx.GetWorkflowNodeInstanceInWorkspace(
		r.Context(),
		db.GetWorkflowNodeInstanceInWorkspaceParams{
			ID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil || !workflowNodeIsOpen(currentNode) {
		writeError(w, http.StatusConflict, "workflow node changed; refresh and try again")
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
	for _, task := range proposedTasks {
		if task.AssigneeRole != "" {
			if _, exists := roleMap[task.AssigneeRole]; !exists {
				writeError(
					w,
					http.StatusConflict,
					"proposed task references an unassigned assignee_role",
				)
				return
			}
		}
	}

	tasks := make([]db.WorkflowNodeTask, 0, len(proposedTasks))
	decisions := make([]workflowExecutorDecision, 0, len(proposedTasks))
	taskIDs := make([]string, 0, len(proposedTasks))
	needsSetup := false
	for _, taskDefinition := range proposedTasks {
		snapshot, _ := json.Marshal(taskDefinition)
		task, createErr := qtx.CreateWorkflowNodeTask(
			r.Context(),
			db.CreateWorkflowNodeTaskParams{
				WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
				WorkflowNodeInstanceID: currentNode.ID,
				TaskKey:                taskDefinition.Key,
				Source:                 "dynamic",
				Required:               taskDefinition.Required,
				DefinitionSnapshot:     snapshot,
				MaterializationStatus:  "pending_materialization",
				CreatedByType:          "member",
				CreatedByID:            userUUID,
			},
		)
		if createErr != nil {
			writeError(
				w,
				http.StatusConflict,
				"proposed task key already exists in this node attempt",
			)
			return
		}
		decision, resolveErr := resolveWorkflowTaskExecutor(
			r.Context(),
			qtx,
			locked.WorkspaceID,
			locked,
			nodeDefinition,
			taskDefinition,
			roleMap,
			newWorkflowConditionEvaluator(r.Context(), qtx, locked.WorkspaceID, locked),
		)
		if resolveErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve workflow executor")
			return
		}
		if _, createErr = createWorkflowExecutorResolution(
			r.Context(),
			qtx,
			locked.WorkspaceID,
			locked,
			currentNode,
			task,
			decision,
		); createErr != nil {
			writeError(
				w,
				http.StatusInternalServerError,
				"failed to record workflow executor resolution",
			)
			return
		}
		if decision.Assignment == nil {
			needsSetup = true
		}
		task, createErr = qtx.GetWorkflowNodeTaskInWorkspace(
			r.Context(),
			db.GetWorkflowNodeTaskInWorkspaceParams{
				ID: task.ID, WorkspaceID: task.WorkspaceID,
			},
		)
		if createErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to reload workflow task")
			return
		}
		tasks = append(tasks, task)
		decisions = append(decisions, decision)
		taskIDs = append(taskIDs, uuidToString(task.ID))
	}
	if needsSetup {
		reasons := workflowdomain.EncodeWaitingReasons([]workflowdomain.WaitingReason{{
			Code:    "executor_needs_setup",
			Message: "One or more proposed workflow tasks require an executor",
		}})
		if currentNode.Status != "blocked" {
			currentNode, err = qtx.UpdateWorkflowNodeState(
				r.Context(),
				db.UpdateWorkflowNodeStateParams{
					Status: "blocked", WaitingReasons: reasons, MarkReconciled: true,
					ID: currentNode.ID, WorkspaceID: currentNode.WorkspaceID,
					ExpectedStatus: currentNode.Status,
				},
			)
			if err != nil {
				writeError(w, http.StatusConflict, "workflow node changed; refresh and try again")
				return
			}
		}
		locked, err = qtx.UpdateWorkflowInstanceState(
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
	eventPayload, _ := json.Marshal(map[string]any{
		"submission_id": uuidToString(submission.ID),
		"task_ids":      taskIDs,
		"task_count":    len(taskIDs),
	})
	if _, err := qtx.CreateWorkflowEvent(
		r.Context(),
		db.CreateWorkflowEventParams{
			WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
			WorkflowNodeInstanceID: currentNode.ID,
			EventType:              "node.proposed_tasks_confirmed",
			ActorType:              "member",
			ActorID:                userUUID,
			IdempotencyKey:         req.IdempotencyKey,
			Payload:                eventPayload,
		},
	); err != nil {
		writeError(w, http.StatusConflict, "proposed tasks have already been confirmed")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit proposed tasks")
		return
	}
	h.Metrics.RecordWorkflowHumanIntervention("confirm_proposed_tasks")
	for _, decision := range decisions {
		outcome := "needs_setup"
		if decision.Assignment != nil {
			outcome = "resolved"
		}
		h.Metrics.RecordWorkflowExecutorResolution(
			decision.Strategy,
			outcome,
		)
	}
	if needsSetup {
		h.recordWorkflowInstanceStatusTransition("running", "needs_setup")
		h.recordWorkflowNodeTransition(currentNode, "blocked")
	}
	for index, task := range tasks {
		if decisions[index].Assignment != nil {
			_ = h.materializeWorkflowTask(
				r.Context(),
				currentNode.WorkspaceID,
				locked,
				currentNode,
				task,
			)
		}
		h.publishWorkflowTaskUpdated(
			uuidToString(currentNode.WorkspaceID),
			"member",
			userID,
			uuidToString(locked.ID),
			uuidToString(currentNode.ID),
			uuidToString(task.ID),
		)
	}
	if needsSetup {
		h.publishWorkflowInstanceUpdated(
			uuidToString(currentNode.WorkspaceID),
			"member",
			userID,
			uuidToString(locked.ID),
			uuidToString(currentNode.ID),
		)
	}
	reloadedTasks := make([]db.WorkflowNodeTask, 0, len(tasks))
	for _, task := range tasks {
		reloaded, reloadErr := h.Queries.GetWorkflowNodeTaskInWorkspace(
			r.Context(),
			db.GetWorkflowNodeTaskInWorkspaceParams{
				ID: task.ID, WorkspaceID: task.WorkspaceID,
			},
		)
		if reloadErr == nil {
			reloadedTasks = append(reloadedTasks, reloaded)
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"tasks": workflowTasksResponse(reloadedTasks), "replayed": false,
	})
}
