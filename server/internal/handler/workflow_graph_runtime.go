package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type workflowGraphPropagation struct {
	Nodes       map[string]db.WorkflowNodeInstance
	Activated   []db.WorkflowNodeInstance
	Changed     bool
	NeedsSetup  bool
	CanComplete bool
}

func latestWorkflowNodesByKey(nodes []db.WorkflowNodeInstance) map[string]db.WorkflowNodeInstance {
	latest := make(map[string]db.WorkflowNodeInstance, len(nodes))
	for _, node := range nodes {
		current, exists := latest[node.NodeKey]
		if !exists || node.Attempt > current.Attempt {
			latest[node.NodeKey] = node
		}
	}
	return latest
}

func workflowRoleAssignmentsMap(rows []db.WorkflowInstanceRoleAssignment) map[string]validatedWorkflowRoleAssignment {
	result := make(map[string]validatedWorkflowRoleAssignment, len(rows))
	for _, role := range rows {
		result[role.RoleKey] = validatedWorkflowRoleAssignment{
			RoleKey: role.RoleKey, ActorType: role.ActorType,
			ActorID: role.ActorID, Source: role.Source,
		}
	}
	return result
}

func (h *Handler) propagateWorkflowGraph(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	definition workflowdomain.Definition,
	plan workflowdomain.GraphPlan,
	roles map[string]validatedWorkflowRoleAssignment,
	nodes []db.WorkflowNodeInstance,
	actorType string,
	actorID pgtype.UUID,
) (workflowGraphPropagation, error) {
	result := workflowGraphPropagation{Nodes: latestWorkflowNodesByKey(nodes)}
	maxSteps := len(plan.Ordered)*4 + 8
	for step := 0; step < maxSteps; step++ {
		progressed := false
		for _, nodeDefinition := range plan.Ordered {
			if nodeDefinition.Kind == "start" {
				continue
			}
			node, exists := result.Nodes[nodeDefinition.Key]
			if !exists {
				return result, fmt.Errorf("workflow node %q is missing", nodeDefinition.Key)
			}

			selected, settled, maxSelectedAttempt, err := h.workflowNodeInputState(
				ctx, q, workspaceID, instance.ID, plan, result.Nodes, nodeDefinition,
			)
			if err != nil {
				return result, err
			}
			if !settled {
				continue
			}
			if (node.Status == "superseded" || node.Status == "skipped") &&
				selected && maxSelectedAttempt > node.Attempt {
				snapshot, _ := json.Marshal(nodeDefinition)
				node, err = q.CreateWorkflowNodeInstance(ctx, db.CreateWorkflowNodeInstanceParams{
					WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID,
					NodeKey: nodeDefinition.Key, NodeKind: nodeDefinition.Kind,
					Attempt: node.Attempt + 1, NameSnapshot: nodeDefinition.Name,
					DisplayOrder:       int32(plan.Index[nodeDefinition.Key]),
					DefinitionSnapshot: snapshot, Status: "pending",
				})
				if err != nil {
					return result, fmt.Errorf("create workflow node attempt %q: %w", nodeDefinition.Key, err)
				}
				result.Nodes[nodeDefinition.Key] = node
				result.Changed = true
				progressed = true
			}
			if node.Status != "pending" && node.Status != "ready" {
				continue
			}
			if !selected {
				updated, err := q.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
					Status: "skipped", WaitingReasons: []byte("[]"), MarkReconciled: true,
					ID: node.ID, WorkspaceID: workspaceID, ExpectedStatus: node.Status,
				})
				if err != nil {
					return result, fmt.Errorf("skip workflow node %q: %w", node.NodeKey, err)
				}
				if err := syncWorkflowNodeIssueStatusTx(
					ctx, q, workspaceID, updated, "skipped",
				); err != nil {
					return result, fmt.Errorf("cancel skipped workflow node carriers %q: %w", node.NodeKey, err)
				}
				result.Nodes[node.NodeKey] = updated
				result.Changed = true
				progressed = true
				continue
			}

			switch nodeDefinition.Kind {
			case "activity":
				updated, err := q.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
					Status: "active", WaitingReasons: []byte("[]"),
					ID: node.ID, WorkspaceID: workspaceID, ExpectedStatus: node.Status,
				})
				if err != nil {
					return result, fmt.Errorf("activate workflow activity %q: %w", node.NodeKey, err)
				}
				needsSetup, err := createWorkflowNodeActivationRecords(
					ctx, q, workspaceID, instance, updated, nodeDefinition, definition, roles,
				)
				if err != nil {
					return result, fmt.Errorf("create workflow activity records %q: %w", node.NodeKey, err)
				}
				if needsSetup {
					reasons := workflowdomain.EncodeWaitingReasons([]workflowdomain.WaitingReason{{
						Code:    "executor_needs_setup",
						Message: "One or more workflow tasks require an executor",
					}})
					updated, err = q.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
						Status: "blocked", WaitingReasons: reasons, MarkReconciled: true,
						ID: updated.ID, WorkspaceID: workspaceID, ExpectedStatus: "active",
					})
					if err != nil {
						return result, fmt.Errorf("block workflow activity %q for executor setup: %w", node.NodeKey, err)
					}
					result.NeedsSetup = true
				}
				// An activity that resolved an executor is under way; one that
				// could not is blocked, and never entered Activated — which is
				// what drives the started mirror — so it says so here.
				carrierEvent := "activated"
				if needsSetup {
					carrierEvent = "blocked"
				}
				if err := syncWorkflowNodeIssueStatusTx(
					ctx, q, workspaceID, updated, carrierEvent,
				); err != nil {
					return result, fmt.Errorf("sync workflow activity carriers %q: %w", node.NodeKey, err)
				}
				result.Nodes[node.NodeKey] = updated
				if !needsSetup {
					result.Activated = append(result.Activated, updated)
				}
			case "wait":
				reasons := workflowdomain.EncodeWaitingReasons([]workflowdomain.WaitingReason{{
					Code: "wait_control", Message: "Waiting for an explicit resume or completion action",
				}})
				updated, err := q.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
					Status: "waiting", WaitingReasons: reasons,
					ID: node.ID, WorkspaceID: workspaceID, ExpectedStatus: node.Status,
				})
				if err != nil {
					return result, fmt.Errorf("activate workflow wait %q: %w", node.NodeKey, err)
				}
				result.Nodes[node.NodeKey] = updated
			case "gateway":
				pool, err := workflowExprPool(ctx, q, workspaceID, instance, result.Nodes)
				if err != nil {
					return result, err
				}
				routing, err := workflowdomain.SelectGatewayCases(
					nodeDefinition, plan, pool,
				)
				if err != nil {
					return result, err
				}
				caseIDs := make([]string, len(routing.Cases))
				for i, selected := range routing.Cases {
					caseIDs[i] = selected.ID
				}
				updated, err := q.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
					Status: "completed", WaitingReasons: []byte("[]"), MarkReconciled: true,
					ID: node.ID, WorkspaceID: workspaceID, ExpectedStatus: node.Status,
				})
				if err != nil {
					return result, fmt.Errorf("complete workflow gateway %q: %w", node.NodeKey, err)
				}
				// case_id names the first winner and stays for readers that
				// predate filter mode; case_ids carries the full set. matched
				// and evidence are what the branch is read back with: the
				// submissions they came from keep changing, so a decision that
				// does not carry its own inputs cannot be explained later.
				payload, _ := json.Marshal(map[string]any{
					"case_id":          caseIDs[0],
					"case_ids":         caseIDs,
					"selected_targets": routing.Targets,
					"matched":          routing.Matched,
					"evidence":         routing.Evidence,
				})
				if _, err := q.CreateWorkflowEvent(ctx, db.CreateWorkflowEventParams{
					WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID,
					WorkflowNodeInstanceID: node.ID, EventType: "node.routed",
					ActorType: actorType, ActorID: actorID,
					IdempotencyKey: fmt.Sprintf("route:%s:%d", uuidToString(node.ID), node.Attempt),
					Payload:        payload,
				}); err != nil {
					return result, fmt.Errorf("record workflow gateway route %q: %w", node.NodeKey, err)
				}
				result.Nodes[node.NodeKey] = updated
			case "parallel_split", "parallel_join", "end":
				updated, err := q.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
					Status: "completed", WaitingReasons: []byte("[]"), MarkReconciled: true,
					ID: node.ID, WorkspaceID: workspaceID, ExpectedStatus: node.Status,
				})
				if err != nil {
					return result, fmt.Errorf("complete workflow control node %q: %w", node.NodeKey, err)
				}
				eventType := "node.control_completed"
				if nodeDefinition.Kind == "end" {
					eventType = "node.end_reached"
				}
				payload, _ := json.Marshal(map[string]any{"node_key": node.NodeKey, "node_kind": node.NodeKind})
				if _, err := q.CreateWorkflowEvent(ctx, db.CreateWorkflowEventParams{
					WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID,
					WorkflowNodeInstanceID: node.ID, EventType: eventType,
					ActorType: actorType, ActorID: actorID,
					IdempotencyKey: fmt.Sprintf("control:%s:%d", uuidToString(node.ID), node.Attempt),
					Payload:        payload,
				}); err != nil {
					return result, fmt.Errorf("record workflow control completion %q: %w", node.NodeKey, err)
				}
				result.Nodes[node.NodeKey] = updated
			default:
				return result, fmt.Errorf("unsupported workflow node kind %q", nodeDefinition.Kind)
			}
			result.Changed = true
			progressed = true
		}
		if !progressed {
			break
		}
		if step == maxSteps-1 {
			return result, errors.New("workflow graph propagation exceeded safety limit")
		}
	}

	endReached := false
	open := false
	for key, node := range result.Nodes {
		definition, exists := plan.Node(key)
		if !exists {
			continue
		}
		if definition.Kind == "end" && node.Status == "completed" {
			endReached = true
		}
		switch node.Status {
		case "pending", "ready", "active", "in_review", "waiting", "blocked":
			open = true
		}
	}
	if !endReached || open {
		result.CanComplete = false
		return result, nil
	}
	// Acceptance is the last gate and it belongs to the run, not to a node:
	// the work is finished, and what is left is whether the requirement is.
	// This is where the acceptance activity used to sit on the canvas.
	approved, err := h.settleWorkflowAcceptance(ctx, q, workspaceID, instance, definition)
	if err != nil {
		return result, err
	}
	result.CanComplete = approved
	return result, nil
}

