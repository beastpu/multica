package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type createWorkflowRunRequest struct {
	Title             string                        `json:"title"`
	Description       string                        `json:"description,omitempty"`
	Priority          string                        `json:"priority,omitempty"`
	ProjectID         string                        `json:"project_id,omitempty"`
	WorkflowID        string                        `json:"workflow_id"`
	WorkflowVersionID string                        `json:"workflow_version_id,omitempty"`
	HostStatusMode    string                        `json:"host_status_mode,omitempty"`
	Input             json.RawMessage               `json:"input,omitempty"`
	RoleAssignments   []workflowRoleAssignmentInput `json:"role_assignments,omitempty"`
	IdempotencyKey    string                        `json:"idempotency_key"`
}

// CreateWorkflow is the product-level "New workflow" entry point. The host
// Issue and the workflow runtime are committed in one IssueService transaction:
// a failure in either half leaves neither half behind.
func (h *Handler) CreateWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	var req createWorkflowRunRequest
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
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
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
	if existing, err := h.Queries.GetWorkflowInstanceByStartIdempotencyKey(
		r.Context(),
		db.GetWorkflowInstanceByStartIdempotencyKeyParams{
			WorkspaceID: wsUUID, IdempotencyKey: req.IdempotencyKey,
		},
	); err == nil {
		h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
		h.writeWorkflowInstanceDetail(w, r, existing, http.StatusOK)
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to check workflow request")
		return
	}
	templateID, ok := parseUUIDOrBadRequest(w, req.WorkflowID, "workflow_id")
	if !ok {
		return
	}
	template, err := h.Queries.GetWorkflowInWorkspace(
		r.Context(),
		db.GetWorkflowInWorkspaceParams{ID: templateID, WorkspaceID: wsUUID},
	)
	if errors.Is(err, pgx.ErrNoRows) || template.Status != "published" {
		writeError(w, http.StatusNotFound, "published workflow template not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow template")
		return
	}
	versionID := template.LatestPublishedVersionID
	if strings.TrimSpace(req.WorkflowVersionID) != "" {
		versionID, ok = parseUUIDOrBadRequest(w, req.WorkflowVersionID, "workflow_version_id")
		if !ok {
			return
		}
	}
	version, err := h.Queries.GetWorkflowVersionInWorkspace(
		r.Context(),
		db.GetWorkflowVersionInWorkspaceParams{ID: versionID, WorkspaceID: wsUUID},
	)
	if errors.Is(err, pgx.ErrNoRows) || version.WorkflowID != template.ID {
		writeError(w, http.StatusBadRequest, "workflow version not found")
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
	req.RoleAssignments = defaultWorkflowOwnerAssignment(
		definition, req.RoleAssignments, userID,
	)
	assignments, missingRoles, ok := h.validateWorkflowRoleAssignments(
		w, r, wsUUID, workspaceID, definition, req.RoleAssignments,
	)
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
		hostStatusMode = "managed"
	}
	if hostStatusMode != "managed" && hostStatusMode != "independent" {
		writeError(w, http.StatusBadRequest, "host_status_mode must be managed or independent")
		return
	}
	priority := req.Priority
	if priority == "" {
		priority = "none"
	}
	if !validateIssueEnum(w, "priority", priority, validIssuePriorities) {
		return
	}
	var projectID pgtype.UUID
	if strings.TrimSpace(req.ProjectID) != "" {
		projectID, ok = parseUUIDOrBadRequest(w, req.ProjectID, "project_id")
		if !ok {
			return
		}
	}
	var hostAssigneeType pgtype.Text
	var hostAssigneeID pgtype.UUID
	for _, assignment := range assignments {
		if assignment.RoleKey == "owner" {
			hostAssigneeType = pgtype.Text{String: assignment.ActorType, Valid: true}
			hostAssigneeID = assignment.ActorID
			break
		}
	}

	var createdInstance db.WorkflowInstance
	var activeNodes []db.WorkflowNodeInstance
	prefix := h.getIssuePrefix(r.Context(), wsUUID)
	result, createErr := h.IssueService.Create(
		r.Context(),
		service.IssueCreateParams{
			WorkspaceID: wsUUID, Title: req.Title,
			Description: pgtype.Text{String: req.Description, Valid: req.Description != ""},
			Status:      "in_progress", Priority: priority,
			AssigneeType: hostAssigneeType, AssigneeID: hostAssigneeID,
			CreatorType: "member", CreatorID: userUUID, ProjectID: projectID,
		},
		service.IssueCreateOpts{
			ActorID: userID, Platform: "workflow",
			BroadcastPayload: func(issue db.Issue, _ []db.Attachment, _ []db.IssueLabel) map[string]any {
				return map[string]any{"issue": issueToResponse(issue, prefix)}
			},
			WithinCreateTransaction: func(
				ctx context.Context,
				qtx *db.Queries,
				host db.Issue,
			) error {
				var runtimeErr error
				createdInstance, activeNodes, runtimeErr = h.createWorkflowRuntime(
					ctx, qtx, workflowRuntimeStartParams{
						WorkspaceID: wsUUID, WorkflowID: template.ID,
						WorkflowVersionID: version.ID, HostIssueID: host.ID,
						Title:          host.Title,
						HostStatusMode: hostStatusMode, Input: normalizedInput,
						StartedByID: userUUID, IdempotencyKey: req.IdempotencyKey,
						Definition: definition, Plan: plan, Assignments: assignments,
						MissingRoles: missingRoles,
					},
				)
				return runtimeErr
			},
		},
	)
	if createErr != nil {
		// A concurrent replay loses the global start-idempotency race. Its host
		// Issue transaction is rolled back; return the winner's instance.
		if existing, lookupErr := h.Queries.GetWorkflowInstanceByStartIdempotencyKey(
			r.Context(),
			db.GetWorkflowInstanceByStartIdempotencyKeyParams{
				WorkspaceID: wsUUID, IdempotencyKey: req.IdempotencyKey,
			},
		); lookupErr == nil {
			h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
			h.writeWorkflowInstanceDetail(w, r, existing, http.StatusOK)
			return
		}
		switch {
		case errors.Is(createErr, service.ErrActiveDuplicate):
			writeError(w, http.StatusConflict, "an active issue with this title already exists")
		case errors.Is(createErr, service.ErrProjectNotFound):
			writeError(w, http.StatusBadRequest, "project not found in this workspace")
		default:
			writeError(w, http.StatusInternalServerError, "failed to create workflow")
		}
		return
	}
	if !result.Issue.ID.Valid || !createdInstance.ID.Valid {
		writeError(w, http.StatusInternalServerError, "workflow creation did not commit")
		return
	}
	h.recordWorkflowStarted(
		r.Context(), "new_workflow", createdInstance, activeNodes,
		len(missingRoles),
	)
	if createdInstance.Status == "completed" {
		_ = h.updateManagedWorkflowHostStatus(r.Context(), createdInstance, "done")
	}
	h.publishWorkflowInstanceUpdated(
		workspaceID, "member", userID,
		uuidToString(createdInstance.ID), firstWorkflowNodeID(activeNodes),
	)
	for _, activeNode := range activeNodes {
		h.publishWorkflowNodeUpdated(
			workspaceID, "member", userID,
			uuidToString(createdInstance.ID), uuidToString(activeNode.ID),
		)
		h.materializeWorkflowNodeTasks(r.Context(), wsUUID, createdInstance, activeNode)
	}
	h.notifyWorkflowNeedsSetup(r.Context(), createdInstance, missingRoles)
	h.writeWorkflowInstanceDetail(w, r, createdInstance, http.StatusCreated)
}

func defaultWorkflowOwnerAssignment(
	definition workflowdomain.Definition,
	assignments []workflowRoleAssignmentInput,
	userID string,
) []workflowRoleAssignmentInput {
	for _, assignment := range assignments {
		if assignment.RoleKey == "owner" {
			return assignments
		}
	}
	for _, role := range definition.Roles {
		if role.Key != "owner" {
			continue
		}
		for _, actorType := range role.AllowedActorTypes {
			if actorType == "member" {
				return append(assignments, workflowRoleAssignmentInput{
					RoleKey: "owner", ActorType: "member", ActorID: userID,
					Source: "user_selected",
				})
			}
		}
	}
	return assignments
}
