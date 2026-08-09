package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type workflowActionRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Reason         string `json:"reason,omitempty"`
}

func decodeWorkflowActionRequest(w http.ResponseWriter, r *http.Request) (workflowActionRequest, bool) {
	var req workflowActionRequest
	if r.Body == nil || r.ContentLength == 0 {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return workflowActionRequest{}, false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return workflowActionRequest{}, false
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return workflowActionRequest{}, false
	}
	return req, true
}

func (h *Handler) PauseWorkflowInstance(w http.ResponseWriter, r *http.Request) {
	h.transitionWorkflowInstance(w, r, "running", "paused", "workflow.paused")
}

func (h *Handler) ResumeWorkflowInstance(w http.ResponseWriter, r *http.Request) {
	h.transitionWorkflowInstance(w, r, "paused", "running", "workflow.resumed")
}

func (h *Handler) transitionWorkflowInstance(w http.ResponseWriter, r *http.Request, expected, target, eventType string) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, h.resolveWorkspaceID(r), "workspace not found", "owner", "admin"); !ok {
		return
	}
	req, ok := decodeWorkflowActionRequest(w, r)
	if !ok {
		return
	}
	instance, ok := h.loadWorkflowInstance(w, r)
	if !ok {
		return
	}
	if instance.Status == target {
		if _, err := h.Queries.GetWorkflowEventByIdempotencyKey(r.Context(), db.GetWorkflowEventByIdempotencyKeyParams{
			WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID, IdempotencyKey: req.IdempotencyKey,
		}); err == nil {
			h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
			h.writeWorkflowInstanceDetail(w, r, instance, http.StatusOK)
			return
		}
	}
	if instance.Status != expected {
		writeError(w, http.StatusConflict, "workflow instance cannot perform this transition")
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
	locked, err := qtx.LockWorkflowInstance(r.Context(), db.LockWorkflowInstanceParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID})
	if err != nil || locked.Status != expected {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	updated, err := qtx.UpdateWorkflowInstanceState(r.Context(), db.UpdateWorkflowInstanceStateParams{
		Status: target, MarkReconciled: false, ID: locked.ID, WorkspaceID: locked.WorkspaceID, ExpectedRevision: locked.Revision,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	payload, _ := json.Marshal(map[string]any{"reason": strings.TrimSpace(req.Reason)})
	if _, err := qtx.CreateWorkflowEvent(r.Context(), db.CreateWorkflowEventParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		EventType: eventType, ActorType: "member", ActorID: userUUID,
		IdempotencyKey: req.IdempotencyKey, Payload: payload,
	}); err != nil {
		writeError(w, http.StatusConflict, "workflow action has already been recorded")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow transition")
		return
	}
	h.recordWorkflowInstanceStatusTransition(locked.Status, updated.Status)
	h.Metrics.RecordWorkflowHumanIntervention(target)
	h.publishWorkflowInstanceUpdated(
		uuidToString(updated.WorkspaceID), "member", userID,
		uuidToString(updated.ID), "",
	)
	h.writeWorkflowInstanceDetail(w, r, updated, http.StatusOK)
}

func (h *Handler) CancelWorkflowInstance(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, h.resolveWorkspaceID(r), "workspace not found", "owner", "admin"); !ok {
		return
	}
	req, ok := decodeWorkflowActionRequest(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	instance, ok := h.loadWorkflowInstance(w, r)
	if !ok {
		return
	}
	if instance.Status == "cancelled" {
		if _, err := h.Queries.GetWorkflowEventByIdempotencyKey(r.Context(), db.GetWorkflowEventByIdempotencyKeyParams{
			WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID, IdempotencyKey: req.IdempotencyKey,
		}); err == nil {
			h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
			h.writeWorkflowInstanceDetail(w, r, instance, http.StatusOK)
			return
		}
	}
	if instance.Status == "completed" || instance.Status == "cancelled" {
		writeError(w, http.StatusConflict, "terminal workflow cannot be cancelled")
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
	locked, err := qtx.LockWorkflowInstance(r.Context(), db.LockWorkflowInstanceParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID})
	if err != nil || locked.Status == "completed" || locked.Status == "cancelled" {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	cancelledTasks, err := qtx.CancelAgentTasksByWorkflowInstance(
		r.Context(),
		db.CancelAgentTasksByWorkflowInstanceParams{
			WorkflowInstanceID: locked.ID,
			WorkspaceID:        locked.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel workflow agent tasks")
		return
	}
	if err := qtx.CancelOpenWorkflowNodes(r.Context(), db.CancelOpenWorkflowNodesParams{
		WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel workflow nodes")
		return
	}
	updated, err := qtx.UpdateWorkflowInstanceState(r.Context(), db.UpdateWorkflowInstanceStateParams{
		Status: "cancelled", Result: []byte(`{"reason":"cancelled_by_member"}`),
		ID: locked.ID, WorkspaceID: locked.WorkspaceID, ExpectedRevision: locked.Revision,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	payload, _ := json.Marshal(map[string]any{"reason": strings.TrimSpace(req.Reason)})
	if _, err := qtx.CreateWorkflowEvent(r.Context(), db.CreateWorkflowEventParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		EventType: "workflow.cancelled", ActorType: "member", ActorID: userUUID,
		IdempotencyKey: req.IdempotencyKey, Payload: payload,
	}); err != nil {
		writeError(w, http.StatusConflict, "workflow action has already been recorded")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow cancellation")
		return
	}
	h.TaskService.BroadcastCancelledTasks(r.Context(), cancelledTasks)
	// The nodes were cancelled inside the transaction; their carriers have to
	// follow, or the issues stay open on someone's board as work to pick up
	// for a run that no longer exists.
	h.cancelWorkflowNodeCarriers(r.Context(), updated)
	h.recordWorkflowInstanceStatusTransition(locked.Status, updated.Status)
	h.Metrics.RecordWorkflowHumanIntervention("cancel")
	h.publishWorkflowInstanceUpdated(
		uuidToString(updated.WorkspaceID), "member", userID,
		uuidToString(updated.ID), "",
	)
	h.writeWorkflowInstanceDetail(w, r, updated, http.StatusOK)
}

type updateWorkflowRolesRequest struct {
	RoleAssignments []workflowRoleAssignmentInput `json:"role_assignments"`
	IdempotencyKey  string                        `json:"idempotency_key"`
}

func (h *Handler) UpdateWorkflowInstanceRoles(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	var req updateWorkflowRolesRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || len(req.RoleAssignments) == 0 {
		writeError(w, http.StatusBadRequest, "role_assignments is required")
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
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user_id")
	if !ok {
		return
	}
	if instance.StartedByID != userUUID {
		if _, roleOK := h.requireWorkspaceRole(w, r, h.resolveWorkspaceID(r), "workspace not found", "owner", "admin"); !roleOK {
			return
		}
	}
	// Roles stay editable while the run can still act on them; a terminal
	// instance is history and must not be rewritten.
	switch instance.Status {
	case "needs_setup", "running", "paused":
	default:
		writeError(
			w,
			http.StatusConflict,
			"workflow roles cannot be changed after the workflow ends",
		)
		return
	}
	if instance.Status != "needs_setup" {
		if event, eventErr := h.Queries.GetWorkflowEventByIdempotencyKey(
			r.Context(),
			db.GetWorkflowEventByIdempotencyKeyParams{
				WorkflowInstanceID: instance.ID,
				WorkspaceID:        instance.WorkspaceID,
				IdempotencyKey:     idempotencyKey,
			},
		); eventErr == nil && event.EventType == "workflow.roles_updated" {
			h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
			h.writeWorkflowInstanceDetail(
				w,
				r,
				instance,
				http.StatusOK,
			)
			return
		}
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
	assignments, _, ok := h.validateWorkflowRoleAssignments(w, r, instance.WorkspaceID, h.resolveWorkspaceID(r), definition, req.RoleAssignments)
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
	locked, err := qtx.LockWorkflowInstance(r.Context(), db.LockWorkflowInstanceParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusConflict, "workflow setup changed; refresh and try again")
		return
	}
	switch locked.Status {
	case "needs_setup", "running", "paused":
	default:
		writeError(w, http.StatusConflict, "workflow setup changed; refresh and try again")
		return
	}
	for _, assignment := range assignments {
		if _, err := qtx.UpsertWorkflowRoleAssignment(r.Context(), db.UpsertWorkflowRoleAssignmentParams{
			WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID, RoleKey: assignment.RoleKey,
			ActorType: assignment.ActorType, ActorID: assignment.ActorID, Source: assignment.Source,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update workflow roles")
			return
		}
	}
	allRoles, err := qtx.ListWorkflowRoleAssignments(r.Context(), db.ListWorkflowRoleAssignmentsParams{
		WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow roles")
		return
	}
	roleMap := make(map[string]validatedWorkflowRoleAssignment, len(allRoles))
	for _, role := range allRoles {
		roleMap[role.RoleKey] = validatedWorkflowRoleAssignment{
			RoleKey: role.RoleKey, ActorType: role.ActorType, ActorID: role.ActorID, Source: role.Source,
		}
	}
	missing := make([]string, 0)
	for _, role := range definition.Roles {
		if role.Required {
			if _, exists := roleMap[role.Key]; !exists {
				missing = append(missing, role.Key)
			}
		}
	}
	var activeNodes []db.WorkflowNodeInstance
	updated := locked
	if locked.Status != "needs_setup" {
		// The run already started: keep its state and only re-resolve the
		// participants of attempts that can still act on the new assignment.
		nodes, err := qtx.ListWorkflowNodeInstances(r.Context(), db.ListWorkflowNodeInstancesParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load workflow nodes")
			return
		}
		if err := refreshWorkflowNodeParticipants(
			r.Context(), qtx, locked.WorkspaceID, nodes, definition, plan, roleMap,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update workflow participants")
			return
		}
	} else if len(missing) == 0 {
		nodes, err := qtx.ListWorkflowNodeInstances(r.Context(), db.ListWorkflowNodeInstancesParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load workflow nodes")
			return
		}
		nodesByKey := latestWorkflowNodesByKey(nodes)
		startNode := nodesByKey[plan.StartKey]
		completedStart, err := qtx.UpdateWorkflowNodeState(r.Context(), db.UpdateWorkflowNodeStateParams{
			Status: "completed", WaitingReasons: []byte("[]"), ID: startNode.ID,
			WorkspaceID: locked.WorkspaceID, ExpectedStatus: "pending",
		})
		if err != nil {
			writeError(w, http.StatusConflict, "workflow nodes changed; refresh and try again")
			return
		}
		for index := range nodes {
			if nodes[index].ID == completedStart.ID {
				nodes[index] = completedStart
			}
		}
		propagated, err := h.propagateWorkflowGraph(
			r.Context(), qtx, locked.WorkspaceID, locked, definition, plan,
			roleMap, nodes, "member", userUUID,
		)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to activate workflow")
			return
		}
		activeNodes = propagated.Activated
		targetStatus := "running"
		if propagated.NeedsSetup {
			targetStatus = "needs_setup"
		} else if propagated.CanComplete {
			targetStatus = "completed"
		}
		updated, err = qtx.UpdateWorkflowInstanceState(r.Context(), db.UpdateWorkflowInstanceStateParams{
			Status: targetStatus, ID: locked.ID, WorkspaceID: locked.WorkspaceID,
			ExpectedRevision: locked.Revision,
		})
		if err != nil {
			writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
			return
		}
	}
	payload, _ := json.Marshal(map[string]any{"missing_roles": missing})
	if _, err := qtx.CreateWorkflowEvent(r.Context(), db.CreateWorkflowEventParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		EventType: "workflow.roles_updated", ActorType: "member", ActorID: userUUID,
		IdempotencyKey: idempotencyKey, Payload: payload,
	}); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "workflow action has already been recorded")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow roles")
		return
	}
	if locked.Status != "needs_setup" {
		h.Metrics.RecordWorkflowOperation("role_reassignment", "applied")
	} else if len(missing) == 0 {
		h.Metrics.RecordWorkflowOperation("role_resolution", "resolved")
		h.recordWorkflowInstanceStatusTransition(locked.Status, updated.Status)
		h.recordWorkflowNodesActivated(r.Context(), activeNodes)
	} else {
		h.Metrics.RecordWorkflowOperation("role_resolution", "failed")
	}
	h.Metrics.RecordWorkflowHumanIntervention("configure_roles")
	if updated.Status == "completed" {
		_ = h.updateManagedWorkflowHostStatus(r.Context(), updated, "done")
	} else if updated.Status == "running" {
		_ = h.updateManagedWorkflowHostStatus(r.Context(), updated, "in_progress")
	}
	h.publishWorkflowInstanceUpdated(
		uuidToString(updated.WorkspaceID), "member", userID,
		uuidToString(updated.ID), firstWorkflowNodeID(activeNodes),
	)
	for _, activeNode := range activeNodes {
		h.publishWorkflowNodeUpdated(
			uuidToString(updated.WorkspaceID), "member", userID,
			uuidToString(updated.ID), uuidToString(activeNode.ID),
		)
		h.materializeWorkflowNodeTasks(r.Context(), locked.WorkspaceID, updated, activeNode)
	}
	h.writeWorkflowInstanceDetail(w, r, updated, http.StatusOK)
}

func (h *Handler) ListWorkflowInstanceIssues(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadWorkflowInstance(w, r)
	if !ok {
		return
	}
	issues, err := h.Queries.ListWorkflowInstanceIssues(r.Context(), db.ListWorkflowInstanceIssuesParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow issues")
		return
	}
	items := make([]IssueResponse, 0, len(issues))
	prefix := h.getIssuePrefix(r.Context(), instance.WorkspaceID)
	issueIDs := make([]pgtype.UUID, len(issues))
	for i, issue := range issues {
		issueIDs[i] = issue.ID
	}
	workflowContexts := h.issueWorkflowContextsByIssue(
		r.Context(),
		instance.WorkspaceID,
		issueIDs,
		prefix,
	)
	for _, issue := range issues {
		item := issueToResponse(issue, prefix)
		item.WorkflowContext = workflowContexts[item.ID]
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": items, "total": len(items)})
}

func (h *Handler) ListWorkflowInstanceEvents(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadWorkflowInstance(w, r)
	if !ok {
		return
	}
	events, err := h.Queries.ListWorkflowEvents(r.Context(), db.ListWorkflowEventsParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID, RowLimit: 200,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow events")
		return
	}
	items := make([]map[string]any, 0, len(events))
	for _, event := range events {
		items = append(items, map[string]any{
			"id": uuidToString(event.ID), "workflow_node_instance_id": uuidToPtr(event.WorkflowNodeInstanceID),
			"event_type": event.EventType, "actor_type": event.ActorType, "actor_id": uuidToPtr(event.ActorID),
			"idempotency_key": event.IdempotencyKey, "payload": json.RawMessage(event.Payload),
			"created_at": timestampToString(event.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": items})
}

func workflowInstanceIDFromRequest(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	return parseUUIDOrBadRequest(w, chi.URLParam(r, "instanceId"), "workflow_instance_id")
}