// settleWorkflowAcceptance opens a pending acceptance the first time a run
// reaches its end, and reports whether the run is cleared to complete. A
// workflow with no acceptance policy is cleared immediately.
func (h *Handler) settleWorkflowAcceptance(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	definition workflowdomain.Definition,
) (bool, error) {
	if definition.Acceptance.Policy != "member" {
		return true, nil
	}
	latest, err := q.GetLatestWorkflowAcceptance(ctx, db.GetLatestWorkflowAcceptanceParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: workspaceID,
	})
	switch {
	case err == nil && latest.Status == "approved":
		return true, nil
	case err == nil && latest.Status == "pending":
		return false, nil
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return false, fmt.Errorf("load latest acceptance: %w", err)
	}
	revision, err := q.GetNextWorkflowAcceptanceRevision(
		ctx,
		db.GetNextWorkflowAcceptanceRevisionParams{
			WorkflowInstanceID: instance.ID, WorkspaceID: workspaceID,
		},
	)
	if err != nil {
		return false, fmt.Errorf("next acceptance revision: %w", err)
	}
	if _, err := q.CreateWorkflowAcceptance(ctx, db.CreateWorkflowAcceptanceParams{
		WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID,
		Revision: revision, Status: "pending", Evidence: []byte("[]"),
		IdempotencyKey: fmt.Sprintf("end_reached:%s:%d", uuidToString(instance.ID), revision),
	}); err != nil {
		return false, fmt.Errorf("create pending acceptance: %w", err)
	}
	return false, nil
}

