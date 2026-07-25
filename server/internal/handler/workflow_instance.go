package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/service"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type workflowRoleAssignmentInput struct {
	RoleKey   string `json:"role_key"`
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
	Source    string `json:"source,omitempty"`
}

type startWorkflowRequest struct {
	TemplateID        string                        `json:"template_id"`
	TemplateVersionID string                        `json:"template_version_id,omitempty"`
	HostStatusMode    string                        `json:"host_status_mode,omitempty"`
	Input             json.RawMessage               `json:"input,omitempty"`
	RoleAssignments   []workflowRoleAssignmentInput `json:"role_assignments"`
	IdempotencyKey    string                        `json:"idempotency_key"`
}

type workflowInstanceResponse struct {
	ID                  string                            `json:"id"`
	WorkspaceID         string                            `json:"workspace_id"`
	TemplateID          string                            `json:"template_id"`
	TemplateVersionID   string                            `json:"template_version_id"`
	HostIssueID         string                            `json:"host_issue_id"`
	Status              string                            `json:"status"`
	HostStatusMode      string                            `json:"host_status_mode"`
	Input               json.RawMessage                   `json:"input"`
	Result              json.RawMessage                   `json:"result"`
	Revision            int64                             `json:"revision"`
	StartedByType       string                            `json:"started_by_type"`
	StartedByID         *string                           `json:"started_by_id"`
	StartedAt           string                            `json:"started_at"`
	PausedAt            *string                           `json:"paused_at"`
	CompletedAt         *string                           `json:"completed_at"`
	CancelledAt         *string                           `json:"cancelled_at"`
	LastReconciledAt    *string                           `json:"last_reconciled_at"`
	CreatedAt           string                            `json:"created_at"`
	UpdatedAt           string                            `json:"updated_at"`
	NextAction          string                            `json:"next_action"`
	InterventionReason  string                            `json:"intervention_reason"`
	HostIssueTitle      string                            `json:"host_issue_title"`
	HostIssueIdentifier string                            `json:"host_issue_identifier"`
	HostIssuePriority   string                            `json:"host_issue_priority"`
	ProjectID           *string                           `json:"project_id"`
	TemplateName        string                            `json:"template_name"`
	TemplateVersion     int32                             `json:"template_version"`
	CurrentActivities   []workflowCurrentActivityResponse `json:"current_activities"`
	ActivityCompleted   int32                             `json:"activity_completed"`
	ActivityTotal       int32                             `json:"activity_total"`
	CurrentOwners       []workflowActorReferenceResponse  `json:"current_owners"`
}

