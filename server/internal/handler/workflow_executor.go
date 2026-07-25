package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type workflowExecutorDecision struct {
	Assignment *validatedWorkflowRoleAssignment
	Strategy   string
	Candidates []byte
	Reason     string
	Snapshot   []byte
}

func resolveWorkflowTaskExecutor(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	node workflowdomain.NodeDefinition,
	task workflowdomain.IssueTemplate,
	roles map[string]validatedWorkflowRoleAssignment,
) (workflowExecutorDecision, error) {
	if task.AssigneeType != "" && task.AssigneeID != "" {
		assignment, err := directWorkflowExecutorAssignment(
			task.AssigneeType,
			task.AssigneeID,
		)
		if err != nil {
			return workflowExecutorDecision{}, err
		}
		if err := validateWorkflowExecutorActor(
			ctx,
			q,
			workspaceID,
			assignment,
		); err == nil {
			return workflowExecutorDecision{
				Assignment: &assignment,
				Strategy:   "fixed_actor",
				Candidates: workflowExecutorCandidates(assignment),
				Reason:     "Resolved from the issue template direct assignee",
				Snapshot: workflowDirectActorSnapshot(
					"fixed_actor",
					task.AssigneeType,
					task.AssigneeID,
				),
			}, nil
		}
		return workflowExecutorDecision{
			Strategy:   "manual",
			Candidates: workflowExecutorCandidates(assignment),
			Reason:     "The issue template direct assignee is unavailable",
			Snapshot: workflowDirectActorSnapshot(
				"fixed_actor",
				task.AssigneeType,
				task.AssigneeID,
			),
		}, nil
	}
	if task.AssigneeRole != "" {
		if assignment, ok := roles[task.AssigneeRole]; ok {
			return workflowExecutorDecision{
				Assignment: &assignment, Strategy: "fixed_role",
				Candidates: workflowExecutorCandidates(assignment),
				Reason:     "Resolved from the issue template assignee role",
				Snapshot:   workflowExecutorSnapshot("fixed_role", task.AssigneeRole, "", "", ""),
			}, nil
		}
	}
	for _, strategy := range node.Executor.Strategies {
		switch strategy.Kind {
		case "fixed_actor":
			assignment, err := directWorkflowExecutorAssignment(
				strategy.ActorType,
				strategy.ActorID,
			)
			if err != nil {
				return workflowExecutorDecision{}, err
			}
			if err := validateWorkflowExecutorActor(
				ctx,
				q,
				workspaceID,
				assignment,
			); err == nil {
				return workflowExecutorDecision{
					Assignment: &assignment,
					Strategy:   strategy.Kind,
					Candidates: workflowExecutorCandidates(assignment),
					Reason:     "Resolved from the node direct executor",
					Snapshot: workflowDirectActorSnapshot(
						strategy.Kind,
						strategy.ActorType,
						strategy.ActorID,
					),
				}, nil
			}
		case "fixed_role", "fallback_role":
			if assignment, ok := roles[strategy.Role]; ok {
				return workflowExecutorDecision{
					Assignment: &assignment, Strategy: strategy.Kind,
					Candidates: workflowExecutorCandidates(assignment),
					Reason:     "Resolved from workflow role " + strategy.Role,
					Snapshot: workflowExecutorSnapshot(
						strategy.Kind, strategy.Role, "", "", "",
					),
				}, nil
			}
		case "previous_selected":
			assignment, candidates, reason, err := resolvePreviousSelectedExecutor(
				ctx, q, workspaceID, instance, strategy,
			)
			if err != nil {
				return workflowExecutorDecision{}, err
			}
			if assignment != nil {
				return workflowExecutorDecision{
					Assignment: assignment, Strategy: strategy.Kind,
					Candidates: candidates, Reason: reason,
					Snapshot: workflowExecutorSnapshot(
						strategy.Kind, "", "", strategy.Node, strategy.Field,
					),
				}, nil
			}
		case "capability_match":
			assignment, candidates, reason, err := resolveCapabilityExecutor(
				ctx, q, workspaceID, roles[strategy.Role], strategy.Capability,
			)
			if err != nil {
				return workflowExecutorDecision{}, err
			}
			if assignment != nil {
				return workflowExecutorDecision{
					Assignment: assignment, Strategy: strategy.Kind,
					Candidates: candidates, Reason: reason,
					Snapshot: workflowExecutorSnapshot(
						strategy.Kind, strategy.Role, strategy.Capability, "", "",
					),
				}, nil
			}
		case "manual":
			return workflowExecutorDecision{
				Strategy: "manual", Candidates: []byte("[]"),
				Reason:   "Manual executor selection is required",
				Snapshot: workflowExecutorSnapshot("manual", "", "", "", ""),
			}, nil
		}
	}
	return workflowExecutorDecision{
		Strategy: "manual", Candidates: []byte("[]"),
		Reason:   "No executor strategy resolved; manual selection is required",
		Snapshot: workflowExecutorSnapshot("manual", "", "", "", ""),
	}, nil
}

