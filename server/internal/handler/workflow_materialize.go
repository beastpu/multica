package handler

// Run creation and materialisation: turning a published definition into a run,
// and a run's activated node into the issues and agent tasks that carry it.
// Split out of workflow_instance.go, which held both this and the HTTP surface
// that reads runs back — the engine half has no request to answer to.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/service"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

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
		WorkspaceID: params.WorkspaceID, WorkflowID: params.WorkflowID,
		WorkflowVersionID: params.WorkflowVersionID, HostIssueID: params.HostIssueID,
		Title:  params.Title,
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

// workflowNodeParticipantRoles lists the role slots a node materializes as
// participants: its owner, and its reviewer when one is named by role. The
// acceptance approver is gone from here because acceptance is no longer a node.
//
// The reviewer is seated rather than only read from the definition because the
// canvas has to show who a node in review is waiting on, and "waiting on whom"
// is answered from participants everywhere else.
func workflowNodeParticipantRoles(
	nodeDefinition workflowdomain.NodeDefinition,
) []workflowParticipantRole {
	roles := make([]workflowParticipantRole, 0, 2)
	if nodeDefinition.OwnerRole != "" {
		roles = append(roles, workflowParticipantRole{nodeDefinition.OwnerRole, "owner"})
	}
	if nodeDefinition.Reviewer != nil && nodeDefinition.Reviewer.Kind == "role" {
		roles = append(roles, workflowParticipantRole{nodeDefinition.Reviewer.Role, "reviewer"})
	}
	return roles
}

// writeWorkflowNodeParticipants resolves the node's role slots into
// participant rows. A node without an owner role falls back to a pinned actor
// executor, which is what makes a directly assigned member the node owner.
func writeWorkflowNodeParticipants(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	participantRoles []workflowParticipantRole,
	roles map[string]validatedWorkflowRoleAssignment,
) error {
	for _, participant := range participantRoles {
		assignment, exists := roles[participant.key]
		if !exists {
			continue
		}
		if _, err := q.CreateWorkflowNodeParticipant(ctx, db.CreateWorkflowNodeParticipantParams{
			WorkspaceID: workspaceID, WorkflowNodeInstanceID: node.ID, Role: participant.role,
			ActorType: assignment.ActorType, ActorID: assignment.ActorID,
		}); err != nil {
			return fmt.Errorf("create participant: %w", err)
		}
	}
	if reviewer := nodeDefinition.Reviewer; reviewer != nil && reviewer.Kind == "actor" {
		assignment, err := directWorkflowExecutorAssignment(
			reviewer.ActorType,
			reviewer.ActorID,
		)
		if err != nil {
			return fmt.Errorf("resolve direct node reviewer: %w", err)
		}
		if err := validateWorkflowExecutorActor(ctx, q, workspaceID, assignment); err != nil {
			return fmt.Errorf("validate direct node reviewer: %w", err)
		}
		if _, err := q.CreateWorkflowNodeParticipant(ctx, db.CreateWorkflowNodeParticipantParams{
			WorkspaceID: workspaceID, WorkflowNodeInstanceID: node.ID, Role: "reviewer",
			ActorType: assignment.ActorType, ActorID: assignment.ActorID,
		}); err != nil {
			return fmt.Errorf("create direct node reviewer: %w", err)
		}
	}
	if nodeDefinition.OwnerRole != "" {
		return nil
	}
	if nodeDefinition.Executor == nil || nodeDefinition.Executor.Kind != "actor" {
		return nil
	}
	assignment, err := directWorkflowExecutorAssignment(
		nodeDefinition.Executor.ActorType,
		nodeDefinition.Executor.ActorID,
	)
	if err != nil {
		return fmt.Errorf("resolve direct node owner: %w", err)
	}
	if err := validateWorkflowExecutorActor(ctx, q, workspaceID, assignment); err != nil {
		return nil
	}
	if _, err := q.CreateWorkflowNodeParticipant(ctx, db.CreateWorkflowNodeParticipantParams{
		WorkspaceID: workspaceID, WorkflowNodeInstanceID: node.ID, Role: "owner",
		ActorType: assignment.ActorType, ActorID: assignment.ActorID,
	}); err != nil {
		return fmt.Errorf("create direct node owner: %w", err)
	}
	return nil
}

