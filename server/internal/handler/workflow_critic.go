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

	// A review is found through the node it judges. It used to be found through
	// a workflow_node_task row created solely to be pointed at — a table whose
	// rows mean "a unit of work with an executor", which a review is not. That
	// row's executor column stayed null forever, and every reader of the table
	// had to be taught the exception. Two of them were taught only after they
	// had already told users to assign an executor that resolves nothing.
	latest, err := h.Queries.GetLatestAgentTaskForWorkflowNodeReview(ctx, node.ID)
	if err == nil {
		if latest.AgentID == agentID &&
			latest.Status != "failed" && latest.Status != "cancelled" {
			return nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = h.TaskService.EnqueueWorkflowNodeCriticTask(
		ctx, instance.WorkspaceID, instance.StartedByID,
		instance.ID, node.ID, agentID, squadID, instance.Title,
		node.LatestSubmissionID,
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
		ctx, instance.WorkspaceID, instance.StartedByID,
		instance.ID, current.ID, agentID, squadID, instance.Title,
		service.WorkflowVerdictRetry{Problem: problem, Wrote: wrote},
		// The retry judges the same revision as the attempt it replaces. A
		// retry that silently moved to a newer one would be a different review.
		current.LatestSubmissionID,
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

	// A verdict is declared through `multica workflow review`, never inferred
	// from what the reviewer wrote afterwards.
	//
	// Inference is what this code did, and it failed the only way that matters:
	// plausibly. The same reviewer wrote `{"verdict":"approve"}` while
	// rejecting a fix that had deleted the button it was asked to wire up —
	// read leniently, that ships. Three reviews in a row used a vocabulary the
	// protocol had shown them verbatim, and one spent its entire run guessing
	// request bodies against an endpoint that refuses agents.
	if reviewDecision != "" {
		return h.applyWorkflowCriticVerdict(
			ctx, task, instance, node, nodeDefinition,
			reviewDecision, strings.TrimSpace(reviewReason), output,
		)
	}

	// No decision was declared, so the review did not conclude. That is not a
	// verdict of any kind — least of all `blocked`, which means "I looked and
	// cannot judge this". Recording one would put words in a reviewer's mouth.
	//
	// Ask once, saying plainly what is missing, then leave the node in review
	// for a person. A run that ends here has a reviewer that finished without
	// deciding, which is a thing worth a human's attention rather than a
	// synthesised judgement that reads like one.
	if direct.VerdictRetry == nil {
		retried, retryErr := h.retryWorkflowAgentCriticVerdict(
			ctx, task, instance, node, nodeDefinition,
			"the review ended without running `multica workflow review`", output,
		)
		if retryErr != nil {
			return retryErr
		}
		if retried {
			return nil
		}
	}
	return h.markWorkflowCriticVerdictUndeclared(ctx, instance, node, output)
}

// markWorkflowCriticVerdictUndeclared records that a review finished without
// stating a verdict, and leaves the node waiting for a person.
//
// Deliberately not a verdict row. "The reviewer did not decide" and "the
// reviewer decided it cannot be judged" are different facts with different
// remedies, and the first used to be filed as the second — so a node whose
// reviewer simply never answered looked, in the UI and in the data, exactly
// like one that had been examined and found unjudgeable.
func (h *Handler) markWorkflowCriticVerdictUndeclared(
	ctx context.Context,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	output string,
) error {
	current, err := h.Queries.GetWorkflowNodeInstanceInWorkspace(
		ctx,
		db.GetWorkflowNodeInstanceInWorkspaceParams{
			ID: node.ID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		return err
	}
	reasons := appendWorkflowWaitingReason(
		decodeWorkflowWaitingReasons(current.WaitingReasons),
		workflowdomain.WaitingReason{
			Code: "verdict_not_declared",
			// The reviewer's own words are the only evidence of what it did
			// instead. Discarding them left a stuck node explained by nothing.
			Message: workflowdomain.DescribeUndeclaredVerdict(output),
		},
	)
	if _, err := h.Queries.SetWorkflowNodeWaitingReasons(
		ctx,
		db.SetWorkflowNodeWaitingReasonsParams{
			WaitingReasons: workflowdomain.EncodeWaitingReasons(reasons),
			ID:             current.ID, WorkspaceID: instance.WorkspaceID,
		},
	); err != nil {
		return err
	}
	slog.Info(
		"workflow review ended without a declared verdict",
		"workflow_instance_id", uuidToString(instance.ID),
		"node_key", current.NodeKey,
	)
	h.publishWorkflowInstanceUpdated(
		uuidToString(instance.WorkspaceID), "system", "",
		uuidToString(instance.ID), uuidToString(current.ID),
	)
	return nil
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
	// Which revision this review was dispatched to judge. Empty on a task
	// enqueued before this was carried, where the old newest-wins behaviour is
	// the only thing available.
	var judged string
	if direct, ok := service.ParseWorkflowNodeTaskContext(task); ok {
		judged = strings.TrimSpace(direct.SubmissionID)
	}
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
	// The verdict belongs to the revision the reviewer was given, which is not
	// always the newest one: submissions are listed newest-first, so taking the
	// first valid one recorded a judgement against whatever arrived last. If a
	// new revision lands mid-review, the reviewer never saw it, and a rejection
	// of the old one would have condemned it anyway.
	var submission db.WorkflowNodeSubmission
	for _, candidate := range submissions {
		if candidate.Status != "valid" {
			continue
		}
		if judged != "" && uuidToString(candidate.ID) != judged {
			continue
		}
		submission = candidate
		break
	}
	if !submission.ID.Valid {
		if judged != "" {
			// The revision under review is gone or no longer valid. There is
			// nothing to attribute this verdict to, and inventing an
			// attribution is what this guard exists to stop. The node stays in
			// review; the reconciler dispatches a reviewer for what is current.
			slog.Info(
				"workflow critic verdict discarded: the revision it judged is no longer valid",
				"workflow_instance_id", uuidToString(instance.ID),
				"node_key", currentNode.NodeKey,
				"submission_id", judged,
			)
			return nil
		}
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
		SubmissionID:       submission.ID,
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