func directWorkflowExecutorAssignment(
	actorType string,
	actorID string,
) (validatedWorkflowRoleAssignment, error) {
	parsedActorID, err := util.ParseUUID(actorID)
	if err != nil {
		return validatedWorkflowRoleAssignment{}, err
	}
	return validatedWorkflowRoleAssignment{
		ActorType: actorType,
		ActorID:   parsedActorID,
		Source:    "template",
	}, nil
}

func resolvePreviousSelectedExecutor(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	strategy workflowdomain.ExecutorStrategy,
) (*validatedWorkflowRoleAssignment, []byte, string, error) {
	sourceNode, err := q.GetLatestWorkflowNodeAttempt(ctx, db.GetLatestWorkflowNodeAttemptParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: workspaceID, NodeKey: strategy.Node,
	})
	if errors.Is(err, pgx.ErrNoRows) || !sourceNode.LatestSubmissionID.Valid {
		return nil, []byte("[]"), "Upstream submission has no selected actor", nil
	}
	if err != nil {
		return nil, nil, "", err
	}
	submission, err := q.GetWorkflowSubmissionInWorkspace(ctx, db.GetWorkflowSubmissionInWorkspaceParams{
		ID: sourceNode.LatestSubmissionID, WorkspaceID: workspaceID,
	})
	if err != nil || submission.Status != "valid" {
		return nil, []byte("[]"), "Upstream submission is not valid", nil
	}
	payload := map[string]any{}
	if json.Unmarshal(submission.Payload, &payload) != nil {
		return nil, []byte("[]"), "Upstream submission payload is invalid", nil
	}
	actorIDText, ok := payload[strategy.Field].(string)
	if !ok || strings.TrimSpace(actorIDText) == "" {
		return nil, []byte("[]"), "Upstream submission did not select an actor", nil
	}
	actorID, err := util.ParseUUID(actorIDText)
	if err != nil {
		return nil, []byte("[]"), "Upstream submission selected an invalid actor", nil
	}
	var sourceDefinition workflowdomain.NodeDefinition
	if json.Unmarshal(sourceNode.DefinitionSnapshot, &sourceDefinition) != nil ||
		sourceDefinition.SubmissionSchema == nil {
		return nil, nil, "", errors.New("upstream workflow node definition is invalid")
	}
	actorType := ""
	for _, field := range sourceDefinition.SubmissionSchema.Fields {
		if field.Key == strategy.Field {
			actorType = field.Type
			break
		}
	}
	assignment := validatedWorkflowRoleAssignment{
		ActorType: actorType, ActorID: actorID, Source: "copied",
	}
	if err := validateWorkflowExecutorActor(ctx, q, workspaceID, assignment); err != nil {
		return nil, workflowExecutorCandidates(assignment),
			"Upstream selected actor is unavailable", nil
	}
	return &assignment, workflowExecutorCandidates(assignment),
		"Resolved from validated upstream submission " + strategy.Node + "." + strategy.Field,
		nil
}

