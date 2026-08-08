package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func createWorkflowVerdictRework(
	ctx context.Context,
	q *db.Queries,
	locked db.WorkflowInstance,
	currentNode db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	reason string,
	actorType string,
	actorID pgtype.UUID,
	idempotencyKey string,
) (db.WorkflowInstance, db.WorkflowNodeInstance, error) {
	version, err := q.GetWorkflowVersionInWorkspace(ctx, db.GetWorkflowVersionInWorkspaceParams{
		ID: locked.WorkflowVersionID, WorkspaceID: locked.WorkspaceID,
	})
	if err != nil {
		return locked, db.WorkflowNodeInstance{}, err
	}
	definition, err := workflowdomain.ParseDefinition(version.Definition)
	if err != nil {
		return locked, db.WorkflowNodeInstance{}, err
	}
	plan, err := workflowdomain.BuildGraphPlan(definition)
	if err != nil {
		return locked, db.WorkflowNodeInstance{}, err
	}
	nodes, err := q.ListWorkflowNodeInstances(ctx, db.ListWorkflowNodeInstancesParams{
		WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
	})
	if err != nil {
		return locked, db.WorkflowNodeInstance{}, err
	}
	// A Critic rejecting and a worker resubmitting is the loop most likely to
	// run away, because neither side ever tires. At the cap the node stops
	// being handed back and waits for a person instead — halting is the point,
	// so this is not an error the Critic callback should retry.
	if capErr := workflowdomain.ValidateReworkAttempt(
		nodeDefinition, int(currentNode.Attempt),
	); capErr != nil {
		reasons := workflowdomain.EncodeWaitingReasons([]workflowdomain.WaitingReason{{
			Code:    "rework_attempts_exhausted",
			Message: capErr.Error(),
		}})
		held, holdErr := q.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
			Status: "blocked", WaitingReasons: reasons, MarkReconciled: true,
			ID: currentNode.ID, WorkspaceID: locked.WorkspaceID,
			ExpectedStatus: currentNode.Status,
		})
		if holdErr != nil {
			return locked, db.WorkflowNodeInstance{}, holdErr
		}
		// The node stopped because it ran out of attempts, and its carriers say
		// so — an issue still reading in progress would describe someone
		// working on something the run has given up sending back.
		if syncErr := syncWorkflowNodeIssueStatusTx(
			ctx, q, locked.WorkspaceID, held, "blocked",
		); syncErr != nil {
			return locked, db.WorkflowNodeInstance{}, syncErr
		}
		return locked, held, nil
	}
	affected := plan.Descendants(currentNode.NodeKey)
	affected[currentNode.NodeKey] = struct{}{}
	for _, candidate := range nodes {
		if _, exists := affected[candidate.NodeKey]; !exists {
			continue
		}
		switch candidate.Status {
		case "active", "in_review", "waiting", "blocked", "completed", "skipped":
			// The carrier is deliberately not touched here: the next attempt
			// takes over this same issue and reopens it.
			if _, err := q.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
				Status: "superseded", WaitingReasons: []byte("[]"),
				ID: candidate.ID, WorkspaceID: locked.WorkspaceID,
				ExpectedStatus: candidate.Status,
			}); err != nil {
				return locked, db.WorkflowNodeInstance{}, err
			}
		}
	}
	if err := q.DeletePendingWorkflowAcceptance(ctx, db.DeletePendingWorkflowAcceptanceParams{
		WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
	}); err != nil {
		return locked, db.WorkflowNodeInstance{}, err
	}
	snapshot, _ := json.Marshal(nodeDefinition)
	reworkNode, err := q.CreateWorkflowNodeInstance(ctx, db.CreateWorkflowNodeInstanceParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		NodeKey: currentNode.NodeKey, NodeKind: currentNode.NodeKind,
		Attempt: currentNode.Attempt + 1, NameSnapshot: currentNode.NameSnapshot,
		DisplayOrder: currentNode.DisplayOrder, DefinitionSnapshot: snapshot,
		Status: "active",
	})
	if err != nil {
		return locked, db.WorkflowNodeInstance{}, err
	}
	roleRows, err := q.ListWorkflowRoleAssignments(ctx, db.ListWorkflowRoleAssignmentsParams{
		WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID,
	})
	if err != nil {
		return locked, db.WorkflowNodeInstance{}, err
	}
	needsSetup, err := createWorkflowNodeActivationRecords(
		ctx, q, locked.WorkspaceID, locked, reworkNode, nodeDefinition,
		definition, workflowRoleAssignmentsMap(roleRows),
	)
	if err != nil {
		return locked, db.WorkflowNodeInstance{}, err
	}
	status := "running"
	if needsSetup {
		status = "needs_setup"
		reasons := workflowdomain.EncodeWaitingReasons([]workflowdomain.WaitingReason{{
			Code: "executor_needs_setup", Message: "One or more workflow tasks require an executor",
		}})
		reworkNode, err = q.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
			Status: "blocked", WaitingReasons: reasons, MarkReconciled: true,
			ID: reworkNode.ID, WorkspaceID: locked.WorkspaceID,
			ExpectedStatus: "active",
		})
		if err != nil {
			return locked, db.WorkflowNodeInstance{}, err
		}
	}
	updated, err := q.UpdateWorkflowInstanceState(ctx, db.UpdateWorkflowInstanceStateParams{
		Status: status, MarkReconciled: true, ID: locked.ID,
		WorkspaceID: locked.WorkspaceID, ExpectedRevision: locked.Revision,
	})
	if err != nil {
		return locked, db.WorkflowNodeInstance{}, err
	}
	// Who rejected is part of the judgement, not decoration: the executor is
	// told what sent it back so it knows whether a reviewer or a person is
	// waiting. This helper serves both the agent Critic and a member recording
	// a fail verdict, so the action follows the actor rather than the helper.
	reworkAction := "critic_rework"
	if actorType == "member" {
		reworkAction = "manual_rework"
	}
	payload, _ := json.Marshal(map[string]any{
		"action": reworkAction, "node_key": currentNode.NodeKey,
		"node_instance_id":           uuidToString(currentNode.ID),
		"activated_node_instance_id": uuidToString(reworkNode.ID),
		"reason":                     reason,
	})
	if _, err := q.CreateWorkflowEvent(ctx, db.CreateWorkflowEventParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		WorkflowNodeInstanceID: currentNode.ID, EventType: "node.rollback",
		ActorType: actorType, ActorID: actorID,
		IdempotencyKey: idempotencyKey, Payload: payload,
	}); err != nil {
		return locked, db.WorkflowNodeInstance{}, err
	}
	return updated, reworkNode, nil
}

