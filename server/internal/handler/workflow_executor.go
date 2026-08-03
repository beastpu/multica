package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

// resolveWorkflowNodeExecutor answers "who does this node" from the node's one
// executor field, falling back once if it names nobody available. The issue
// template no longer carries an assignee, so there is a single place to look
// and the answer no longer depends on which of three fields the author filled
// in last.
func resolveWorkflowNodeExecutor(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	node workflowdomain.NodeDefinition,
	roles map[string]validatedWorkflowRoleAssignment,
) (workflowExecutorDecision, error) {
	if !workflowdomain.HasExecutor(node.Executor) {
		return workflowExecutorManualDecision(
			"The node names no executor; manual selection is required",
		), nil
	}
	decision, resolved, err := resolveWorkflowExecutorEntry(
		ctx, q, workspaceID, *node.Executor, roles, false,
	)
	if err != nil {
		return workflowExecutorDecision{}, err
	}
	if resolved {
		return decision, nil
	}
	fallback := node.Executor.Fallback
	if fallback == nil {
		return decision, nil
	}
	fallbackDecision, resolved, err := resolveWorkflowExecutorEntry(
		ctx, q, workspaceID, *fallback, roles, true,
	)
	if err != nil {
		return workflowExecutorDecision{}, err
	}
	if resolved {
		return fallbackDecision, nil
	}
	return fallbackDecision, nil
}

// resolveWorkflowExecutorEntry resolves one executor entry. The bool reports
// whether it produced an actor, which is what tells the caller to stop rather
// than reach for the fallback.
func resolveWorkflowExecutorEntry(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	executor workflowdomain.ExecutorDefinition,
	roles map[string]validatedWorkflowRoleAssignment,
	isFallback bool,
) (workflowExecutorDecision, bool, error) {
	origin := "node executor"
	if isFallback {
		origin = "node executor fallback"
	}
	switch executor.Kind {
	case "actor":
		assignment, err := directWorkflowExecutorAssignment(executor.ActorType, executor.ActorID)
		if err != nil {
			return workflowExecutorDecision{}, false, err
		}
		snapshot := workflowDirectActorSnapshot(
			executor.Kind, executor.ActorType, executor.ActorID,
		)
		if err := validateWorkflowExecutorActor(ctx, q, workspaceID, assignment); err != nil {
			return workflowExecutorDecision{
				Strategy:   "manual",
				Candidates: workflowExecutorCandidates(assignment),
				Reason:     "The " + origin + " actor is unavailable",
				Snapshot:   snapshot,
			}, false, nil
		}
		return workflowExecutorDecision{
			Assignment: &assignment,
			Strategy:   "fixed_actor",
			Candidates: workflowExecutorCandidates(assignment),
			Reason:     "Resolved from the " + origin,
			Snapshot:   snapshot,
		}, true, nil
	case "role":
		strategy := "fixed_role"
		if isFallback {
			strategy = "fallback_role"
		}
		assignment, ok := roles[executor.Role]
		if !ok {
			return workflowExecutorDecision{
				Strategy: "manual", Candidates: []byte("[]"),
				Reason:   "Workflow role " + executor.Role + " is unassigned",
				Snapshot: workflowExecutorSnapshot(strategy, executor.Role, "", "", ""),
			}, false, nil
		}
		return workflowExecutorDecision{
			Assignment: &assignment, Strategy: strategy,
			Candidates: workflowExecutorCandidates(assignment),
			Reason:     "Resolved from workflow role " + executor.Role,
			Snapshot:   workflowExecutorSnapshot(strategy, executor.Role, "", "", ""),
		}, true, nil
	case "capability":
		assignment, candidates, reason, err := resolveCapabilityExecutor(
			ctx, q, workspaceID, roles[executor.Role], executor.Capability,
		)
		if err != nil {
			return workflowExecutorDecision{}, false, err
		}
		snapshot := workflowExecutorSnapshot(
			"capability_match", executor.Role, executor.Capability, "", "",
		)
		if assignment == nil {
			return workflowExecutorDecision{
				Strategy: "manual", Candidates: candidates,
				Reason: reason, Snapshot: snapshot,
			}, false, nil
		}
		return workflowExecutorDecision{
			Assignment: assignment, Strategy: "capability_match",
			Candidates: candidates, Reason: reason, Snapshot: snapshot,
		}, true, nil
	default:
		return workflowExecutorManualDecision("Manual executor selection is required"), false, nil
	}
}

func workflowExecutorManualDecision(reason string) workflowExecutorDecision {
	return workflowExecutorDecision{
		Strategy: "manual", Candidates: []byte("[]"), Reason: reason,
		Snapshot: workflowExecutorSnapshot("manual", "", "", "", ""),
	}
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
		if task.Source == "critic" {
			continue
		}
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
		if task.Source == "critic" {
			continue
		}
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