func resolveCapabilityExecutor(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	pool validatedWorkflowRoleAssignment,
	capability string,
) (*validatedWorkflowRoleAssignment, []byte, string, error) {
	if !pool.ActorID.Valid || (pool.ActorType != "agent" && pool.ActorType != "squad") {
		return nil, []byte("[]"), "Capability pool role is not assigned", nil
	}
	skills, err := q.ListAgentSkillsByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, nil, "", err
	}
	matchingAgents := map[pgtype.UUID]bool{}
	for _, skill := range skills {
		if skill.Enabled && strings.EqualFold(strings.TrimSpace(skill.Name), strings.TrimSpace(capability)) {
			matchingAgents[skill.AgentID] = true
		}
	}
	candidateRows := make([]map[string]any, 0)
	switch pool.ActorType {
	case "agent":
		if !matchingAgents[pool.ActorID] {
			return nil, []byte("[]"), "Assigned pool agent does not provide the required capability", nil
		}
		if err := validateWorkflowExecutorActor(ctx, q, workspaceID, pool); err != nil {
			return nil, []byte("[]"), "Assigned capability agent is unavailable", nil
		}
		candidateRows = append(candidateRows, map[string]any{
			"actor_type": "agent", "actor_id": uuidToString(pool.ActorID),
			"capability": capability, "available": true,
		})
	case "squad":
		squad, err := q.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
			ID: pool.ActorID, WorkspaceID: workspaceID,
		})
		if err != nil || squad.ArchivedAt.Valid {
			return nil, []byte("[]"), "Assigned capability squad is unavailable", nil
		}
		members, err := q.ListSquadMemberPreviewRowsBySquad(ctx, pool.ActorID)
		if err != nil {
			return nil, nil, "", err
		}
		for _, member := range members {
			if member.MemberType != "agent" || !matchingAgents[member.MemberID] {
				continue
			}
			agentAssignment := validatedWorkflowRoleAssignment{
				ActorType: "agent", ActorID: member.MemberID,
			}
			available := validateWorkflowExecutorActor(
				ctx, q, workspaceID, agentAssignment,
			) == nil
			candidateRows = append(candidateRows, map[string]any{
				"actor_type": "agent", "actor_id": uuidToString(member.MemberID),
				"capability": capability, "available": available,
			})
		}
		availableCandidate := false
		for _, candidate := range candidateRows {
			availableCandidate = availableCandidate || candidate["available"] == true
		}
		if !availableCandidate {
			encoded, _ := json.Marshal(candidateRows)
			return nil, encoded, "No available squad member provides the required capability", nil
		}
	}
	encoded, _ := json.Marshal(candidateRows)
	return &pool, encoded, "Resolved from capability " + capability + " in the assigned pool", nil
}

func validateWorkflowExecutorActor(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	assignment validatedWorkflowRoleAssignment,
) error {
	switch assignment.ActorType {
	case "member":
		_, err := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID: assignment.ActorID, WorkspaceID: workspaceID,
		})
		return err
	case "agent":
		agent, err := q.GetAgent(ctx, assignment.ActorID)
		if err != nil {
			return err
		}
		if agent.WorkspaceID != workspaceID || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
			return errors.New("agent is unavailable")
		}
		return nil
	case "squad":
		squad, err := q.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
			ID: assignment.ActorID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return err
		}
		if squad.ArchivedAt.Valid {
			return errors.New("squad is archived")
		}
		return nil
	default:
		return fmt.Errorf("unsupported executor actor type %q", assignment.ActorType)
	}
}

func workflowExecutorCandidates(assignments ...validatedWorkflowRoleAssignment) []byte {
	rows := make([]map[string]any, 0, len(assignments))
	for _, assignment := range assignments {
		rows = append(rows, map[string]any{
			"actor_type": assignment.ActorType,
			"actor_id":   uuidToString(assignment.ActorID),
		})
	}
	encoded, _ := json.Marshal(rows)
	return encoded
}

func workflowExecutorSnapshot(strategy, role, capability, node, field string) []byte {
	encoded, _ := json.Marshal(map[string]any{
		"strategy": strategy, "role": role, "capability": capability,
		"node": node, "field": field,
	})
	return encoded
}