func (h *Handler) activateWorkflowVerdictRework(
	ctx context.Context,
	previous db.WorkflowInstance,
	updated db.WorkflowInstance,
	reworkNode db.WorkflowNodeInstance,
) {
	h.recordWorkflowInstanceStatusTransition(previous.Status, updated.Status)
	h.recordWorkflowNodesActivated(ctx, []db.WorkflowNodeInstance{reworkNode})
	version, err := h.Queries.GetWorkflowVersionInWorkspace(
		ctx,
		db.GetWorkflowVersionInWorkspaceParams{
			ID: updated.WorkflowVersionID, WorkspaceID: updated.WorkspaceID,
		},
	)
	if err == nil {
		if definition, parseErr := workflowdomain.ParseDefinition(version.Definition); parseErr == nil {
			h.applyWorkflowNodeEnterActions(
				ctx, updated, definition, []db.WorkflowNodeInstance{reworkNode},
			)
		}
	}
	h.materializeWorkflowNodeTasks(ctx, updated.WorkspaceID, updated, reworkNode)
}

func (h *Handler) ensureWorkflowAgentCriticTask(
	ctx context.Context,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
) error {
	reviewerType, reviewerID, resolved, err := workflowReviewerAssignment(
		ctx, h.Queries, node, nodeDefinition,
	)
	if err != nil || !resolved {
		return err
	}
	agentID := reviewerID
	var squadID pgtype.UUID
	switch reviewerType {
	case "member":
		return nil
	case "agent":
	case "squad":
		squad, squadErr := h.Queries.GetSquadInWorkspace(
			ctx,
			db.GetSquadInWorkspaceParams{ID: reviewerID, WorkspaceID: instance.WorkspaceID},
		)
		if squadErr != nil {
			return fmt.Errorf("load critic squad: %w", squadErr)
		}
		agentID = squad.LeaderID
		squadID = squad.ID
	default:
		return fmt.Errorf("unsupported workflow reviewer type %q", reviewerType)
	}

	tasks, err := h.Queries.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		return err
	}
	var carrier db.WorkflowNodeTask
	for _, task := range tasks {
		if task.Source == "critic" {
			carrier = task
			break
		}
	}
	if !carrier.ID.Valid {
		snapshot, _ := json.Marshal(nodeDefinition.Reviewer)
		carrier, err = h.Queries.CreateWorkflowNodeTask(ctx, db.CreateWorkflowNodeTaskParams{
			WorkspaceID: instance.WorkspaceID, WorkflowInstanceID: instance.ID,
			WorkflowNodeInstanceID: node.ID, TaskKey: "critic", Source: "critic",
			Required: false, DefinitionSnapshot: snapshot,
			MaterializationStatus: "materialized", CreatedByType: "system",
		})
		if err != nil {
			return fmt.Errorf("create critic task carrier: %w", err)
		}
	}
	latest, err := h.Queries.GetLatestAgentTaskForWorkflowNodeTask(ctx, carrier.ID)
	if err == nil {
		context, ok := service.ParseWorkflowNodeTaskContext(latest)
		if ok && context.Phase == service.WorkflowNodeTaskPhaseCritic &&
			latest.AgentID == agentID &&
			latest.Status != "failed" && latest.Status != "cancelled" {
			return nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = h.TaskService.EnqueueWorkflowNodeCriticTask(
		ctx, instance.WorkspaceID, instance.StartedByID, carrier.ID,
		instance.ID, node.ID, agentID, squadID, instance.Title,
	)
	if err != nil {
		return fmt.Errorf("enqueue workflow critic: %w", err)
	}
	return nil
}

// retryWorkflowAgentCriticVerdict re-asks the same reviewer for a readable
// verdict. It reports whether a retry was enqueued; false means the caller
// should record the blocked verdict as before.
//
// The retry is refused when the node has moved on — a stale Critic finishing
// after its node was cancelled, superseded, or already judged must not queue
// work against it.
func (h *Handler) retryWorkflowAgentCriticVerdict(
	ctx context.Context,
	task db.AgentTaskQueue,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	problem string,
	wrote string,
) (bool, error) {
	current, err := h.Queries.GetWorkflowNodeInstanceInWorkspace(
		ctx,
		db.GetWorkflowNodeInstanceInWorkspaceParams{
			ID: node.ID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		return false, err
	}
	if instance.Status != "running" || !workflowNodeIsOpen(current) {
		return false, nil
	}
	reviewerType, reviewerID, resolved, err := workflowReviewerAssignment(
		ctx, h.Queries, current, nodeDefinition,
	)
	if err != nil || !resolved {
		return false, err
	}
	agentID := reviewerID
	var squadID pgtype.UUID
	switch reviewerType {
	case "agent":
	case "squad":
		squad, squadErr := h.Queries.GetSquadInWorkspace(
			ctx,
			db.GetSquadInWorkspaceParams{ID: reviewerID, WorkspaceID: instance.WorkspaceID},
		)
		if squadErr != nil {
			return false, fmt.Errorf("load critic squad: %w", squadErr)
		}
		agentID = squad.LeaderID
		squadID = squad.ID
	default:
		// A human reviewer has no protocol to violate.
		return false, nil
	}
	if _, err := h.TaskService.EnqueueWorkflowNodeCriticRetryTask(
		ctx, instance.WorkspaceID, instance.StartedByID, task.WorkflowNodeTaskID,
		instance.ID, current.ID, agentID, squadID, instance.Title,
		service.WorkflowVerdictRetry{Problem: problem, Wrote: wrote},
	); err != nil {
		return false, fmt.Errorf("enqueue critic verdict retry: %w", err)
	}
	slog.Info(
		"workflow critic verdict unreadable, retrying once",
		"workflow_instance_id", uuidToString(instance.ID),
		"node_key", current.NodeKey,
		"problem", problem,
	)
	return true, nil
}

func (h *Handler) recordWorkflowAgentCriticVerdict(
	ctx context.Context,
	task db.AgentTaskQueue,
	output string,
	reviewDecision string,
	reviewReason string,
) error {
	direct, ok := service.ParseWorkflowNodeTaskContext(task)
	if !ok || direct.Phase != service.WorkflowNodeTaskPhaseCritic {
		return nil
	}
	workspaceID, err := util.ParseUUID(direct.WorkspaceID)
	if err != nil {
		return err
	}
	nodeID, err := util.ParseUUID(direct.NodeInstanceID)
	if err != nil {
		return err
	}
	node, err := h.Queries.GetWorkflowNodeInstanceInWorkspace(
		ctx,
		db.GetWorkflowNodeInstanceInWorkspaceParams{ID: nodeID, WorkspaceID: workspaceID},
	)
	if err != nil {
		return err
	}
	instance, err := h.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{ID: node.WorkflowInstanceID, WorkspaceID: workspaceID},
	)
	if err != nil {
		return err
	}
	var nodeDefinition workflowdomain.NodeDefinition
	if err := json.Unmarshal(node.DefinitionSnapshot, &nodeDefinition); err != nil {
		return err
	}
	reviewerType, reviewerID, resolved, err := workflowReviewerAssignment(
		ctx, h.Queries, node, nodeDefinition,
	)
	if err != nil || !resolved {
		return err
	}
	allowed, err := workflowActorMatchesReviewer(
		ctx, h.Queries, reviewerType, reviewerID, "agent", task.AgentID,
	)
	if err != nil || !allowed {
		// A role may be reassigned while an old Critic is running. Its stale
		// result must not hold the daemon in an infinite completion retry.
		return err
	}

	// A verdict declared through `multica workflow review` is the verdict. It
	// was checked against the allowed set where the reviewer stated it, so
	// there is nothing here to infer and nothing to get wrong.
	//
	// Reading it out of the agent's prose is the fallback, and it is kept only
	// for a reviewer that finished without running the command. That inference
	// is why this function exists in the shape it does: the same reviewer wrote
	// `{"verdict":"approve"}` when it meant to reject, and a lenient reader
	// would have approved a fix that deleted the button it was asked to wire.
	if reviewDecision != "" {
		return h.applyWorkflowCriticVerdict(
			ctx, task, instance, node, nodeDefinition,
			reviewDecision, strings.TrimSpace(reviewReason), output,
		)
	}

	critic, parseErr := workflowdomain.ParseCriticOutput(output)
	result := critic.Result
	reason := critic.Reason
	if parseErr != nil {
		// A verdict can be sound and still be shaped wrong. WTE-14841's Critic
		// rejected a fix that had deleted the button it was meant to wire up,
		// listed four findings, and wrote them under `verdict`/`reason` instead
		// of `approved`/`comment` — a correct review, discarded on a field name,
		// and a human called to redo it. Ask once, showing the objection, before
		// spending a person.
		if direct.VerdictRetry == nil {
			retried, retryErr := h.retryWorkflowAgentCriticVerdict(
				ctx, task, instance, node, nodeDefinition, parseErr.Error(), output,
			)
			if retryErr != nil {
				return retryErr
			}
			if retried {
				return nil
			}
		}
		result = "blocked"
		// The reviewer's own words go into the reason. Discarding them left a
		// blocked node explained only by a byte offset, and made the failure
		// undiagnosable after the fact — the run that hit this had nothing
		// left to inspect.
		reason = workflowdomain.DescribeCriticParseFailure(parseErr, output)
	}
	return h.applyWorkflowCriticVerdict(
		ctx, task, instance, node, nodeDefinition, result, reason, output,
	)
}

// applyWorkflowCriticVerdict records one verdict and everything that follows
// from it — artifact review status, rework, instance progression — inside a
// single transaction. Both ways of arriving at a verdict end here, so a
// declared decision and an inferred one cannot diverge in what they do.
func (h *Handler) applyWorkflowCriticVerdict(
	ctx context.Context,
	task db.AgentTaskQueue,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	result string,
	reason string,
	output string,
) error {
	workspaceID := node.WorkspaceID
	if result == "pass" && reason == "" {
		reason = "Approved by workflow Critic"
	}


	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.LockWorkflowInstance(ctx, db.LockWorkflowInstanceParams{
		ID: instance.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return err
	}
	idempotencyKey := "critic-task:" + uuidToString(task.ID)
	if _, eventErr := qtx.GetWorkflowEventByIdempotencyKey(
		ctx,
		db.GetWorkflowEventByIdempotencyKeyParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: workspaceID,
			IdempotencyKey: idempotencyKey,
		},
	); eventErr == nil {
		return nil
	}
	currentNode, err := qtx.GetWorkflowNodeInstanceInWorkspace(
		ctx,
		db.GetWorkflowNodeInstanceInWorkspaceParams{ID: node.ID, WorkspaceID: workspaceID},
	)
	if err != nil || locked.Status != "running" || !workflowNodeIsOpen(currentNode) {
		return nil
	}
	currentReviewerType, currentReviewerID, currentResolved, err := workflowReviewerAssignment(
		ctx, qtx, currentNode, nodeDefinition,
	)
	if err != nil {
		return err
	}
	currentAllowed, err := workflowActorMatchesReviewer(
		ctx, qtx, currentReviewerType, currentReviewerID, "agent", task.AgentID,
	)
	if err != nil {
		return err
	}
	if !currentResolved || !currentAllowed {
		return nil
	}
	submissions, err := qtx.ListWorkflowSubmissions(ctx, db.ListWorkflowSubmissionsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return err
	}
	var submission db.WorkflowNodeSubmission
	for _, candidate := range submissions {
		if candidate.Status == "valid" {
			submission = candidate
			break
		}
	}
	if !submission.ID.Valid {
		return errors.New("workflow critic requires a valid submission")
	}
	artifactIDs, err := reviewWorkflowArtifactsForVerdict(
		ctx, qtx, workspaceID, currentNode, nodeDefinition,
		result, reason, task.AgentID,
	)
	if err != nil {
		return fmt.Errorf("review workflow artifacts: %w", err)
	}
	revision, err := qtx.GetNextWorkflowVerdictRevision(
		ctx,
		db.GetNextWorkflowVerdictRevisionParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
		},
	)
	if err != nil {
		return err
	}
	basis, _ := json.Marshal(map[string]any{
		"kind": "critic_protocol_v1", "submission_id": uuidToString(submission.ID),
		"agent_task_id": uuidToString(task.ID), "artifact_ids": artifactIDs,
	})
	definitionSnapshot, _ := json.Marshal(nodeDefinition.Reviewer)
	verdict, err := qtx.CreateWorkflowVerdict(ctx, db.CreateWorkflowVerdictParams{
		WorkspaceID: workspaceID, WorkflowInstanceID: locked.ID,
		WorkflowNodeInstanceID: node.ID, Revision: revision,
		Result: result, Reason: reason, Evidence: []byte("[]"), Basis: basis,
		EvaluatorType: "agent", EvaluatorID: task.AgentID,
		DefinitionSnapshot: definitionSnapshot,
	})
	if err != nil {
		return err
	}
	if err := qtx.SetWorkflowNodeLatestVerdict(ctx, db.SetWorkflowNodeLatestVerdictParams{
		LatestVerdictID: verdict.ID, ID: node.ID, WorkspaceID: workspaceID,
	}); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"action": "agent_critic_verdict", "verdict_id": uuidToString(verdict.ID),
		"revision": verdict.Revision, "result": verdict.Result,
	})
	if _, err := qtx.CreateWorkflowEvent(ctx, db.CreateWorkflowEventParams{
		WorkspaceID: workspaceID, WorkflowInstanceID: locked.ID,
		WorkflowNodeInstanceID: node.ID, EventType: "node.verdict_recorded",
		ActorType: "agent", ActorID: task.AgentID,
		IdempotencyKey: idempotencyKey, Payload: payload,
	}); err != nil {
		return err
	}
	updatedInstance := locked
	var reworkNode db.WorkflowNodeInstance
	if result == "fail" {
		updatedInstance, reworkNode, err = createWorkflowVerdictRework(
			ctx, qtx, locked, currentNode, nodeDefinition, reason, "agent", task.AgentID,
			"critic-rework:"+uuidToString(task.ID),
		)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	reviewStatus := ""
	if result == "pass" {
		reviewStatus = "approved"
	} else if result == "fail" {
		reviewStatus = "rejected"
	}
	h.publishWorkflowArtifactsReviewed(
		workspaceID, locked.ID, node.ID, "agent", uuidToString(task.AgentID),
		artifactIDs, reviewStatus,
	)
	h.Metrics.RecordWorkflowVerdict("agent", verdict.Result)
	if reworkNode.ID.Valid {
		h.activateWorkflowVerdictRework(ctx, locked, updatedInstance, reworkNode)
	} else {
		_, _ = h.reconcileWorkflowInstance(
			ctx, workspaceID, locked.ID, "agent", task.AgentID,
			"verdict:"+uuidToString(verdict.ID),
		)
	}
	h.publishWorkflowRealtime(
		protocol.EventWorkflowVerdictCreated,
		uuidToString(workspaceID), "agent", uuidToString(task.AgentID),
		map[string]any{
			"workflow_instance_id":      uuidToString(locked.ID),
			"workflow_node_instance_id": uuidToString(node.ID),
			"workflow_verdict_id":       uuidToString(verdict.ID),
		},
	)
	h.publishWorkflowNodeUpdated(
		uuidToString(workspaceID), "agent", uuidToString(task.AgentID),
		uuidToString(locked.ID), uuidToString(node.ID),
	)
	if reworkNode.ID.Valid {
		h.publishWorkflowInstanceUpdated(
			uuidToString(workspaceID), "agent", uuidToString(task.AgentID),
			uuidToString(locked.ID), uuidToString(reworkNode.ID),
		)
	}
	return nil
}