func (h *Handler) workflowNodeInputState(
	ctx context.Context,
	q *db.Queries,
	workspaceID, instanceID pgtype.UUID,
	plan workflowdomain.GraphPlan,
	nodes map[string]db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
) (selected, settled bool, maxSelectedAttempt int32, err error) {
	incoming := plan.Predecessors(nodeDefinition.Key)
	if len(incoming) == 0 {
		return false, false, 0, nil
	}
	selectedCount := 0
	settledCount := 0
	for _, edge := range incoming {
		predecessor, exists := nodes[edge.From]
		if !exists {
			return false, false, 0, fmt.Errorf("workflow predecessor %q is missing", edge.From)
		}
		edgeSelected := false
		switch predecessor.Status {
		case "completed":
			settledCount++
			predecessorDefinition, _ := plan.Node(predecessor.NodeKey)
			if predecessorDefinition.Kind == "gateway" {
				routing, routeErr := q.GetWorkflowNodeRoutingEvent(ctx, db.GetWorkflowNodeRoutingEventParams{
					WorkflowInstanceID: instanceID, WorkspaceID: workspaceID,
					WorkflowNodeInstanceID: predecessor.ID,
				})
				if routeErr != nil {
					return false, false, 0, fmt.Errorf("load gateway route %q: %w", predecessor.NodeKey, routeErr)
				}
				var payload struct {
					CaseID          string   `json:"case_id"`
					SelectedTargets []string `json:"selected_targets"`
				}
				if json.Unmarshal(routing.Payload, &payload) != nil || len(payload.SelectedTargets) == 0 {
					return false, false, 0, fmt.Errorf("gateway route %q is invalid", predecessor.NodeKey)
				}
				edgeSelected = slices.Contains(payload.SelectedTargets, edge.To)
			} else {
				edgeSelected = true
			}
		case "skipped":
			settledCount++
			if _, skipErr := q.GetWorkflowNodeExplicitSkipEvent(
				ctx,
				db.GetWorkflowNodeExplicitSkipEventParams{
					WorkflowInstanceID: instanceID, WorkspaceID: workspaceID,
					WorkflowNodeInstanceID: predecessor.ID,
				},
			); skipErr == nil {
				edgeSelected = true
			} else if !errors.Is(skipErr, pgx.ErrNoRows) {
				return false, false, 0, fmt.Errorf(
					"load explicit skip for %q: %w",
					predecessor.NodeKey,
					skipErr,
				)
			}
		case "superseded", "cancelled", "failed":
			settledCount++
		}
		if edgeSelected {
			selectedCount++
			if predecessor.Attempt > maxSelectedAttempt {
				maxSelectedAttempt = predecessor.Attempt
			}
		}
	}

	if nodeDefinition.Kind == "parallel_join" && nodeDefinition.JoinMode == "any" {
		if selectedCount > 0 {
			return true, true, maxSelectedAttempt, nil
		}
		if settledCount == len(incoming) {
			return false, true, 0, nil
		}
		return false, false, 0, nil
	}
	if settledCount != len(incoming) {
		return false, false, 0, nil
	}
	return selectedCount > 0, true, maxSelectedAttempt, nil
}