// refreshWorkflowNodeParticipants re-resolves participants for node attempts
// that have not reached a terminal state, so a role reassignment reaches the
// work already in flight. Materialized issue assignees are deliberately left
// alone: reassigning an issue is its own explicit action.
func refreshWorkflowNodeParticipants(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	nodes []db.WorkflowNodeInstance,
	definition workflowdomain.Definition,
	plan workflowdomain.GraphPlan,
	roles map[string]validatedWorkflowRoleAssignment,
) error {
	for _, node := range nodes {
		switch node.Status {
		case "pending", "ready", "active", "in_review", "waiting", "blocked":
		default:
			continue
		}
		nodeDefinition, ok := plan.Node(node.NodeKey)
		if !ok || nodeDefinition.Kind != "activity" {
			continue
		}
		if err := q.DeleteWorkflowNodeParticipants(
			ctx,
			db.DeleteWorkflowNodeParticipantsParams{
				WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
			},
		); err != nil {
			return fmt.Errorf("clear participants: %w", err)
		}
		if err := writeWorkflowNodeParticipants(
			ctx, q, workspaceID, node, nodeDefinition,
			workflowNodeParticipantRoles(nodeDefinition), roles,
		); err != nil {
			return err
		}
	}
	return nil
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
	participantRoles := workflowNodeParticipantRoles(nodeDefinition)
	if err := writeWorkflowNodeParticipants(
		ctx, q, workspaceID, node, nodeDefinition, participantRoles, roles,
	); err != nil {
		return false, err
	}
	needsSetup := false
	if nodeDefinition.IssuePolicy == "none" &&
		workflowdomain.HasExecutor(nodeDefinition.Executor) {
		decision, err := resolveWorkflowNodeExecutor(
			ctx, q, workspaceID, nodeDefinition, roles,
		)
		if err != nil {
			return false, fmt.Errorf("resolve direct node executor: %w", err)
		}
		if decision.Assignment == nil ||
			decision.Assignment.ActorType == "agent" ||
			decision.Assignment.ActorType == "squad" {
			snapshot, _ := json.Marshal(nodeDefinition)
			task, err := q.CreateWorkflowNodeTask(ctx, db.CreateWorkflowNodeTaskParams{
				WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID,
				WorkflowNodeInstanceID: node.ID, TaskKey: "execution",
				Source: "execution", Required: true, DefinitionSnapshot: snapshot,
				MaterializationStatus: "pending_materialization",
				CreatedByType:         instance.StartedByType, CreatedByID: instance.StartedByID,
			})
			if err != nil {
				return false, fmt.Errorf("create direct node task: %w", err)
			}
			if _, err := createWorkflowExecutorResolution(
				ctx, q, workspaceID, instance, node, task, decision,
			); err != nil {
				return false, fmt.Errorf("create direct executor resolution: %w", err)
			}
			needsSetup = decision.Assignment == nil
		}
	}
	for _, issueTemplate := range workflowdomain.NodeIssueTemplates(nodeDefinition) {
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
		decision, err := resolveWorkflowNodeExecutor(
			ctx, q, workspaceID, nodeDefinition, roles,
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
	for _, issueTemplate := range workflowdomain.NodeIssueTemplates(nodeDefinition) {
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
		decision, resolutionErr := resolveWorkflowNodeExecutor(
			ctx, q, workspaceID, nodeDefinition, roleMap,
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
	// Materialization is the moment the carriers exist, so it is the earliest
	// point the mirror has anything to write to. Marking them started at
	// activation instead — which is where the other transitions are handled —
	// ran ten milliseconds too early and found no issue to update: a silent
	// no-op that looked like coverage.
	if workflowNodeIsOpen(node) {
		h.syncWorkflowNodeIssueStatus(ctx, workspaceID, node, "activated")
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
	if task.Source == "execution" {
		return h.materializeWorkflowAgentTask(
			ctx, workspaceID, instance, node, task, resolution,
		)
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
	if node.Attempt > 1 {
		reused, err := h.reuseWorkflowReworkIssue(ctx, workspaceID, node, task)
		if err != nil {
			return err
		}
		if reused {
			materializationOutcome = "reworked"
			return nil
		}
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
	var nodeDefinition workflowdomain.NodeDefinition
	_ = json.Unmarshal(node.DefinitionSnapshot, &nodeDefinition)
	var host db.Issue
	var hostIdentifier string
	var projectID pgtype.UUID
	if instance.HostIssueID.Valid {
		host, err = h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
			ID: instance.HostIssueID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return h.failWorkflowTaskMaterialization(ctx, workspaceID, task.ID, "host issue not found")
		}
		hostIdentifier = h.getIssuePrefix(ctx, workspaceID) + "-" + strconv.Itoa(int(host.Number))
		projectID = host.ProjectID
	}
	titleContext := instance.Title
	if host.ID.Valid {
		titleContext = host.Title
	}
	title := strings.ReplaceAll(template.Title, "{{host.title}}", titleContext)
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
		Description: workflowTaskIssueDescription(
			template.Description,
			nodeDefinition.Description,
			hostIdentifier,
			host.Description.String,
			h.workflowPurpose(ctx, workspaceID, instance, host.ID.Valid),
		),
		Status: status, Priority: priority,
		AssigneeType: resolution.ActorType, AssigneeID: resolution.ActorID,
		CreatorType: "member", CreatorID: instance.StartedByID,
		ParentIssueID: instance.HostIssueID, ProjectID: projectID,
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
		h.stampWorkflowIssueMetadata(ctx, workspaceID, result.Issue, instance, node, hostIdentifier)
	}
	return err
}

func (h *Handler) materializeWorkflowAgentTask(
	ctx context.Context,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	task db.WorkflowNodeTask,
	resolution db.WorkflowExecutorResolution,
) error {
	latest, err := h.Queries.GetLatestAgentTaskForWorkflowNodeTask(
		ctx, task.ID,
	)
	if err == nil {
		if latest.Status == "failed" || latest.Status == "cancelled" {
			if _, retryErr := h.TaskService.RetryWorkflowNodeTask(ctx, latest); retryErr != nil {
				return h.failWorkflowTaskMaterialization(
					ctx, workspaceID, task.ID, retryErr.Error(),
				)
			}
		}
		_, markErr := h.Queries.MarkWorkflowNodeTaskExecuted(
			ctx, db.MarkWorkflowNodeTaskExecutedParams{ID: task.ID, WorkspaceID: workspaceID},
		)
		return markErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	agentID := resolution.ActorID
	var squadID pgtype.UUID
	switch resolution.ActorType.String {
	case "agent":
	case "squad":
		squad, err := h.Queries.GetSquadInWorkspace(
			ctx, db.GetSquadInWorkspaceParams{ID: resolution.ActorID, WorkspaceID: workspaceID},
		)
		if err != nil {
			return h.failWorkflowTaskMaterialization(ctx, workspaceID, task.ID, "squad executor not found")
		}
		agentID = squad.LeaderID
		squadID = squad.ID
	default:
		return h.failWorkflowTaskMaterialization(
			ctx, workspaceID, task.ID, "direct workflow execution requires an agent or squad",
		)
	}
	var nodeDefinition workflowdomain.NodeDefinition
	if err := json.Unmarshal(node.DefinitionSnapshot, &nodeDefinition); err != nil {
		return h.failWorkflowTaskMaterialization(ctx, workspaceID, task.ID, "invalid node definition")
	}
	prompt := strings.TrimSpace(nodeDefinition.Description)
	if prompt == "" {
		prompt = "Complete the workflow activity: " + node.NameSnapshot
	}
	var runInput struct {
		Instructions string `json:"instructions"`
	}
	if json.Unmarshal(instance.Input, &runInput) == nil {
		if instructions := strings.TrimSpace(runInput.Instructions); instructions != "" {
			prompt = instructions + "\n\n" + prompt
		}
	}
	if _, err := h.TaskService.EnqueueWorkflowNodeTask(
		ctx, workspaceID, instance.StartedByID, task.ID, instance.ID, node.ID,
		agentID, squadID, instance.Title, prompt,
	); err != nil {
		return h.failWorkflowTaskMaterialization(ctx, workspaceID, task.ID, err.Error())
	}
	_, err = h.Queries.MarkWorkflowNodeTaskExecuted(
		ctx, db.MarkWorkflowNodeTaskExecutedParams{ID: task.ID, WorkspaceID: workspaceID},
	)
	return err
}

// reuseWorkflowReworkIssue continues a rework attempt on the issue an earlier
// attempt of the same node already used, instead of opening a second issue for
// the same piece of work.
//
// Reuse is what makes rework legible to whoever picks it up: the executor sees
// its own prior attempt, the review that rejected it, and the whole thread, on
// the issue it already knows. A fresh issue would hand it a blank slate and
// strand the old one in a non-terminal status, still assigned to someone who is
// no longer on the hook.
//
// Reports false when no earlier attempt left an issue behind — a first attempt,
// or a task key a newer template version introduced. Callers fall through to
// ordinary materialization.
func (h *Handler) reuseWorkflowReworkIssue(
	ctx context.Context,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
	task db.WorkflowNodeTask,
) (bool, error) {
	priorIssueID, err := h.Queries.GetPriorAttemptWorkflowTaskIssue(
		ctx,
		db.GetPriorAttemptWorkflowTaskIssueParams{
			WorkspaceID:        workspaceID,
			WorkflowInstanceID: task.WorkflowInstanceID,
			NodeKey:            node.NodeKey,
			TaskKey:            task.TaskKey,
			Attempt:            node.Attempt,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !priorIssueID.Valid {
		return false, nil
	}
	// Reopen before binding: a bound task whose issue still reads "done" would
	// let the node complete again on the previous attempt's outcome.
	reopened, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID: priorIssueID, Status: "todo", WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, err
	}
	if _, err := h.Queries.BindWorkflowNodeTaskIssue(
		ctx,
		db.BindWorkflowNodeTaskIssueParams{
			IssueID: priorIssueID, ID: task.ID, WorkspaceID: workspaceID,
		},
	); err != nil {
		return false, err
	}
	// Reopening alone does not wake an agent assignee — nothing re-dispatches a
	// status change the way an assignment does.
	if _, err := h.TaskService.EnqueueTaskForIssue(ctx, reopened); err != nil {
		slog.Warn("enqueue agent task for workflow rework failed",
			"issue_id", uuidToString(priorIssueID),
			"node_key", node.NodeKey,
			"attempt", node.Attempt,
			"error", err)
	}
	return true, nil
}

// workflowHostExcerpt renders the host requirement as a blockquote under the
// reference line.
//
// It is quoted rather than transcribed: the identifier above it names where the
// authoritative text lives, so a reader who finds the excerpt truncated — or
// suspects it is stale, because the host issue keeps being edited and this is a
// snapshot taken at materialization — knows exactly where to go.
func workflowHostExcerpt(hostDescription string) string {
	text := strings.TrimSpace(hostDescription)
	if text == "" {
		return ""
	}
	truncated := false
	if runes := []rune(text); len(runes) > maxHostExcerptRunes {
		text = strings.TrimSpace(string(runes[:maxHostExcerptRunes]))
		truncated = true
	}
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimRight("> "+line, " ")
	}
	if truncated {
		lines = append(lines, "> …")
	}
	return strings.Join(lines, "\n")
}

// workflowTaskIssueDescription writes what the executor of this task is being
// asked to do: the node's own instructions, then the requirement it serves.
//
// The requirement travels as an identifier plus a quoted excerpt. The
// identifier is what stays true — the frontend autolinks it into a mention card
// carrying the live title and status, and it is where an edit to the
// requirement lands. The excerpt is a convenience snapshot: without it the
// child issue says only "Parent requirement: MUL-123", which is nothing to work
// from on a phone or in a notification. workflowPurpose is the last resort — a
// run with no host issue has nothing else that says why it is running at all.
//
// English matches the rest of the server's generated content; there is no i18n
// layer on this side.
func workflowTaskIssueDescription(
	templateDescription string,
	nodeDescription string,
	hostIdentifier string,
	hostDescription string,
	workflowPurpose string,
) pgtype.Text {
	instructions := strings.TrimSpace(templateDescription)
	if instructions == "" {
		instructions = strings.TrimSpace(nodeDescription)
	}
	parts := make([]string, 0, 2)
	if instructions != "" {
		parts = append(parts, instructions)
	}
	if reference := strings.TrimSpace(hostIdentifier); reference != "" {
		block := workflowHostReferencePrefix + reference
		if excerpt := workflowHostExcerpt(hostDescription); excerpt != "" {
			block += "\n>\n" + excerpt
		}
		parts = append(parts, block)
	} else if purpose := strings.TrimSpace(workflowPurpose); purpose != "" {
		parts = append(parts, workflowPurposePrefix+purpose)
	}
	if len(parts) == 0 {
		return pgtype.Text{}
	}
	return pgtype.Text{String: strings.Join(parts, "\n\n"), Valid: true}
}

// workflowPurpose is the workflow's own description, read only when a run has
// no host issue to point at. It says what this kind of run is for rather than
// what this run was asked for, which is weaker than a requirement — and still
// the only thing standing between an executor and no statement of intent.
func (h *Handler) workflowPurpose(
	ctx context.Context,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	hasHostIssue bool,
) string {
	if hasHostIssue {
		return ""
	}
	version, err := h.Queries.GetWorkflowVersionInWorkspace(
		ctx,
		db.GetWorkflowVersionInWorkspaceParams{
			ID: instance.WorkflowVersionID, WorkspaceID: workspaceID,
		},
	)
	if err != nil {
		return ""
	}
	workflow, err := h.Queries.GetWorkflowInWorkspace(ctx, db.GetWorkflowInWorkspaceParams{
		ID: version.WorkflowID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(workflow.Description)
}

// workflowIssueMetadata builds the navigation payload stamped onto a node child
// issue. It carries invariants only — the workflow run, the node it belongs to,
// and the host issue. Anything that changes while the workflow runs (upstream
// and downstream nodes, handoff summaries, artifacts) is deliberately absent:
// this is a snapshot taken at creation, while the graph is live, so a stored
// edge list would silently go stale the moment a template is republished or a
// node is skipped or rolled back. Callers read the live shape from the API.
func workflowIssueMetadata(instanceID, nodeKey, hostIssue string) ([]byte, error) {
	return json.Marshal(map[string]string{
		"instance_id": instanceID,
		"node_key":    nodeKey,
		"host_issue":  hostIssue,
	})
}

// stampWorkflowIssueMetadata records the workflow coordinates on a freshly
// materialized node child issue. Provenance is already authoritative via
// origin_type/origin_id, so this is a convenience index for agents and the UI:
// a failure leaves the issue correct but harder to navigate from, and must not
// fail a materialization that already succeeded.
func (h *Handler) stampWorkflowIssueMetadata(
	ctx context.Context,
	workspaceID pgtype.UUID,
	issue db.Issue,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	hostIssue string,
) {
	value, err := workflowIssueMetadata(
		uuidToString(instance.ID), node.NodeKey, hostIssue,
	)
	if err != nil {
		return
	}
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID: issue.ID, WorkspaceID: workspaceID,
		Key: workflowIssueMetadataKey, Value: value,
	}); err != nil {
		slog.Warn("workflow: failed to stamp issue metadata",
			"issue_id", uuidToString(issue.ID),
			"workflow_instance_id", uuidToString(instance.ID),
			"node_key", node.NodeKey,
			"error", err,
		)
	}
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