func workflowDirectActorSnapshot(strategy, actorType, actorID string) []byte {
	encoded, _ := json.Marshal(map[string]any{
		"strategy":   strategy,
		"actor_type": actorType,
		"actor_id":   actorID,
	})
	return encoded
}

func createWorkflowExecutorResolution(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	task db.WorkflowNodeTask,
	decision workflowExecutorDecision,
) (db.WorkflowExecutorResolution, error) {
	status := "needs_setup"
	var actorType pgtype.Text
	var actorID pgtype.UUID
	var resolvedAt pgtype.Timestamptz
	if decision.Assignment != nil {
		status = "resolved"
		actorType = pgtype.Text{String: decision.Assignment.ActorType, Valid: true}
		actorID = decision.Assignment.ActorID
		resolvedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	resolution, err := q.CreateWorkflowExecutorResolution(ctx, db.CreateWorkflowExecutorResolutionParams{
		WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID,
		WorkflowNodeInstanceID: node.ID, WorkflowNodeTaskID: task.ID,
		Strategy: decision.Strategy, Status: status,
		ActorType: actorType, ActorID: actorID,
		Candidates: decision.Candidates, Reason: decision.Reason,
		DefinitionSnapshot: decision.Snapshot, ResolvedAt: resolvedAt,
	})
	if err != nil {
		return db.WorkflowExecutorResolution{}, err
	}
	if status == "resolved" {
		if _, err := q.SetWorkflowNodeTaskExecutorResolution(ctx, db.SetWorkflowNodeTaskExecutorResolutionParams{
			ExecutorResolutionID: resolution.ID, ID: task.ID, WorkspaceID: workspaceID,
		}); err != nil {
			return db.WorkflowExecutorResolution{}, err
		}
	}
	return resolution, nil
}

func workflowNodeNeedsExecutorSetup(
	ctx context.Context,
	q *db.Queries,
	workspaceID, nodeID pgtype.UUID,
) (bool, error) {
	tasks, err := q.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: nodeID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, err
	}
	for _, task := range tasks {
		if task.MaterializationStatus != "cancelled" && !task.ExecutorResolutionID.Valid {
			return true, nil
		}
	}
	return false, nil
}

func workflowInstanceNeedsExecutorSetup(
	ctx context.Context,
	q *db.Queries,
	instance db.WorkflowInstance,
) (bool, error) {
	nodes, err := q.ListWorkflowNodeInstances(ctx, db.ListWorkflowNodeInstancesParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		return false, err
	}
	openNodes := make(map[pgtype.UUID]struct{})
	for _, node := range nodes {
		if workflowNodeIsOpen(node) {
			openNodes[node.ID] = struct{}{}
		}
	}
	tasks, err := q.ListWorkflowInstanceTasks(ctx, db.ListWorkflowInstanceTasksParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		return false, err
	}
	for _, task := range tasks {
		if _, open := openNodes[task.WorkflowNodeInstanceID]; open &&
			task.MaterializationStatus != "cancelled" &&
			!task.ExecutorResolutionID.Valid {
			return true, nil
		}
	}
	return false, nil
}

func workflowInstanceHasRequiredRoles(
	definition workflowdomain.Definition,
	rows []db.WorkflowInstanceRoleAssignment,
) bool {
	assigned := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		assigned[row.RoleKey] = struct{}{}
	}
	for _, role := range definition.Roles {
		if role.Required {
			if _, ok := assigned[role.Key]; !ok {
				return false
			}
		}
	}
	return true
}

func workflowNodeBlockedForExecutor(node db.WorkflowNodeInstance) bool {
	if node.Status != "blocked" {
		return false
	}
	var reasons []workflowdomain.WaitingReason
	if json.Unmarshal(node.WaitingReasons, &reasons) != nil {
		return false
	}
	for _, reason := range reasons {
		if reason.Code == "executor_needs_setup" {
			return true
		}
	}
	return false
}