// workflowExprPool assembles the variable pool a gateway routes on: for every
// node with a latest valid submission, that submission's normalized outputs.
// Whole-submission replacement, no field-level merge — a field the latest
// round did not deliver is absent, and absent fails closed.
func workflowExprPool(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	nodes map[string]db.WorkflowNodeInstance,
) (workflowdomain.ExprPool, error) {
	pool := workflowdomain.ExprPool{}
	valuesFor := func(key string) map[string]any {
		if existing, ok := pool[key]; ok {
			return existing
		}
		created := map[string]any{}
		pool[key] = created
		return created
	}
	for key, node := range nodes {
		if node.LatestSubmissionID.Valid {
			submission, err := q.GetWorkflowSubmissionInWorkspace(ctx, db.GetWorkflowSubmissionInWorkspaceParams{
				ID: node.LatestSubmissionID, WorkspaceID: workspaceID,
			})
			if err == nil && submission.Status == "valid" {
				values := map[string]any{}
				if json.Unmarshal(submission.Payload, &values) == nil && len(values) > 0 {
					maps.Copy(valuesFor(key), values)
				}
			}
		}
		// The review's conclusion sits in the same namespace as the node's own
		// fields, so `review.verdict` reads like any other variable. The
		// definition refuses a declared field that would collide.
		if node.LatestVerdictID.Valid {
			verdict, err := q.GetWorkflowVerdictInWorkspace(ctx, db.GetWorkflowVerdictInWorkspaceParams{
				ID: node.LatestVerdictID, WorkspaceID: workspaceID,
			})
			if err == nil {
				values := valuesFor(key)
				values["verdict"] = verdict.Result
				values["reason"] = verdict.Reason
				if verdict.Confidence.Valid {
					values["confidence"] = verdict.Confidence.Float64
				}
			}
		}
	}
	// A run started without a host issue simply has no issue.* values, and
	// absent fails closed — the branch falls to else rather than guessing.
	if instance.HostIssueID.Valid {
		host, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
			ID: instance.HostIssueID, WorkspaceID: workspaceID,
		})
		if err == nil {
			values := map[string]any{
				"status": host.Status, "priority": host.Priority, "title": host.Title,
			}
			if host.AssigneeType.Valid {
				values["assignee_type"] = host.AssigneeType.String
			}
			if host.AssigneeID.Valid {
				values["assignee_id"] = uuidToString(host.AssigneeID)
			}
			if host.ProjectID.Valid {
				values["project_id"] = uuidToString(host.ProjectID)
			}
			properties := map[string]any{}
			if json.Unmarshal(host.Properties, &properties) == nil {
				for key, value := range properties {
					values[workflowdomain.HostIssuePropertyPrefix+key] = value
				}
			}
			pool[workflowdomain.HostIssueScope] = values
		}
	}
	return pool, nil
}