type workflowCurrentActivityResponse struct {
	ID      string `json:"id"`
	NodeKey string `json:"node_key"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Attempt int32  `json:"attempt"`
}

type workflowActorReferenceResponse struct {
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
}

type workflowInstanceDisplayContext struct {
	HostIssueTitle    string
	HostIssueNumber   int32
	HostIssuePriority string
	ProjectID         pgtype.UUID
	TemplateName      string
	TemplateVersion   int32
	CurrentActivities []workflowCurrentActivityResponse
	ActivityCompleted int32
	ActivityTotal     int32
	CurrentOwners     []workflowActorReferenceResponse
}

type workflowRoleAssignmentResponse struct {
	ID        string `json:"id"`
	RoleKey   string `json:"role_key"`
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
	Source    string `json:"source"`
}

type workflowNodeResponse struct {
	ID                 string          `json:"id"`
	WorkflowInstanceID string          `json:"workflow_instance_id"`
	NodeKey            string          `json:"node_key"`
	NodeKind           string          `json:"node_kind"`
	Attempt            int32           `json:"attempt"`
	Name               string          `json:"name"`
	DisplayOrder       int32           `json:"display_order"`
	Definition         json.RawMessage `json:"definition"`
	Status             string          `json:"status"`
	WaitingReasons     json.RawMessage `json:"waiting_reasons"`
	LatestSubmissionID *string         `json:"latest_submission_id"`
	LatestVerdictID    *string         `json:"latest_verdict_id"`
	ActivatedAt        *string         `json:"activated_at"`
	CompletedAt        *string         `json:"completed_at"`
}

type workflowTaskResponse struct {
	ID                     string          `json:"id"`
	WorkflowNodeInstanceID string          `json:"workflow_node_instance_id"`
	TaskKey                string          `json:"task_key"`
	Source                 string          `json:"source"`
	Required               bool            `json:"required"`
	Definition             json.RawMessage `json:"definition"`
	MaterializationStatus  string          `json:"materialization_status"`
	IssueID                *string         `json:"issue_id"`
	ExecutorResolutionID   *string         `json:"executor_resolution_id"`
	AttemptCount           int32           `json:"attempt_count"`
	LastError              string          `json:"last_error"`
}

type workflowInstanceDetailResponse struct {
	Instance        workflowInstanceResponse         `json:"instance"`
	RoleAssignments []workflowRoleAssignmentResponse `json:"role_assignments"`
	Nodes           []workflowNodeResponse           `json:"nodes"`
	Tasks           []workflowTaskResponse           `json:"tasks"`
}

func workflowInstanceToResponse(row db.WorkflowInstance) workflowInstanceResponse {
	nextAction, intervention := workflowNextAction(row.Status)
	return workflowInstanceResponse{
		ID: uuidToString(row.ID), WorkspaceID: uuidToString(row.WorkspaceID),
		TemplateID: uuidToString(row.TemplateID), TemplateVersionID: uuidToString(row.TemplateVersionID),
		HostIssueID: uuidToString(row.HostIssueID), Status: row.Status, HostStatusMode: row.HostStatusMode,
		Input: json.RawMessage(row.Input), Result: json.RawMessage(row.Result), Revision: row.Revision,
		StartedByType: row.StartedByType, StartedByID: uuidToPtr(row.StartedByID),
		StartedAt: timestampToString(row.StartedAt), PausedAt: timestampToPtr(row.PausedAt),
		CompletedAt: timestampToPtr(row.CompletedAt), CancelledAt: timestampToPtr(row.CancelledAt),
		LastReconciledAt: timestampToPtr(row.LastReconciledAt),
		CreatedAt:        timestampToString(row.CreatedAt), UpdatedAt: timestampToString(row.UpdatedAt),
		NextAction: nextAction, InterventionReason: intervention,
	}
}

func (h *Handler) workflowInstanceToRuntimeResponse(
	ctx context.Context,
	row db.WorkflowInstance,
) workflowInstanceResponse {
	nodes, nodeErr := h.Queries.ListWorkflowNodeInstances(
		ctx,
		db.ListWorkflowNodeInstancesParams{
			WorkflowInstanceID: row.ID,
			WorkspaceID:        row.WorkspaceID,
		},
	)
	tasks, taskErr := h.Queries.ListWorkflowInstanceTasks(
		ctx,
		db.ListWorkflowInstanceTasksParams{
			WorkflowInstanceID: row.ID,
			WorkspaceID:        row.WorkspaceID,
		},
	)
	contexts, contextErr := h.loadWorkflowInstanceDisplayContexts(
		ctx,
		row.WorkspaceID,
		[]pgtype.UUID{row.ID},
	)
	if nodeErr != nil || taskErr != nil {
		return workflowInstanceToResponse(row)
	}
	if contextErr != nil {
		return workflowInstanceRuntimeResponseFromFacts(
			row,
			nodes,
			tasks,
			workflowInstanceDisplayContext{},
			"",
		)
	}
	return workflowInstanceRuntimeResponseFromFacts(
		row,
		nodes,
		tasks,
		contexts[uuidToString(row.ID)],
		h.getIssuePrefix(ctx, row.WorkspaceID),
	)
}

func workflowInstanceRuntimeResponseFromFacts(
	row db.WorkflowInstance,
	nodes []db.WorkflowNodeInstance,
	tasks []db.WorkflowNodeTask,
	display workflowInstanceDisplayContext,
	issuePrefix string,
) workflowInstanceResponse {
	response := workflowInstanceToResponse(row)
	response.NextAction, response.InterventionReason =
		workflowRuntimeNextAction(row.Status, nodes, tasks)
	response.HostIssueTitle = display.HostIssueTitle
	if issuePrefix != "" && display.HostIssueNumber > 0 {
		response.HostIssueIdentifier = issuePrefix + "-" +
			strconv.Itoa(int(display.HostIssueNumber))
	}
	response.HostIssuePriority = display.HostIssuePriority
	response.ProjectID = uuidToPtr(display.ProjectID)
	response.TemplateName = display.TemplateName
	response.TemplateVersion = display.TemplateVersion
	response.CurrentActivities = display.CurrentActivities
	response.ActivityCompleted = display.ActivityCompleted
	response.ActivityTotal = display.ActivityTotal
	response.CurrentOwners = display.CurrentOwners
	if response.CurrentActivities == nil {
		response.CurrentActivities = []workflowCurrentActivityResponse{}
	}
	if response.CurrentOwners == nil {
		response.CurrentOwners = []workflowActorReferenceResponse{}
	}
	return response
}

func (h *Handler) loadWorkflowInstanceDisplayContexts(
	ctx context.Context,
	workspaceID pgtype.UUID,
	instanceIDs []pgtype.UUID,
) (map[string]workflowInstanceDisplayContext, error) {
	contexts := make(
		map[string]workflowInstanceDisplayContext,
		len(instanceIDs),
	)
	if len(instanceIDs) == 0 {
		return contexts, nil
	}
	params := db.ListWorkflowInstanceDisplayContextsParams{
		WorkspaceID: workspaceID, WorkflowInstanceIds: instanceIDs,
	}
	rows, err := h.Queries.ListWorkflowInstanceDisplayContexts(ctx, params)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		contexts[uuidToString(row.WorkflowInstanceID)] =
			workflowInstanceDisplayContext{
				HostIssueTitle:    row.HostIssueTitle,
				HostIssueNumber:   row.HostIssueNumber,
				HostIssuePriority: row.HostIssuePriority,
				ProjectID:         row.ProjectID,
				TemplateName:      row.TemplateName,
				TemplateVersion:   row.TemplateVersion,
				ActivityCompleted: row.ActivityCompleted,
				ActivityTotal:     row.ActivityTotal,
			}
	}
	activities, err := h.Queries.ListWorkflowCurrentActivitiesForInstances(
		ctx,
		db.ListWorkflowCurrentActivitiesForInstancesParams{
			WorkspaceID: workspaceID, WorkflowInstanceIds: instanceIDs,
		},
	)
	if err != nil {
		return nil, err
	}
	for _, activity := range activities {
		key := uuidToString(activity.WorkflowInstanceID)
		display := contexts[key]
		display.CurrentActivities = append(
			display.CurrentActivities,
			workflowCurrentActivityResponse{
				ID: uuidToString(activity.ID), NodeKey: activity.NodeKey,
				Name: activity.NameSnapshot, Status: activity.Status,
				Attempt: activity.Attempt,
			},
		)
		contexts[key] = display
	}
	owners, err := h.Queries.ListWorkflowCurrentOwnersForInstances(
		ctx,
		db.ListWorkflowCurrentOwnersForInstancesParams{
			WorkspaceID: workspaceID, WorkflowInstanceIds: instanceIDs,
		},
	)
	if err != nil {
		return nil, err
	}
	seenOwners := make(map[string]struct{})
	for _, owner := range owners {
		key := uuidToString(owner.WorkflowInstanceID)
		actorID := uuidToString(owner.ActorID)
		dedupeKey := key + ":" + owner.ActorType + ":" + actorID
		if _, exists := seenOwners[dedupeKey]; exists {
			continue
		}
		seenOwners[dedupeKey] = struct{}{}
		display := contexts[key]
		display.CurrentOwners = append(
			display.CurrentOwners,
			workflowActorReferenceResponse{
				ActorType: owner.ActorType,
				ActorID:   actorID,
			},
		)
		contexts[key] = display
	}
	return contexts, nil
}

func workflowNextAction(status string) (string, string) {
	switch status {
	case "needs_setup":
		return "configure_roles", "missing_role_or_executor"
	case "paused":
		return "resume", "workflow_paused"
	case "running":
		return "view_current_activity", ""
	case "failed":
		return "reconcile", "runtime_failure"
	default:
		return "none", ""
	}
}

func workflowRuntimeNextAction(
	status string,
	nodes []db.WorkflowNodeInstance,
	tasks []db.WorkflowNodeTask,
) (string, string) {
	if status == "paused" {
		return "resume", "workflow_paused"
	}
	if status == "failed" {
		return "reconcile", "runtime_failure"
	}
	if status != "running" && status != "needs_setup" {
		return "none", ""
	}
	for _, task := range tasks {
		if task.MaterializationStatus == "failed" {
			return "retry_materialization", "materialization_failed"
		}
		if task.MaterializationStatus != "cancelled" &&
			!task.ExecutorResolutionID.Valid {
			return "configure_executor", "executor_unresolved"
		}
	}
	reasonCodes := make(map[string]struct{})
	blocked := false
	for _, node := range nodes {
		if node.Status == "blocked" {
			blocked = true
		}
		if !workflowNodeIsOpen(node) {
			continue
		}
		var reasons []workflowdomain.WaitingReason
		if json.Unmarshal(node.WaitingReasons, &reasons) != nil {
			continue
		}
		for _, reason := range reasons {
			reasonCodes[reason.Code] = struct{}{}
		}
	}
	hasReason := func(codes ...string) bool {
		for _, code := range codes {
			if _, exists := reasonCodes[code]; exists {
				return true
			}
		}
		return false
	}
	switch {
	case hasReason("executor_needs_setup", "executor_unresolved"):
		return "configure_executor", "executor_unresolved"
	case hasReason("required_task_not_materialized", "stale_materialization"):
		return "retry_materialization", "materialization_failed"
	case hasReason("awaiting_acceptance", "acceptance_not_approved"):
		return "review_acceptance", "awaiting_acceptance"
	case hasReason(
		"task_submission_required",
		"valid_submission_required",
		"submission_required_field_missing",
		"submission_field_type_invalid",
	):
		return "submit_result", "awaiting_submission"
	case hasReason(
		"member_verdict_required",
		"verdict_definition_missing",
		"verdict_not_passed",
	):
		return "record_verdict", "awaiting_verdict"
	case hasReason("confirmation_required"):
		return "confirm_activity", "awaiting_confirmation"
	case hasReason("manual_completion_required"):
		return "complete_activity", "awaiting_manual_completion"
	case hasReason("node_timeout") || blocked:
		return "recover_activity", "blocked_or_timeout"
	case status == "needs_setup":
		return "configure_roles", "missing_required_role"
	default:
		return "view_current_activity", ""
	}
}

type workflowViewerNodeRoles map[string]map[string]struct{}

func workflowPersonalizedNextAction(
	instance db.WorkflowInstance,
	nodes []db.WorkflowNodeInstance,
	tasks []db.WorkflowNodeTask,
	viewerID pgtype.UUID,
	viewerIsAdmin bool,
	roles workflowViewerNodeRoles,
) (string, string) {
	action, reason := workflowRuntimeNextAction(instance.Status, nodes, tasks)
	if action == "none" || action == "view_current_activity" {
		return action, reason
	}
	if viewerIsAdmin {
		return action, reason
	}

	hasRole := func(nodeID pgtype.UUID, role string) bool {
		_, ok := roles[uuidToString(nodeID)][role]
		return ok
	}
	nodeAllows := func(
		role string,
		reasonCodes ...string,
	) bool {
		for _, node := range nodes {
			if !workflowNodeIsOpen(node) || !hasRole(node.ID, role) {
				continue
			}
			for _, code := range reasonCodes {
				if workflowNodeHasWaitingReason(node, code) {
					return true
				}
			}
		}
		return false
	}

	allowed := false
	switch action {
	case "configure_roles":
		allowed = instance.StartedByType == "member" &&
			instance.StartedByID == viewerID
	case "configure_executor":
		for _, task := range tasks {
			if task.MaterializationStatus != "cancelled" &&
				!task.ExecutorResolutionID.Valid &&
				hasRole(task.WorkflowNodeInstanceID, "owner") {
				allowed = true
				break
			}
		}
	case "submit_result":
		allowed = nodeAllows(
			"owner",
			"task_submission_required",
			"valid_submission_required",
			"submission_required_field_missing",
			"submission_field_type_invalid",
		)
	case "record_verdict":
		allowed = nodeAllows(
			"owner",
			"member_verdict_required",
			"verdict_definition_missing",
			"verdict_not_passed",
		)
	case "review_acceptance":
		allowed = nodeAllows(
			"approver",
			"awaiting_acceptance",
			"acceptance_not_approved",
		)
	case "complete_activity":
		allowed = nodeAllows("owner", "manual_completion_required")
	case "recover_activity":
		for _, node := range nodes {
			if hasRole(node.ID, "owner") &&
				(node.Status == "blocked" ||
					workflowNodeHasWaitingReason(node, "node_timeout")) {
				allowed = true
				break
			}
		}
	case "confirm_activity":
		for _, node := range nodes {
			if !workflowNodeHasWaitingReason(node, "confirmation_required") {
				continue
			}
			var definition workflowdomain.NodeDefinition
			if json.Unmarshal(node.DefinitionSnapshot, &definition) != nil {
				continue
			}
			switch definition.Completion.Confirmation {
			case "member_any":
				allowed = true
			case "member_all":
				allowed = len(roles[uuidToString(node.ID)]) > 0
			case "owner_any", "owner_all":
				allowed = hasRole(node.ID, "owner")
			}
			if allowed {
				break
			}
		}
	}
	if !allowed {
		return "view_current_activity", ""
	}
	return action, reason
}

func workflowNodeHasWaitingReason(
	node db.WorkflowNodeInstance,
	code string,
) bool {
	var reasons []workflowdomain.WaitingReason
	if json.Unmarshal(node.WaitingReasons, &reasons) != nil {
		return false
	}
	for _, reason := range reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}

func workflowRoleAssignmentToResponse(row db.WorkflowInstanceRoleAssignment) workflowRoleAssignmentResponse {
	return workflowRoleAssignmentResponse{
		ID: uuidToString(row.ID), RoleKey: row.RoleKey, ActorType: row.ActorType,
		ActorID: uuidToString(row.ActorID), Source: row.Source,
	}
}

func workflowNodeToResponse(row db.WorkflowNodeInstance) workflowNodeResponse {
	return workflowNodeResponse{
		ID: uuidToString(row.ID), WorkflowInstanceID: uuidToString(row.WorkflowInstanceID),
		NodeKey: row.NodeKey, NodeKind: row.NodeKind, Attempt: row.Attempt,
		Name: row.NameSnapshot, DisplayOrder: row.DisplayOrder,
		Definition: json.RawMessage(row.DefinitionSnapshot), Status: row.Status,
		WaitingReasons:     json.RawMessage(row.WaitingReasons),
		LatestSubmissionID: uuidToPtr(row.LatestSubmissionID), LatestVerdictID: uuidToPtr(row.LatestVerdictID),
		ActivatedAt: timestampToPtr(row.ActivatedAt), CompletedAt: timestampToPtr(row.CompletedAt),
	}
}

func workflowTaskToResponse(row db.WorkflowNodeTask) workflowTaskResponse {
	return workflowTaskResponse{
		ID: uuidToString(row.ID), WorkflowNodeInstanceID: uuidToString(row.WorkflowNodeInstanceID),
		TaskKey: row.TaskKey, Source: row.Source, Required: row.Required,
		Definition:            json.RawMessage(row.DefinitionSnapshot),
		MaterializationStatus: row.MaterializationStatus, IssueID: uuidToPtr(row.IssueID),
		ExecutorResolutionID: uuidToPtr(row.ExecutorResolutionID),
		AttemptCount:         row.AttemptCount, LastError: row.LastError,
	}
}

func (h *Handler) StartIssueWorkflow(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	var req startWorkflowRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	hostID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "issue_id")
	if !ok {
		return
	}
	templateID, ok := parseUUIDOrBadRequest(w, req.TemplateID, "template_id")
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
	if _, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{ID: hostID, WorkspaceID: wsUUID}); err != nil {
		writeError(w, http.StatusNotFound, "host issue not found")
		return
	}
	template, err := h.Queries.GetWorkflowTemplateInWorkspace(r.Context(), db.GetWorkflowTemplateInWorkspaceParams{ID: templateID, WorkspaceID: wsUUID})
	if errors.Is(err, pgx.ErrNoRows) || template.Status != "published" {
		writeError(w, http.StatusNotFound, "published workflow template not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template")
		return
	}
	versionID := template.LatestPublishedVersionID
	if strings.TrimSpace(req.TemplateVersionID) != "" {
		versionID, ok = parseUUIDOrBadRequest(w, req.TemplateVersionID, "template_version_id")
		if !ok {
			return
		}
	}
	version, err := h.Queries.GetWorkflowTemplateVersionInWorkspace(r.Context(), db.GetWorkflowTemplateVersionInWorkspaceParams{ID: versionID, WorkspaceID: wsUUID})
	if errors.Is(err, pgx.ErrNoRows) || version.TemplateID != template.ID || version.Status != "published" {
		writeError(w, http.StatusBadRequest, "published template version not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template version")
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
	assignments, missingRoles, ok := h.validateWorkflowRoleAssignments(w, r, wsUUID, workspaceID, definition, req.RoleAssignments)
	if !ok {
		return
	}
	input := req.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	var inputObject map[string]any
	if err := json.Unmarshal(input, &inputObject); err != nil || inputObject == nil {
		writeError(w, http.StatusBadRequest, "input must be a JSON object")
		return
	}
	normalizedInput, _ := json.Marshal(inputObject)
	hostStatusMode := req.HostStatusMode
	if hostStatusMode == "" {
		hostStatusMode = "independent"
	}
	if hostStatusMode != "managed" && hostStatusMode != "independent" {
		writeError(w, http.StatusBadRequest, "host_status_mode must be managed or independent")
		return
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	if existing, err := qtx.GetActiveWorkflowInstanceByHost(r.Context(), db.GetActiveWorkflowInstanceByHostParams{
		HostIssueID: hostID, WorkspaceID: wsUUID,
	}); err == nil {
		if event, eventErr := qtx.GetWorkflowEventByIdempotencyKey(r.Context(), db.GetWorkflowEventByIdempotencyKeyParams{
			WorkflowInstanceID: existing.ID, WorkspaceID: wsUUID, IdempotencyKey: idempotencyKey,
		}); eventErr == nil && event.ID.Valid {
			tx.Rollback(r.Context())
			h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
			h.writeWorkflowInstanceDetail(w, r, existing, http.StatusOK)
			return
		}
		writeError(w, http.StatusConflict, "host issue already has an active workflow")
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to check active workflow")
		return
	}

	instance, activeNodes, err := h.createWorkflowRuntime(
		r.Context(), qtx, workflowRuntimeStartParams{
			WorkspaceID: wsUUID, TemplateID: template.ID, TemplateVersionID: version.ID,
			HostIssueID: hostID, HostStatusMode: hostStatusMode, Input: normalizedInput,
			StartedByID: userUUID, IdempotencyKey: idempotencyKey,
			Definition: definition, Plan: plan, Assignments: assignments, MissingRoles: missingRoles,
		},
	)
	if err != nil {
		if isUniqueViolation(err) {
			_ = tx.Rollback(r.Context())
			existing, lookupErr := h.Queries.GetActiveWorkflowInstanceByHost(
				r.Context(),
				db.GetActiveWorkflowInstanceByHostParams{
					HostIssueID: hostID,
					WorkspaceID: wsUUID,
				},
			)
			if lookupErr == nil {
				if event, eventErr := h.Queries.GetWorkflowEventByIdempotencyKey(
					r.Context(),
					db.GetWorkflowEventByIdempotencyKeyParams{
						WorkflowInstanceID: existing.ID,
						WorkspaceID:        wsUUID,
						IdempotencyKey:     idempotencyKey,
					},
				); eventErr == nil && event.ID.Valid {
					h.Metrics.RecordWorkflowOperation(
						"duplicate",
						"prevented",
					)
					h.writeWorkflowInstanceDetail(
						w,
						r,
						existing,
						http.StatusOK,
					)
					return
				}
				writeError(
					w,
					http.StatusConflict,
					"host issue already has an active workflow",
				)
				return
			}
		}
		writeError(w, http.StatusInternalServerError, "failed to create workflow runtime")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow start")
		return
	}
	h.recordWorkflowStarted(
		r.Context(), "existing_issue", instance, activeNodes, len(missingRoles),
	)
	if instance.Status == "completed" {
		_ = h.updateManagedWorkflowHostStatus(r.Context(), instance, "done")
	} else {
		_ = h.updateManagedWorkflowHostStatus(r.Context(), instance, "in_progress")
	}
	h.publishWorkflowInstanceUpdated(
		workspaceID, "member", userID,
		uuidToString(instance.ID), firstWorkflowNodeID(activeNodes),
	)
	for _, activeNode := range activeNodes {
		h.publishWorkflowNodeUpdated(
			workspaceID, "member", userID,
			uuidToString(instance.ID), uuidToString(activeNode.ID),
		)
		h.materializeWorkflowNodeTasks(r.Context(), wsUUID, instance, activeNode)
	}
	h.notifyWorkflowNeedsSetup(r.Context(), instance, missingRoles)
	h.writeWorkflowInstanceDetail(w, r, instance, http.StatusCreated)
}

type workflowRuntimeStartParams struct {
	WorkspaceID       pgtype.UUID
	TemplateID        pgtype.UUID
	TemplateVersionID pgtype.UUID
	HostIssueID       pgtype.UUID
	HostStatusMode    string
	Input             []byte
	StartedByID       pgtype.UUID
	IdempotencyKey    string
	Definition        workflowdomain.Definition
	Plan              workflowdomain.GraphPlan
	Assignments       []validatedWorkflowRoleAssignment
	MissingRoles      []string
}

func (h *Handler) createWorkflowRuntime(
	ctx context.Context,
	q *db.Queries,
	params workflowRuntimeStartParams,
) (db.WorkflowInstance, []db.WorkflowNodeInstance, error) {
	instanceStatus := "running"
	if len(params.MissingRoles) > 0 {
		instanceStatus = "needs_setup"
	}
	instance, err := q.CreateWorkflowInstance(ctx, db.CreateWorkflowInstanceParams{
		WorkspaceID: params.WorkspaceID, TemplateID: params.TemplateID,
		TemplateVersionID: params.TemplateVersionID, HostIssueID: params.HostIssueID,
		Status: instanceStatus, HostStatusMode: params.HostStatusMode,
		Input: params.Input, StartedByType: "member", StartedByID: params.StartedByID,
	})
	if err != nil {
		return db.WorkflowInstance{}, nil, fmt.Errorf("create instance: %w", err)
	}
	for _, assignment := range params.Assignments {
		if _, err := q.CreateWorkflowRoleAssignment(ctx, db.CreateWorkflowRoleAssignmentParams{
			WorkspaceID: params.WorkspaceID, WorkflowInstanceID: instance.ID,
			RoleKey: assignment.RoleKey, ActorType: assignment.ActorType,
			ActorID: assignment.ActorID, Source: assignment.Source,
		}); err != nil {
			return db.WorkflowInstance{}, nil, fmt.Errorf("create role assignment: %w", err)
		}
	}
	roleMap := workflowRoleMap(params.Assignments)
	nodes := make([]db.WorkflowNodeInstance, 0, len(params.Plan.Ordered))
	for index, nodeDefinition := range params.Plan.Ordered {
		nodeStatus := "pending"
		if instanceStatus == "running" && nodeDefinition.Kind == "start" {
			nodeStatus = "completed"
		}
		snapshot, _ := json.Marshal(nodeDefinition)
		node, err := q.CreateWorkflowNodeInstance(ctx, db.CreateWorkflowNodeInstanceParams{
			WorkspaceID: params.WorkspaceID, WorkflowInstanceID: instance.ID,
			NodeKey: nodeDefinition.Key, NodeKind: nodeDefinition.Kind, Attempt: 1,
			NameSnapshot: nodeDefinition.Name, DisplayOrder: int32(index),
			DefinitionSnapshot: snapshot, Status: nodeStatus,
		})
		if err != nil {
			return db.WorkflowInstance{}, nil, fmt.Errorf("create node instance: %w", err)
		}
		nodes = append(nodes, node)
	}
	eventPayload, _ := json.Marshal(map[string]any{"missing_roles": params.MissingRoles})
	if _, err := q.CreateWorkflowEvent(ctx, db.CreateWorkflowEventParams{
		WorkspaceID: params.WorkspaceID, WorkflowInstanceID: instance.ID,
		EventType: "workflow.started", ActorType: "member", ActorID: params.StartedByID,
		IdempotencyKey: params.IdempotencyKey, Payload: eventPayload,
	}); err != nil {
		return db.WorkflowInstance{}, nil, fmt.Errorf("create start event: %w", err)
	}
	var activeNodes []db.WorkflowNodeInstance
	if instanceStatus == "running" {
		propagated, err := h.propagateWorkflowGraph(
			ctx, q, params.WorkspaceID, instance, params.Definition, params.Plan,
			roleMap, nodes, "member", params.StartedByID,
		)
		if err != nil {
			return db.WorkflowInstance{}, nil, fmt.Errorf("activate workflow graph: %w", err)
		}
		activeNodes = propagated.Activated
		if propagated.NeedsSetup {
			instance, err = q.UpdateWorkflowInstanceState(ctx, db.UpdateWorkflowInstanceStateParams{
				Status: "needs_setup", MarkReconciled: true,
				ID: instance.ID, WorkspaceID: instance.WorkspaceID,
				ExpectedRevision: instance.Revision,
			})
			if err != nil {
				return db.WorkflowInstance{}, nil, fmt.Errorf("pause workflow for executor setup: %w", err)
			}
		} else if propagated.CanComplete {
			instance, err = q.UpdateWorkflowInstanceState(ctx, db.UpdateWorkflowInstanceStateParams{
				Status: "completed", Result: []byte(`{"reason":"empty_workflow"}`),
				MarkReconciled: true, ID: instance.ID, WorkspaceID: instance.WorkspaceID,
				ExpectedRevision: instance.Revision,
			})
			if err != nil {
				return db.WorkflowInstance{}, nil, fmt.Errorf("complete empty workflow: %w", err)
			}
		}
	}
	return instance, activeNodes, nil
}

func firstWorkflowNodeID(nodes []db.WorkflowNodeInstance) string {
	if len(nodes) == 0 {
		return ""
	}
	return uuidToString(nodes[0].ID)
}

type validatedWorkflowRoleAssignment struct {
	RoleKey   string
	ActorType string
	ActorID   pgtype.UUID
	Source    string
}

func (h *Handler) validateWorkflowRoleAssignments(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID pgtype.UUID,
	workspaceIDString string,
	definition workflowdomain.Definition,
	inputs []workflowRoleAssignmentInput,
) ([]validatedWorkflowRoleAssignment, []string, bool) {
	roleDefinitions := make(map[string]workflowdomain.RoleDefinition, len(definition.Roles))
	for _, role := range definition.Roles {
		roleDefinitions[role.Key] = role
	}
	seen := make(map[string]struct{}, len(inputs))
	assignments := make([]validatedWorkflowRoleAssignment, 0, len(inputs))
	for _, input := range inputs {
		role, exists := roleDefinitions[input.RoleKey]
		if !exists {
			writeError(w, http.StatusBadRequest, "role assignment references unknown role")
			return nil, nil, false
		}
		if _, duplicate := seen[input.RoleKey]; duplicate {
			writeError(w, http.StatusBadRequest, "duplicate role assignment")
			return nil, nil, false
		}
		seen[input.RoleKey] = struct{}{}
		allowed := false
		for _, actorType := range role.AllowedActorTypes {
			allowed = allowed || actorType == input.ActorType
		}
		if !allowed {
			writeError(w, http.StatusBadRequest, "role assignment actor type is not allowed")
			return nil, nil, false
		}
		actorID, ok := parseUUIDOrBadRequest(w, input.ActorID, "actor_id")
		if !ok {
			return nil, nil, false
		}
		if status, message := h.validateAssigneePair(r.Context(), r, workspaceIDString,
			pgtype.Text{String: input.ActorType, Valid: true}, actorID); status != 0 {
			writeError(w, status, message)
			return nil, nil, false
		}
		source := input.Source
		if source == "" {
			source = "user_selected"
		}
		switch source {
		case "fixed", "host_assignee", "user_selected", "copied":
		default:
			writeError(w, http.StatusBadRequest, "invalid role assignment source")
			return nil, nil, false
		}
		assignments = append(assignments, validatedWorkflowRoleAssignment{
			RoleKey: input.RoleKey, ActorType: input.ActorType, ActorID: actorID, Source: source,
		})
	}
	missing := make([]string, 0)
	for _, role := range definition.Roles {
		if role.Required {
			if _, exists := seen[role.Key]; !exists {
				missing = append(missing, role.Key)
			}
		}
	}
	_ = workspaceID
	return assignments, missing, true
}

func workflowRoleMap(assignments []validatedWorkflowRoleAssignment) map[string]validatedWorkflowRoleAssignment {
	result := make(map[string]validatedWorkflowRoleAssignment, len(assignments))
	for _, assignment := range assignments {
		result[assignment.RoleKey] = assignment
	}
	return result
}

func createWorkflowNodeActivationRecords(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	definition workflowdomain.Definition,
	roles map[string]validatedWorkflowRoleAssignment,
) (bool, error) {
	participantRoles := make([]struct {
		key  string
		role string
	}, 0, len(nodeDefinition.ParticipantRoles)+2)
	if nodeDefinition.OwnerRole != "" {
		participantRoles = append(participantRoles, struct {
			key  string
			role string
		}{nodeDefinition.OwnerRole, "owner"})
	}
	for _, key := range nodeDefinition.ParticipantRoles {
		participantRoles = append(participantRoles, struct {
			key  string
			role string
		}{key, "participant"})
	}
	if workflowdomain.IsAcceptanceActivity(definition, nodeDefinition) && definition.Acceptance.ApproverRole != "" {
		participantRoles = append(participantRoles, struct {
			key  string
			role string
		}{definition.Acceptance.ApproverRole, "approver"})
	}
	for _, participant := range participantRoles {
		assignment, exists := roles[participant.key]
		if !exists {
			continue
		}
		if _, err := q.CreateWorkflowNodeParticipant(ctx, db.CreateWorkflowNodeParticipantParams{
			WorkspaceID: workspaceID, WorkflowNodeInstanceID: node.ID, Role: participant.role,
			ActorType: assignment.ActorType, ActorID: assignment.ActorID,
		}); err != nil {
			return false, fmt.Errorf("create participant: %w", err)
		}
	}
	if nodeDefinition.OwnerRole == "" {
		for _, strategy := range nodeDefinition.Executor.Strategies {
			if strategy.Kind != "fixed_actor" {
				continue
			}
			assignment, err := directWorkflowExecutorAssignment(
				strategy.ActorType,
				strategy.ActorID,
			)
			if err != nil {
				return false, fmt.Errorf("resolve direct node owner: %w", err)
			}
			if err := validateWorkflowExecutorActor(
				ctx,
				q,
				workspaceID,
				assignment,
			); err != nil {
				break
			}
			if _, err := q.CreateWorkflowNodeParticipant(ctx, db.CreateWorkflowNodeParticipantParams{
				WorkspaceID: workspaceID, WorkflowNodeInstanceID: node.ID, Role: "owner",
				ActorType: assignment.ActorType, ActorID: assignment.ActorID,
			}); err != nil {
				return false, fmt.Errorf("create direct node owner: %w", err)
			}
			break
		}
	}
	needsSetup := false
	for _, issueTemplate := range nodeDefinition.IssueTemplates {
		snapshot, _ := json.Marshal(issueTemplate)
		task, err := q.CreateWorkflowNodeTask(ctx, db.CreateWorkflowNodeTaskParams{
			WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID, WorkflowNodeInstanceID: node.ID,
			TaskKey: issueTemplate.Key, Source: "template", Required: issueTemplate.Required,
			DefinitionSnapshot: snapshot, MaterializationStatus: "pending_materialization",
			CreatedByType: instance.StartedByType, CreatedByID: instance.StartedByID,
		})
		if err != nil {
			return false, fmt.Errorf("create node task: %w", err)
		}
		decision, err := resolveWorkflowTaskExecutor(
			ctx, q, workspaceID, instance, nodeDefinition, issueTemplate, roles,
			newWorkflowConditionEvaluator(ctx, q, workspaceID, instance),
		)
		if err != nil {
			return false, fmt.Errorf("resolve task executor: %w", err)
		}
		if _, err := createWorkflowExecutorResolution(
			ctx, q, workspaceID, instance, node, task, decision,
		); err != nil {
			return false, fmt.Errorf("create executor resolution: %w", err)
		}
		needsSetup = needsSetup || decision.Assignment == nil
	}
	if workflowdomain.IsAcceptanceActivity(definition, nodeDefinition) && definition.Acceptance.Policy == "member" {
		revision, err := q.GetNextWorkflowAcceptanceRevision(ctx, db.GetNextWorkflowAcceptanceRevisionParams{
			WorkflowInstanceID: instance.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return false, fmt.Errorf("next acceptance revision: %w", err)
		}
		if _, err := q.CreateWorkflowAcceptance(ctx, db.CreateWorkflowAcceptanceParams{
			WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID, WorkflowNodeInstanceID: node.ID,
			Revision: revision, Status: "pending", Evidence: []byte("[]"),
			IdempotencyKey: "activation:" + uuidToString(node.ID),
		}); err != nil {
			return false, fmt.Errorf("create pending acceptance: %w", err)
		}
	}
	return needsSetup, nil
}

func ensureWorkflowNodeTasks(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
) (bool, error) {
	tasks, err := q.ListWorkflowNodeTasks(
		ctx,
		db.ListWorkflowNodeTasksParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
		},
	)
	if err != nil {
		return false, err
	}
	existing := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		existing[task.TaskKey] = struct{}{}
	}
	roles, err := q.ListWorkflowRoleAssignments(
		ctx,
		db.ListWorkflowRoleAssignmentsParams{
			WorkflowInstanceID: instance.ID, WorkspaceID: workspaceID,
		},
	)
	if err != nil {
		return false, err
	}
	roleMap := make(map[string]validatedWorkflowRoleAssignment, len(roles))
	for _, role := range roles {
		roleMap[role.RoleKey] = validatedWorkflowRoleAssignment{
			RoleKey: role.RoleKey, ActorType: role.ActorType,
			ActorID: role.ActorID, Source: role.Source,
		}
	}
	repaired := false
	for _, issueTemplate := range nodeDefinition.IssueTemplates {
		if _, exists := existing[issueTemplate.Key]; exists {
			continue
		}
		snapshot, _ := json.Marshal(issueTemplate)
		task, createErr := q.CreateWorkflowNodeTask(
			ctx,
			db.CreateWorkflowNodeTaskParams{
				WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID,
				WorkflowNodeInstanceID: node.ID, TaskKey: issueTemplate.Key,
				Source: "template", Required: issueTemplate.Required,
				DefinitionSnapshot:    snapshot,
				MaterializationStatus: "pending_materialization",
				CreatedByType:         "system",
			},
		)
		if createErr != nil {
			return repaired, createErr
		}
		repaired = true
		decision, resolutionErr := resolveWorkflowTaskExecutor(
			ctx, q, workspaceID, instance, nodeDefinition, issueTemplate, roleMap,
			newWorkflowConditionEvaluator(ctx, q, workspaceID, instance),
		)
		if resolutionErr != nil {
			return repaired, resolutionErr
		}
		decision.Reason = "Reconciler repaired a missing task: " + decision.Reason
		if _, resolutionErr := createWorkflowExecutorResolution(
			ctx, q, workspaceID, instance, node, task, decision,
		); resolutionErr != nil {
			return repaired, resolutionErr
		}
	}
	return repaired, nil
}

func (h *Handler) materializeWorkflowNodeTasks(ctx context.Context, workspaceID pgtype.UUID, instance db.WorkflowInstance, node db.WorkflowNodeInstance) {
	tasks, err := h.Queries.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return
	}
	for _, task := range tasks {
		_ = h.materializeWorkflowTask(ctx, workspaceID, instance, node, task)
		h.publishWorkflowTaskUpdated(
			uuidToString(workspaceID), "system", "",
			uuidToString(instance.ID), uuidToString(node.ID), uuidToString(task.ID),
		)
	}
	if h.WorkflowMaterializer != nil {
		h.WorkflowMaterializer.Notify()
	}
}

func (h *Handler) materializeWorkflowTask(
	ctx context.Context,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	task db.WorkflowNodeTask,
) error {
	if featureflags.WorkflowProgressionPaused(
		ctx,
		h.FeatureFlags,
		uuidToString(workspaceID),
	) {
		return errWorkflowProgressionPaused
	}
	if task.IssueID.Valid {
		h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
		return nil
	}
	if !task.ExecutorResolutionID.Valid {
		return errors.New("workflow task requires an executor resolution")
	}
	resolution, err := h.Queries.GetWorkflowExecutorResolutionInWorkspace(
		ctx,
		db.GetWorkflowExecutorResolutionInWorkspaceParams{
			ID: task.ExecutorResolutionID, WorkspaceID: workspaceID,
		},
	)
	if err != nil ||
		resolution.WorkflowNodeTaskID != task.ID ||
		resolution.WorkflowNodeInstanceID != node.ID ||
		resolution.Status != "resolved" ||
		!resolution.ActorType.Valid ||
		!resolution.ActorID.Valid {
		return errors.New("workflow task executor resolution is not resolved")
	}
	startedAt := time.Now()
	materializationOutcome := "failed"
	defer func() {
		h.Metrics.RecordWorkflowMaterialization(
			materializationOutcome,
			time.Since(startedAt),
		)
	}()
	if existing, err := h.Queries.GetIssueByOrigin(ctx, db.GetIssueByOriginParams{
		WorkspaceID: workspaceID, OriginType: pgtype.Text{String: "workflow", Valid: true}, OriginID: task.ID,
	}); err == nil {
		_, bindErr := h.Queries.BindWorkflowNodeTaskIssue(ctx, db.BindWorkflowNodeTaskIssueParams{
			IssueID: existing.ID, ID: task.ID, WorkspaceID: workspaceID,
		})
		if bindErr == nil {
			materializationOutcome = "recovered"
			h.Metrics.RecordWorkflowOperation(
				"materialization_repair",
				"repaired",
			)
		}
		return bindErr
	}
	claimed := task
	if task.MaterializationStatus != "materializing" {
		var err error
		claimed, err = h.Queries.MarkWorkflowNodeTaskMaterializing(ctx, db.MarkWorkflowNodeTaskMaterializingParams{
			ID: task.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return err
		}
	}
	var template workflowdomain.IssueTemplate
	if err := json.Unmarshal(claimed.DefinitionSnapshot, &template); err != nil {
		return h.failWorkflowTaskMaterialization(ctx, workspaceID, task.ID, "invalid task definition")
	}
	host, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: instance.HostIssueID, WorkspaceID: workspaceID})
	if err != nil {
		return h.failWorkflowTaskMaterialization(ctx, workspaceID, task.ID, "host issue not found")
	}
	title := strings.ReplaceAll(template.Title, "{{host.title}}", host.Title)
	status := template.InitialStatus
	if status == "" {
		status = "todo"
	}
	priority := template.Priority
	if priority == "" {
		priority = "none"
	}
	result, err := h.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID: workspaceID, Title: title,
		Description: pgtype.Text{
			String: template.Description, Valid: strings.TrimSpace(template.Description) != "",
		},
		Status: status, Priority: priority,
		AssigneeType: resolution.ActorType, AssigneeID: resolution.ActorID,
		CreatorType: "member", CreatorID: instance.StartedByID,
		ParentIssueID: instance.HostIssueID, ProjectID: host.ProjectID,
		OriginType: pgtype.Text{String: "workflow", Valid: true}, OriginID: task.ID,
		Stage: pgtype.Int4{Int32: node.DisplayOrder, Valid: true}, AllowDuplicate: true,
	}, service.IssueCreateOpts{ActorID: uuidToString(instance.StartedByID), Platform: "workflow"})
	if err != nil {
		if existing, lookupErr := h.Queries.GetIssueByOrigin(ctx, db.GetIssueByOriginParams{
			WorkspaceID: workspaceID, OriginType: pgtype.Text{String: "workflow", Valid: true}, OriginID: task.ID,
		}); lookupErr == nil {
			_, bindErr := h.Queries.BindWorkflowNodeTaskIssue(ctx, db.BindWorkflowNodeTaskIssueParams{
				IssueID: existing.ID, ID: task.ID, WorkspaceID: workspaceID,
			})
			if bindErr == nil {
				materializationOutcome = "recovered"
				h.Metrics.RecordWorkflowOperation(
					"materialization_repair",
					"repaired",
				)
			}
			return bindErr
		}
		return h.failWorkflowTaskMaterialization(ctx, workspaceID, task.ID, err.Error())
	}
	_, err = h.Queries.BindWorkflowNodeTaskIssue(ctx, db.BindWorkflowNodeTaskIssueParams{
		IssueID: result.Issue.ID, ID: task.ID, WorkspaceID: workspaceID,
	})
	if err == nil {
		materializationOutcome = "success"
	}
	return err
}

func (h *Handler) failWorkflowTaskMaterialization(ctx context.Context, workspaceID, taskID pgtype.UUID, message string) error {
	if len(message) > 1000 {
		message = message[:1000]
	}
	_, err := h.Queries.MarkWorkflowNodeTaskMaterializationFailed(ctx, db.MarkWorkflowNodeTaskMaterializationFailedParams{
		LastError: message, ID: taskID, WorkspaceID: workspaceID,
	})
	return err
}

func (h *Handler) writeWorkflowInstanceDetail(w http.ResponseWriter, r *http.Request, instance db.WorkflowInstance, status int) {
	roles, err := h.Queries.ListWorkflowRoleAssignments(r.Context(), db.ListWorkflowRoleAssignmentsParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow roles")
		return
	}
	nodes, err := h.Queries.ListWorkflowNodeInstances(r.Context(), db.ListWorkflowNodeInstancesParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow nodes")
		return
	}
	tasks, err := h.Queries.ListWorkflowInstanceTasks(r.Context(), db.ListWorkflowInstanceTasksParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow tasks")
		return
	}
	roleResponses := make([]workflowRoleAssignmentResponse, len(roles))
	for i, role := range roles {
		roleResponses[i] = workflowRoleAssignmentToResponse(role)
	}
	nodeResponses := make([]workflowNodeResponse, len(nodes))
	for i, node := range nodes {
		nodeResponses[i] = workflowNodeToResponse(node)
	}
	taskResponses := make([]workflowTaskResponse, len(tasks))
	for i, task := range tasks {
		taskResponses[i] = workflowTaskToResponse(task)
	}
	contexts, err := h.loadWorkflowInstanceDisplayContexts(
		r.Context(),
		instance.WorkspaceID,
		[]pgtype.UUID{instance.ID},
	)
	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			"failed to load workflow display context",
		)
		return
	}
	writeJSON(w, status, workflowInstanceDetailResponse{
		Instance: workflowInstanceRuntimeResponseFromFacts(
			instance,
			nodes,
			tasks,
			contexts[uuidToString(instance.ID)],
			h.getIssuePrefix(r.Context(), instance.WorkspaceID),
		),
		RoleAssignments: roleResponses, Nodes: nodeResponses, Tasks: taskResponses,
	})
}

func (h *Handler) GetIssueWorkflow(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return
	}
	hostID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "issue_id")
	if !ok {
		return
	}
	instance, err := h.Queries.GetLatestWorkflowInstanceByHost(r.Context(), db.GetLatestWorkflowInstanceByHostParams{
		HostIssueID: hostID, WorkspaceID: wsUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workflow instance not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow instance")
		return
	}
	h.writeWorkflowInstanceDetail(w, r, instance, http.StatusOK)
}

func (h *Handler) GetWorkflowInstance(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadWorkflowInstance(w, r)
	if !ok {
		return
	}
	h.writeWorkflowInstanceDetail(w, r, instance, http.StatusOK)
}

func (h *Handler) loadWorkflowInstance(w http.ResponseWriter, r *http.Request) (db.WorkflowInstance, bool) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return db.WorkflowInstance{}, false
	}
	instanceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "instanceId"), "workflow_instance_id")
	if !ok {
		return db.WorkflowInstance{}, false
	}
	instance, err := h.Queries.GetWorkflowInstanceInWorkspace(r.Context(), db.GetWorkflowInstanceInWorkspaceParams{
		ID: instanceID, WorkspaceID: wsUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workflow instance not found")
		return db.WorkflowInstance{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow instance")
		return db.WorkflowInstance{}, false
	}
	return instance, true
}

func (h *Handler) ListWorkflowInstances(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	viewerID, ok := parseUUIDOrBadRequest(w, userID, "user_id")
	if !ok {
		return
	}
	viewerMember, err := h.getWorkspaceMember(r.Context(), userID, uuidToString(wsUUID))
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	viewerIsAdmin := viewerMember.Role == "owner" || viewerMember.Role == "admin"
	var status pgtype.Text
	if value := strings.TrimSpace(r.URL.Query().Get("status")); value != "" {
		switch value {
		case "active", "terminal", "needs_setup", "running", "paused",
			"completed", "cancelled", "failed":
			status = pgtype.Text{String: value, Valid: true}
		default:
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
	}
	projectID, ok := optionalWorkflowQueryUUID(w, r, "project_id")
	if !ok {
		return
	}
	templateID, ok := optionalWorkflowQueryUUID(w, r, "template_id")
	if !ok {
		return
	}
	var currentNodeKey pgtype.Text
	if value := strings.TrimSpace(r.URL.Query().Get("current_node_key")); value != "" {
		currentNodeKey = pgtype.Text{String: value, Valid: true}
	}
	ownerID, ok := optionalWorkflowQueryUUID(w, r, "owner_id")
	if !ok {
		return
	}
	var ownerType pgtype.Text
	if value := strings.TrimSpace(r.URL.Query().Get("owner_type")); value != "" {
		switch value {
		case "member", "agent", "squad":
			ownerType = pgtype.Text{String: value, Valid: true}
		default:
			writeError(w, http.StatusBadRequest, "invalid owner_type")
			return
		}
	}
	if ownerID.Valid != ownerType.Valid {
		writeError(
			w,
			http.StatusBadRequest,
			"owner_type and owner_id must be provided together",
		)
		return
	}
	relatedToMe := false
	if value := strings.TrimSpace(r.URL.Query().Get("related_to_me")); value != "" {
		parsed, parseErr := strconv.ParseBool(value)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "related_to_me must be true or false")
			return
		}
		relatedToMe = parsed
	}
	var interventionType pgtype.Text
	if value := strings.TrimSpace(r.URL.Query().Get("intervention_type")); value != "" {
		switch value {
		case "configure_roles",
			"configure_executor",
			"retry_materialization",
			"review_acceptance",
			"submit_result",
			"record_verdict",
			"confirm_activity",
			"complete_activity",
			"recover_activity",
			"resume",
			"reconcile",
			"view_current_activity",
			"none":
			interventionType = pgtype.Text{String: value, Valid: true}
		default:
			writeError(w, http.StatusBadRequest, "invalid intervention_type")
			return
		}
	}
	limit := 50
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	cursorUpdatedAt, cursorInterventionRank, cursorID, ok :=
		parseWorkflowInstanceCursor(
			w, strings.TrimSpace(r.URL.Query().Get("cursor")),
		)
	if !ok {
		return
	}
	filter := db.CountWorkflowInstancesParams{
		ViewerIsAdmin: viewerIsAdmin,
		WorkspaceID:   wsUUID, Status: status, ProjectID: projectID,
		TemplateID: templateID, CurrentNodeKey: currentNodeKey,
		OwnerID: ownerID, OwnerType: ownerType,
		RelatedToMe: relatedToMe, ViewerID: viewerID,
		InterventionType: interventionType,
	}
	total, err := h.Queries.CountWorkflowInstances(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count workflow instances")
		return
	}
	rows, err := h.Queries.ListWorkflowInstances(r.Context(), db.ListWorkflowInstancesParams{
		ViewerIsAdmin: viewerIsAdmin,
		WorkspaceID:   wsUUID, Status: status, ProjectID: projectID,
		TemplateID: templateID, CurrentNodeKey: currentNodeKey,
		OwnerID: ownerID, OwnerType: ownerType,
		RelatedToMe: relatedToMe, ViewerID: viewerID,
		InterventionType: interventionType, CursorUpdatedAt: cursorUpdatedAt,
		CursorInterventionRank: cursorInterventionRank,
		CursorID:               cursorID, RowLimit: int32(limit + 1),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow instances")
		return
	}
	hasNextPage := len(rows) > limit
	if hasNextPage {
		rows = rows[:limit]
	}
	instanceIDs := make([]pgtype.UUID, len(rows))
	for index, row := range rows {
		instanceIDs[index] = row.ID
	}
	contexts, err := h.loadWorkflowInstanceDisplayContexts(
		r.Context(),
		wsUUID,
		instanceIDs,
	)
	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			"failed to load workflow display contexts",
		)
		return
	}
	nodes, err := h.Queries.ListWorkflowNodeInstancesForInstances(
		r.Context(),
		db.ListWorkflowNodeInstancesForInstancesParams{
			WorkspaceID: wsUUID, WorkflowInstanceIds: instanceIDs,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow nodes")
		return
	}
	tasks, err := h.Queries.ListWorkflowTasksForInstances(
		r.Context(),
		db.ListWorkflowTasksForInstancesParams{
			WorkspaceID: wsUUID, WorkflowInstanceIds: instanceIDs,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow tasks")
		return
	}
	participants, err := h.Queries.ListWorkflowCurrentMemberParticipantsForInstances(
		r.Context(),
		db.ListWorkflowCurrentMemberParticipantsForInstancesParams{
			WorkspaceID:         wsUUID,
			WorkflowInstanceIds: instanceIDs,
			ViewerID:            viewerID,
		},
	)
	if err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			"failed to load workflow participants",
		)
		return
	}
	nodesByInstance := make(map[string][]db.WorkflowNodeInstance, len(rows))
	for _, node := range nodes {
		key := uuidToString(node.WorkflowInstanceID)
		nodesByInstance[key] = append(nodesByInstance[key], node)
	}
	tasksByInstance := make(map[string][]db.WorkflowNodeTask, len(rows))
	for _, task := range tasks {
		key := uuidToString(task.WorkflowInstanceID)
		tasksByInstance[key] = append(tasksByInstance[key], task)
	}
	rolesByInstance := make(
		map[string]workflowViewerNodeRoles,
		len(rows),
	)
	for _, participant := range participants {
		instanceKey := uuidToString(participant.WorkflowInstanceID)
		nodeKey := uuidToString(participant.WorkflowNodeInstanceID)
		instanceRoles := rolesByInstance[instanceKey]
		if instanceRoles == nil {
			instanceRoles = workflowViewerNodeRoles{}
			rolesByInstance[instanceKey] = instanceRoles
		}
		nodeRoles := instanceRoles[nodeKey]
		if nodeRoles == nil {
			nodeRoles = map[string]struct{}{}
			instanceRoles[nodeKey] = nodeRoles
		}
		nodeRoles[participant.Role] = struct{}{}
	}
	issuePrefix := h.getIssuePrefix(r.Context(), wsUUID)
	items := make([]workflowInstanceResponse, len(rows))
	for i, row := range rows {
		key := uuidToString(row.ID)
		items[i] = workflowInstanceRuntimeResponseFromFacts(
			row,
			nodesByInstance[key],
			tasksByInstance[key],
			contexts[key],
			issuePrefix,
		)
		items[i].NextAction, items[i].InterventionReason =
			workflowPersonalizedNextAction(
				row,
				nodesByInstance[key],
				tasksByInstance[key],
				viewerID,
				viewerIsAdmin,
				rolesByInstance[key],
			)
	}
	var nextCursor *string
	if hasNextPage && len(rows) > 0 {
		cursor := encodeWorkflowInstanceCursor(
			rows[len(rows)-1],
			workflowInterventionRank(items[len(items)-1].NextAction),
		)
		nextCursor = &cursor
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instances": items, "total": total, "next_cursor": nextCursor,
	})
}

type workflowInstanceCursor struct {
	InterventionRank int32  `json:"intervention_rank"`
	UpdatedAt        string `json:"updated_at"`
	ID               string `json:"id"`
}

func optionalWorkflowQueryUUID(
	w http.ResponseWriter,
	r *http.Request,
	name string,
) (pgtype.UUID, bool) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return pgtype.UUID{}, true
	}
	return parseUUIDOrBadRequest(w, value, name)
}

func parseWorkflowInstanceCursor(
	w http.ResponseWriter,
	value string,
) (pgtype.Timestamptz, int32, pgtype.UUID, bool) {
	if value == "" {
		return pgtype.Timestamptz{}, 0, pgtype.UUID{}, true
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return pgtype.Timestamptz{}, 0, pgtype.UUID{}, false
	}
	var cursor workflowInstanceCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return pgtype.Timestamptz{}, 0, pgtype.UUID{}, false
	}
	if cursor.InterventionRank != 0 && cursor.InterventionRank != 1 {
		writeError(w, http.StatusBadRequest, "invalid cursor intervention rank")
		return pgtype.Timestamptz{}, 0, pgtype.UUID{}, false
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, cursor.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor timestamp")
		return pgtype.Timestamptz{}, 0, pgtype.UUID{}, false
	}
	id, ok := parseUUIDOrBadRequest(w, cursor.ID, "cursor.id")
	if !ok {
		return pgtype.Timestamptz{}, 0, pgtype.UUID{}, false
	}
	return pgtype.Timestamptz{
		Time:  updatedAt,
		Valid: true,
	}, cursor.InterventionRank, id, true
}

func encodeWorkflowInstanceCursor(
	row db.WorkflowInstance,
	interventionRank int32,
) string {
	raw, _ := json.Marshal(workflowInstanceCursor{
		InterventionRank: interventionRank,
		UpdatedAt:        row.UpdatedAt.Time.UTC().Format(time.RFC3339Nano),
		ID:               uuidToString(row.ID),
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func workflowInterventionRank(action string) int32 {
	if action != "none" && action != "view_current_activity" {
		return 0
	}
	return 1
}