func workflowConditionResolver(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	nodes map[string]db.WorkflowNodeInstance,
) (workflowdomain.ConditionResolver, error) {
	host := db.Issue{Title: instance.Title}
	properties := map[string]any{}
	if instance.HostIssueID.Valid {
		loadedHost, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
			ID: instance.HostIssueID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return nil, fmt.Errorf("load workflow host for condition: %w", err)
		}
		host = loadedHost
		_ = json.Unmarshal(host.Properties, &properties)
	}

	submissions := map[string]map[string]any{}
	verdicts := map[string]map[string]any{}
	for key, node := range nodes {
		if node.LatestSubmissionID.Valid {
			submission, err := q.GetWorkflowSubmissionInWorkspace(ctx, db.GetWorkflowSubmissionInWorkspaceParams{
				ID: node.LatestSubmissionID, WorkspaceID: workspaceID,
			})
			if err == nil && submission.Status == "valid" {
				payload := map[string]any{}
				if json.Unmarshal(submission.Payload, &payload) == nil {
					submissions[key] = payload
				}
			}
		}
		if node.LatestVerdictID.Valid {
			verdict, err := q.GetWorkflowVerdictInWorkspace(ctx, db.GetWorkflowVerdictInWorkspaceParams{
				ID: node.LatestVerdictID, WorkspaceID: workspaceID,
			})
			if err == nil {
				value := map[string]any{
					"result": verdict.Result,
					"reason": verdict.Reason,
				}
				if verdict.Confidence.Valid {
					value["confidence"] = verdict.Confidence.Float64
				}
				verdicts[key] = value
			}
		}
	}

	hostValues := map[string]any{
		"title": host.Title, "status": host.Status, "priority": host.Priority,
	}
	if host.AssigneeType.Valid {
		hostValues["assignee_type"] = host.AssigneeType.String
	}
	if host.AssigneeID.Valid {
		hostValues["assignee_id"] = uuidToString(host.AssigneeID)
	}
	if host.ProjectID.Valid {
		hostValues["project_id"] = uuidToString(host.ProjectID)
	}
	return func(source, node, key string) (any, bool) {
		switch source {
		case "host_issue":
			value, ok := hostValues[key]
			return value, ok
		case "host_property":
			value, ok := properties[key]
			return value, ok
		case "node_submission":
			value, ok := submissions[node][key]
			return value, ok
		case "node_verdict":
			value, ok := verdicts[node][key]
			return value, ok
		default:
			return nil, false
		}
	}, nil
}

func workflowNodeIsOpen(node db.WorkflowNodeInstance) bool {
	switch node.Status {
	case "active", "in_review", "waiting", "blocked":
		return true
	default:
		return false
	}
}

func workflowActiveNodes(nodes map[string]db.WorkflowNodeInstance, plan workflowdomain.GraphPlan) []db.WorkflowNodeInstance {
	result := make([]db.WorkflowNodeInstance, 0)
	for _, definition := range plan.Ordered {
		node, exists := nodes[definition.Key]
		if exists && workflowNodeIsOpen(node) {
			result = append(result, node)
		}
	}
	return result
}

func workflowErrIsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
