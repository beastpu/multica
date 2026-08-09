package handler

// The workflow engine: the loop that decides what a run should look like now,
// and the readiness rules it decides with. Split out of workflow_node.go, which
// had grown to hold both this and the HTTP surface that triggers it — the two
// have different callers, different tests, and only one of them belongs in a
// handler package at all.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"crypto/sha256"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/util"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func workflowReviewerAssignment(
	ctx context.Context,
	q *db.Queries,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
) (string, pgtype.UUID, bool, error) {
	reviewer := nodeDefinition.Reviewer
	if reviewer == nil {
		return "", pgtype.UUID{}, false, nil
	}
	role := "reviewer"
	if reviewer.Kind == "owner" {
		role = "owner"
	}
	if reviewer.Kind == "actor" {
		actorID, err := util.ParseUUID(reviewer.ActorID)
		if err != nil {
			return "", pgtype.UUID{}, false, err
		}
		return reviewer.ActorType, actorID, true, nil
	}
	if reviewer.Kind != "role" && reviewer.Kind != "owner" {
		return "", pgtype.UUID{}, false, nil
	}
	participants, err := q.ListWorkflowNodeParticipants(
		ctx,
		db.ListWorkflowNodeParticipantsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil {
		return "", pgtype.UUID{}, false, err
	}
	for _, participant := range participants {
		if participant.Role == role {
			return participant.ActorType, participant.ActorID, true, nil
		}
	}
	return "", pgtype.UUID{}, false, nil
}

func workflowActorMatchesReviewer(
	ctx context.Context,
	q *db.Queries,
	reviewerType string,
	reviewerID pgtype.UUID,
	actorType string,
	actorID pgtype.UUID,
) (bool, error) {
	switch reviewerType {
	case "member", "agent":
		return reviewerType == actorType && reviewerID == actorID, nil
	case "squad":
		if actorType != "agent" {
			return false, nil
		}
		return q.IsSquadMember(ctx, db.IsSquadMemberParams{
			SquadID: reviewerID, MemberType: "agent", MemberID: actorID,
		})
	default:
		return false, nil
	}
}

func (h *Handler) reconcileWorkflowInstance(
	ctx context.Context,
	workspaceID, instanceID pgtype.UUID,
	actorType string,
	actorID pgtype.UUID,
	idempotencyKey string,
) (result db.WorkflowInstance, resultErr error) {
	transitioned := false
	defer func() {
		switch {
		case resultErr == nil && transitioned:
			h.Metrics.RecordWorkflowOperation("reconcile", "repaired")
		case errors.Is(resultErr, errWorkflowNoop):
			h.Metrics.RecordWorkflowOperation("reconcile", "noop")
		case resultErr != nil:
			h.Metrics.RecordWorkflowOperation("reconcile", "failed")
		}
	}()
	if featureflags.WorkflowProgressionPaused(
		ctx,
		h.FeatureFlags,
		uuidToString(workspaceID),
	) {
		return db.WorkflowInstance{}, errWorkflowProgressionPaused
	}
	current, err := h.Queries.GetWorkflowInstanceInWorkspace(ctx, db.GetWorkflowInstanceInWorkspaceParams{
		ID: instanceID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return db.WorkflowInstance{}, err
	}
	maxTransitions := 128
	// Which activities completed, and how many times each. A run that reaches
	// the limit is cycling through some subset of its graph, and the counts name
	// that subset — the one thing a reader needs to start looking.
	nodeCompletions := map[string]int{}
	for step := 0; step < maxTransitions; step++ {
		tx, err := h.TxStarter.Begin(ctx)
		if err != nil {
			return current, err
		}
		qtx := h.Queries.WithTx(tx)
		locked, err := qtx.LockWorkflowInstance(ctx, db.LockWorkflowInstanceParams{ID: instanceID, WorkspaceID: workspaceID})
		if err != nil {
			tx.Rollback(ctx)
			return current, err
		}
		if locked.Status != "running" {
			tx.Rollback(ctx)
			if transitioned {
				return locked, nil
			}
			return locked, errWorkflowNoop
		}
		version, err := qtx.GetWorkflowVersionInWorkspace(ctx, db.GetWorkflowVersionInWorkspaceParams{
			ID: locked.WorkflowVersionID, WorkspaceID: workspaceID,
		})
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		definition, err := workflowdomain.ParseDefinition(version.Definition)
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		plan, err := workflowdomain.BuildGraphPlan(definition)
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		nodes, err := qtx.ListWorkflowNodeInstances(ctx, db.ListWorkflowNodeInstancesParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		roleRows, err := qtx.ListWorkflowRoleAssignments(ctx, db.ListWorkflowRoleAssignmentsParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		roleMap := workflowRoleAssignmentsMap(roleRows)
		propagated, err := h.propagateWorkflowGraph(
			ctx, qtx, workspaceID, locked, definition, plan, roleMap,
			nodes, actorType, actorID,
		)
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		repairedTasks := false
		needsSetup := propagated.NeedsSetup
		var completedNode db.WorkflowNodeInstance
		var completionSubmission db.WorkflowNodeSubmission
		var completionVerdict db.WorkflowNodeVerdict
		var blockedNodes []db.WorkflowNodeInstance
		var carrierSyncs []workflowCarrierSync
		var createdSubmissions []db.WorkflowNodeSubmission
		var createdVerdicts []db.WorkflowNodeVerdict
		var criticDispatches []workflowCriticDispatch
		for _, active := range workflowActiveNodes(propagated.Nodes, plan) {
			nodeDefinition, exists := plan.Node(active.NodeKey)
			if !exists || nodeDefinition.Kind != "activity" {
				continue
			}
			repaired, err := ensureWorkflowNodeTasks(
				ctx, qtx, workspaceID, locked, active, nodeDefinition,
			)
			if err != nil {
				tx.Rollback(ctx)
				return locked, err
			}
			repairedTasks = repairedTasks || repaired
			nodeNeedsSetup, err := workflowNodeNeedsExecutorSetup(
				ctx, qtx, workspaceID, active.ID,
			)
			if err != nil {
				tx.Rollback(ctx)
				return locked, err
			}
			if nodeNeedsSetup {
				needsSetup = true
				if !workflowNodeBlockedForExecutor(active) {
					reasons := workflowdomain.EncodeWaitingReasons([]workflowdomain.WaitingReason{{
						Code:    "executor_needs_setup",
						Message: "One or more workflow tasks require an executor",
					}})
					blocked, updateErr := qtx.UpdateWorkflowNodeState(
						ctx,
						db.UpdateWorkflowNodeStateParams{
							Status: "blocked", WaitingReasons: reasons, MarkReconciled: true,
							ID: active.ID, WorkspaceID: workspaceID,
							ExpectedStatus: active.Status,
						},
					)
					if updateErr != nil {
						tx.Rollback(ctx)
						return locked, updateErr
					}
					propagated.Nodes[active.NodeKey] = blocked
					blockedNodes = append(blockedNodes, blocked)
				}
				continue
			}
			ready, reasons, submission, verdict, err := h.evaluateWorkflowNode(
				ctx, qtx, workspaceID, locked, active, nodeDefinition, definition,
				true,
			)
			if err != nil {
				tx.Rollback(ctx)
				return locked, err
			}
			if submission.ID.Valid &&
				(!active.LatestSubmissionID.Valid ||
					active.LatestSubmissionID != submission.ID) {
				createdSubmissions = append(createdSubmissions, submission)
			}
			if verdict.ID.Valid &&
				(!active.LatestVerdictID.Valid ||
					active.LatestVerdictID != verdict.ID) {
				createdVerdicts = append(createdVerdicts, verdict)
			}
			if ready && !completedNode.ID.Valid {
				completedNode, err = qtx.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
					Status: "completed", WaitingReasons: []byte("[]"), MarkReconciled: true,
					ID: active.ID, WorkspaceID: workspaceID, ExpectedStatus: active.Status,
				})
				if err != nil {
					tx.Rollback(ctx)
					return locked, err
				}
				propagated.Nodes[active.NodeKey] = completedNode
				nodeCompletions[completedNode.NodeKey]++
				completionSubmission = submission
				completionVerdict = verdict
				continue
			}
			if !ready {
				nextStatus := "waiting"
				switch {
				case workflowWaitingReasonsBlockNode(reasons):
					nextStatus = "blocked"
				case workflowdomain.ReviewerAcceptsActor(nodeDefinition) &&
					workflowWaitingReasonsAwaitReview(reasons):
					nextStatus = "in_review"
				}
				updatedNode, err := qtx.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
					Status: nextStatus, WaitingReasons: workflowdomain.EncodeWaitingReasons(reasons),
					MarkReconciled: true, ID: active.ID, WorkspaceID: workspaceID,
					ExpectedStatus: active.Status,
				})
				if err != nil {
					tx.Rollback(ctx)
					return locked, err
				}
				propagated.Nodes[active.NodeKey] = updatedNode
				if workflowWaitingReasonsNeedReviewer(reasons) {
					criticDispatches = append(criticDispatches, workflowCriticDispatch{
						node: updatedNode, definition: nodeDefinition,
					})
				}
				if nextStatus == "blocked" && active.Status != "blocked" {
					blockedNodes = append(blockedNodes, updatedNode)
				}
				// Review and back again. Both directions have to reach the
				// carrier or the board stops tracking the node partway through
				// its own round trip: delivered work reads as still in
				// progress, and work sent back for more reads as still under
				// review.
				if nextStatus != active.Status &&
					(nextStatus == "in_review" || nextStatus == "waiting") {
					carrierSyncs = append(carrierSyncs, workflowCarrierSync{
						node: updatedNode, event: nextStatus,
					})
				}
			}
		}

		activated := append([]db.WorkflowNodeInstance(nil), propagated.Activated...)
		graphChanged := propagated.Changed
		canComplete := propagated.CanComplete
		if completedNode.ID.Valid {
			eventPayload := map[string]any{"node_key": completedNode.NodeKey}
			if completionSubmission.ID.Valid {
				eventPayload["submission_id"] = uuidToString(completionSubmission.ID)
			}
			if completionVerdict.ID.Valid {
				eventPayload["verdict_id"] = uuidToString(completionVerdict.ID)
			}
			payload, _ := json.Marshal(eventPayload)
			transitionKey := fmt.Sprintf("advance:%s:%d", uuidToString(completedNode.ID), completedNode.Attempt)
			if strings.TrimSpace(idempotencyKey) != "" && step == 0 {
				transitionKey = idempotencyKey + ":" + transitionKey
			}
			if _, err := qtx.CreateWorkflowEvent(ctx, db.CreateWorkflowEventParams{
				WorkspaceID: workspaceID, WorkflowInstanceID: locked.ID,
				WorkflowNodeInstanceID: completedNode.ID, EventType: "node.completed",
				ActorType: actorType, ActorID: actorID,
				IdempotencyKey: transitionKey, Payload: payload,
			}); err != nil {
				tx.Rollback(ctx)
				return locked, err
			}
			nodeValues := make([]db.WorkflowNodeInstance, 0, len(propagated.Nodes))
			for _, node := range propagated.Nodes {
				nodeValues = append(nodeValues, node)
			}
			afterCompletion, err := h.propagateWorkflowGraph(
				ctx, qtx, workspaceID, locked, definition, plan, roleMap,
				nodeValues, actorType, actorID,
			)
			if err != nil {
				tx.Rollback(ctx)
				return locked, err
			}
			activated = append(activated, afterCompletion.Activated...)
			graphChanged = graphChanged || afterCompletion.Changed
			needsSetup = needsSetup || afterCompletion.NeedsSetup
			canComplete = afterCompletion.CanComplete
		}

		targetStatus := locked.Status
		var result []byte
		if needsSetup {
			targetStatus = "needs_setup"
		} else if canComplete {
			targetStatus = "completed"
			result, _ = json.Marshal(map[string]any{
				"completed_node_key": completedNode.NodeKey,
			})
		}
		updated, err := qtx.UpdateWorkflowInstanceState(ctx, db.UpdateWorkflowInstanceStateParams{
			Status: targetStatus, Result: result, MarkReconciled: true,
			ID: locked.ID, WorkspaceID: workspaceID, ExpectedRevision: locked.Revision,
		})
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		if err := tx.Commit(ctx); err != nil {
			return locked, err
		}
		h.recordWorkflowInstanceStatusTransition(locked.Status, updated.Status)
		if completedNode.ID.Valid {
			h.recordWorkflowNodeTransition(completedNode, "completed")
			h.syncWorkflowNodeIssueStatus(ctx, workspaceID, completedNode, "completed")
		}
		for _, blockedNode := range blockedNodes {
			h.recordWorkflowNodeTransition(blockedNode, "blocked")
			h.syncWorkflowNodeIssueStatus(ctx, workspaceID, blockedNode, "blocked")
			// Blocking is only useful if somebody hears about it. The list
			// holds nodes that just crossed into blocked, so this notifies on
			// the transition rather than on every reconcile that finds them
			// still stuck.
			h.notifyWorkflowIntervention(
				ctx, updated, blockedNode,
				workflowWaitingReasonsForNotification(
					decodeWorkflowWaitingReasons(blockedNode.WaitingReasons),
				),
			)
		}
		for _, carrier := range carrierSyncs {
			h.syncWorkflowNodeIssueStatus(ctx, workspaceID, carrier.node, carrier.event)
		}
		h.recordWorkflowNodesActivated(ctx, activated)
		for range createdSubmissions {
			h.Metrics.RecordWorkflowSubmission("valid")
		}
		for _, verdict := range createdVerdicts {
			h.Metrics.RecordWorkflowVerdict(
				verdict.EvaluatorType,
				verdict.Result,
			)
		}
		if repairedTasks {
			h.Metrics.RecordWorkflowOperation("task_repair", "repaired")
		}
		if updated.Status == "completed" {
			if err := h.updateManagedWorkflowHostStatus(ctx, updated, "done"); err != nil {
				return updated, err
			}
		}
		stepTransitioned := completedNode.ID.Valid || graphChanged ||
			repairedTasks || updated.Status != locked.Status
		transitioned = transitioned || stepTransitioned
		current = updated
		for _, node := range activated {
			h.materializeWorkflowNodeTasks(ctx, workspaceID, updated, node)
		}
		for _, dispatch := range criticDispatches {
			if err := h.ensureWorkflowAgentCriticTask(
				ctx, updated, dispatch.node, dispatch.definition,
			); err != nil {
				return updated, err
			}
		}
		if repairedTasks && h.WorkflowMaterializer != nil {
			h.WorkflowMaterializer.Notify()
		}
		if updated.Status == "completed" {
			return updated, nil
		}
		if !completedNode.ID.Valid {
			if transitioned {
				return updated, nil
			}
			return updated, errWorkflowNoop
		}
	}
	h.recordWorkflowTransitionLimit(ctx, current, maxTransitions, nodeCompletions)
	return current, errWorkflowTransitionLimit
}

// recordWorkflowTransitionLimit makes an exhausted reconcile findable.
//
// Every other way a run stops making progress writes a waiting reason and
// raises an intervention. This one returned a bare error that reached a log
// line and nothing else: the run stayed 'running' with no sign anything was
// wrong, and because the transitions it had just written made it due again,
// the reconciler re-claimed it every cycle to spend the same work.
//
// The idempotency key digests the completion counts, following the sweeper —
// the same cycle raises one finding, a different one is a new finding.
func (h *Handler) recordWorkflowTransitionLimit(
	ctx context.Context,
	instance db.WorkflowInstance,
	transitions int,
	nodeCompletions map[string]int,
) {
	cycling := make([]string, 0, len(nodeCompletions))
	for nodeKey := range nodeCompletions {
		cycling = append(cycling, nodeKey)
	}
	sort.Strings(cycling)
	var signature strings.Builder
	for _, nodeKey := range cycling {
		fmt.Fprintf(&signature, "%s=%d;", nodeKey, nodeCompletions[nodeKey])
	}
	digest := sha256.Sum256([]byte(signature.String()))
	payload, _ := json.Marshal(map[string]any{
		"transitions":      transitions,
		"node_completions": nodeCompletions,
	})
	event, err := h.Queries.CreateWorkflowEvent(ctx, db.CreateWorkflowEventParams{
		WorkspaceID:        instance.WorkspaceID,
		WorkflowInstanceID: instance.ID,
		EventType:          "workflow.transition_limit_exceeded",
		ActorType:          "system",
		IdempotencyKey:     fmt.Sprintf("transition-limit:%x", digest[:8]),
		Payload:            payload,
	})
	if err != nil || !event.ID.Valid {
		return
	}
	// The run is the subject, not any one activity. The limit says the graph as
	// a whole stopped settling, and the activity that happened to complete last
	// is a symptom — naming it would point the reader at the wrong thing.
	h.notifyWorkflowActionRequired(
		ctx, instance, nil, "transition_limit",
		"Workflow run stopped settling",
		fmt.Sprintf(
			"The run reached its %d-transition safety limit without settling and needs a person. Activities that kept completing: %s",
			transitions, strings.Join(cycling, ", "),
		),
		map[string]any{
			"transitions":      transitions,
			"node_completions": nodeCompletions,
		},
	)
}

func currentWorkflowNode(nodes []db.WorkflowNodeInstance) (db.WorkflowNodeInstance, bool) {
	var selected db.WorkflowNodeInstance
	found := false
	for _, node := range nodes {
		switch node.Status {
		case "active", "in_review", "waiting", "blocked":
			if !found || node.DisplayOrder < selected.DisplayOrder || (node.NodeKey == selected.NodeKey && node.Attempt > selected.Attempt) {
				selected = node
				found = true
			}
		}
	}
	return selected, found
}

func latestNodeByKey(nodes []db.WorkflowNodeInstance, key string) (db.WorkflowNodeInstance, error) {
	var selected db.WorkflowNodeInstance
	found := false
	for _, node := range nodes {
		if node.NodeKey == key && (!found || node.Attempt > selected.Attempt) {
			selected = node
			found = true
		}
	}
	if !found {
		return db.WorkflowNodeInstance{}, fmt.Errorf("workflow node %q not found", key)
	}
	return selected, nil
}

// workflowNodeTimeoutReason reports the timeout waiting reason for a node that
// has been active past its configured timeout.
//
// Both the reconciler and the sweeper persist a node's waiting reasons, and
// each rebuilds the list from scratch. Deriving the timeout here, from state
// both of them already hold, keeps the later writer from erasing what the
// earlier one recorded: an instance reads `node_timeout` to surface
// `blocked_or_timeout`, so dropping it makes a stalled activity look exactly
// like a healthy one.
func workflowNodeTimeoutReason(
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
) (workflowdomain.WaitingReason, bool) {
	if nodeDefinition.TimeoutMinutes <= 0 || !node.ActivatedAt.Valid {
		return workflowdomain.WaitingReason{}, false
	}
	deadline := node.ActivatedAt.Time.Add(
		time.Duration(nodeDefinition.TimeoutMinutes) * time.Minute,
	)
	if !time.Now().After(deadline) {
		return workflowdomain.WaitingReason{}, false
	}
	return workflowdomain.WaitingReason{
		Code:    "node_timeout",
		Message: "The activity exceeded its configured timeout",
	}, true
}

// evaluateWorkflowNode reports whether a node may complete, plus the reasons it
// cannot. The timeout is appended here rather than inside the readiness rules
// because it is not a readiness input — a node past its deadline still
// completes the moment its real obligations are met.
func (h *Handler) evaluateWorkflowNode(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	definition workflowdomain.Definition,
	includeManualCompletion bool,
) (bool, []workflowdomain.WaitingReason, db.WorkflowNodeSubmission, db.WorkflowNodeVerdict, error) {
	ready, reasons, submission, verdict, err := h.evaluateWorkflowNodeReadiness(
		ctx, q, workspaceID, instance, node, nodeDefinition, definition,
		includeManualCompletion,
	)
	if err != nil || ready {
		return ready, reasons, submission, verdict, err
	}
	if reason, timedOut := workflowNodeTimeoutReason(node, nodeDefinition); timedOut {
		reasons = appendWorkflowWaitingReason(reasons, reason)
	}
	return ready, reasons, submission, verdict, nil
}

func (h *Handler) evaluateWorkflowNodeReadiness(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	definition workflowdomain.Definition,
	includeManualCompletion bool,
) (bool, []workflowdomain.WaitingReason, db.WorkflowNodeSubmission, db.WorkflowNodeVerdict, error) {
	tasks, err := q.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, err
	}
	issues, err := q.ListWorkflowNodeIssues(ctx, db.ListWorkflowNodeIssuesParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, err
	}
	issueStatuses := make(map[pgtype.UUID]string, len(issues))
	for _, issue := range issues {
		issueStatuses[issue.ID] = issue.Status
	}
	reasons := make([]workflowdomain.WaitingReason, 0)
	// An activity that defers its issues to runtime has nothing to gate on
	// until someone decomposes it: with no tasks, "all required issues are
	// done" is true of the empty set, so the node activated and completed in
	// the same breath and the work it stood for never happened. That is the
	// one reading of "decide at runtime" nobody means.
	//
	// Checked against materialized tasks rather than the declaration, so a
	// fixed_and_dynamic node that did declare one is unaffected, and the block
	// clears the moment the first task exists.
	if workflowdomain.AllowsDynamicIssues(nodeDefinition) && len(tasks) == 0 {
		reasons = append(reasons, workflowdomain.WaitingReason{
			Code:    "awaiting_decomposition",
			Message: "Waiting for this activity to be broken into issues",
		})
	}
	requiredIssueOutcome := nodeDefinition.Completion.RequiredIssueOutcome
	if requiredIssueOutcome == "" {
		requiredIssueOutcome = "done"
	}
	for _, task := range tasks {
		if !task.Required {
			continue
		}
		if task.Source == "execution" {
			if task.MaterializationStatus != "materialized" {
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "direct_execution_not_dispatched", Field: task.TaskKey,
					Message: "Direct agent execution has not been dispatched",
				})
				continue
			}
			agentTask, taskErr := q.GetLatestAgentTaskForWorkflowNodeTask(ctx, task.ID)
			if taskErr != nil {
				if errors.Is(taskErr, pgx.ErrNoRows) {
					reasons = append(reasons, workflowdomain.WaitingReason{
						Code: "direct_execution_not_dispatched", Field: task.TaskKey,
						Message: "Direct agent execution has not been dispatched",
					})
					continue
				}
				return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, taskErr
			}
			switch agentTask.Status {
			case "completed":
			case "failed", "cancelled":
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "direct_execution_failed", Field: task.TaskKey,
					Message: "Direct agent execution did not complete successfully",
				})
			default:
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "direct_execution_running", Field: task.TaskKey,
					Message: "Direct agent execution is still running",
				})
			}
			continue
		}
		if requiredIssueOutcome == "none" {
			continue
		}
		if task.MaterializationStatus != "materialized" || !task.IssueID.Valid {
			reasons = append(reasons, workflowdomain.WaitingReason{
				Code: "required_task_not_materialized", Field: task.TaskKey,
				Message: "Required task has not been materialized",
			})
			continue
		}
		// Whoever did the work decides. When a run owns this task the run's
		// outcome is the answer and the issue is its mirror: an agent that
		// delivered has nothing left to do, and holding the node until someone
		// also drags the issue to done strands finished work behind a status
		// change nobody owes. Only a task no run ever claimed — a person
		// working in the issue — is answered by the issue's own status.
		owner, ownerErr := q.GetOwningAgentTaskForWorkflowNodeTask(
			ctx,
			db.GetOwningAgentTaskForWorkflowNodeTaskParams{
				WorkflowNodeTaskID: task.ID, IssueID: task.IssueID,
			},
		)
		if ownerErr != nil && !errors.Is(ownerErr, pgx.ErrNoRows) {
			return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, ownerErr
		}
		if ownerErr == nil {
			switch owner.Status {
			case "completed":
			case "failed", "cancelled":
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "direct_execution_failed", Field: task.TaskKey,
					Message: "Direct agent execution did not complete successfully",
				})
			default:
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "direct_execution_running", Field: task.TaskKey,
					Message: "Direct agent execution is still running",
				})
			}
			continue
		}
		status := issueStatuses[task.IssueID]
		outcomeSatisfied := status == "done"
		if requiredIssueOutcome == "terminal" {
			outcomeSatisfied = status == "done" || status == "cancelled"
		}
		if !outcomeSatisfied {
			code := "required_issue_not_done"
			message := "Required issue has not reached the configured outcome"
			if status == "cancelled" && requiredIssueOutcome == "done" {
				code = "required_issue_cancelled"
				message = "Required issue was cancelled under a done-only policy"
			}
			reasons = append(reasons, workflowdomain.WaitingReason{
				Code: code, Field: task.TaskKey, Message: message,
			})
		}
	}
	if required := workflowdomain.RequiredArtifacts(nodeDefinition); len(required) > 0 {
		artifacts, err := q.ListWorkflowNodeArtifacts(ctx, db.ListWorkflowNodeArtifactsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, err
		}
		delivered := make(map[string]db.WorkflowArtifact, len(artifacts))
		for _, artifact := range artifacts {
			delivered[artifact.ArtifactKey] = artifact
		}
		for _, requirement := range required {
			artifact, exists := delivered[requirement.Key]
			if !exists {
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "required_artifact_missing", Field: requirement.Key,
					Message: "Required artifact has not been submitted",
				})
				continue
			}
			// A rejected artifact is an explicit "not acceptable", so it blocks
			// exactly like a missing one. Submitted and approved both let the
			// delivery enter review; actor-reviewed nodes have a stricter
			// post-verdict gate below and cannot leave until required artifacts
			// are approved.
			if artifact.ReviewStatus == "rejected" {
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "required_artifact_rejected", Field: requirement.Key,
					Message: "Required artifact was rejected in review",
				})
			}
		}
	}
	if len(reasons) > 0 {
		return false, reasons, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, nil
	}
	submissions, err := q.ListWorkflowSubmissions(ctx, db.ListWorkflowSubmissionsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, err
	}
	var submission db.WorkflowNodeSubmission
	validSubmissionsByIssue := map[pgtype.UUID]struct{}{}
	for _, candidate := range submissions {
		if candidate.Status != "valid" {
			continue
		}
		if !submission.ID.Valid {
			submission = candidate
		}
		if candidate.SourceIssueID.Valid {
			validSubmissionsByIssue[candidate.SourceIssueID] = struct{}{}
		}
	}
	submissionPolicy := workflowdomain.SubmissionPolicy(nodeDefinition)
	if submissionPolicy == "per_required_task" || submissionPolicy == "fan_in" {
		missingTaskSubmissions := make([]workflowdomain.WaitingReason, 0)
		requiredTaskCount := 0
		for _, task := range tasks {
			if !task.Required {
				continue
			}
			requiredTaskCount++
			if !task.IssueID.Valid {
				missingTaskSubmissions = append(
					missingTaskSubmissions,
					workflowdomain.WaitingReason{
						Code: "task_submission_required", Field: task.TaskKey,
						Message: "Required task needs a valid structured submission",
					},
				)
				continue
			}
			if _, exists := validSubmissionsByIssue[task.IssueID]; !exists {
				missingTaskSubmissions = append(
					missingTaskSubmissions,
					workflowdomain.WaitingReason{
						Code: "task_submission_required", Field: task.TaskKey,
						Message: "Required task needs a valid structured submission",
					},
				)
			}
		}
		if len(missingTaskSubmissions) > 0 {
			return false, missingTaskSubmissions, submission, db.WorkflowNodeVerdict{}, nil
		}
		if requiredTaskCount == 0 && !submission.ID.Valid {
			return false, []workflowdomain.WaitingReason{{
				Code:    "valid_submission_required",
				Message: "At least one valid structured submission is required",
			}}, submission, db.WorkflowNodeVerdict{}, nil
		}
	}
	reviewRequired := workflowdomain.RequiresReview(nodeDefinition)
	submissionRequired := nodeDefinition.Completion.SubmissionRequired ||
		submissionPolicy != "none"
	if !submission.ID.Valid && nodeDefinition.SubmissionSchema == nil &&
		(submissionRequired || reviewRequired) {
		revision, err := q.GetNextWorkflowSubmissionRevision(ctx, db.GetNextWorkflowSubmissionRevisionParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return false, nil, submission, db.WorkflowNodeVerdict{}, err
		}
		basis, _ := json.Marshal(map[string]any{"kind": "all_required_issues_done"})
		submission, err = q.CreateWorkflowSubmission(ctx, db.CreateWorkflowSubmissionParams{
			WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID, WorkflowNodeInstanceID: node.ID,
			Revision: revision, Status: "valid", Payload: basis, Summary: "All required issues are done",
			Evidence: []byte("[]"), SubmittedByType: "system", SchemaVersion: 1,
		})
		if err != nil {
			return false, nil, submission, db.WorkflowNodeVerdict{}, err
		}
		if err := q.SetWorkflowNodeLatestSubmission(ctx, db.SetWorkflowNodeLatestSubmissionParams{
			LatestSubmissionID: submission.ID, ID: node.ID, WorkspaceID: workspaceID,
		}); err != nil {
			return false, nil, submission, db.WorkflowNodeVerdict{}, err
		}
	}
	if !submission.ID.Valid && submissionRequired {
		return false, []workflowdomain.WaitingReason{{
			Code: "valid_submission_required", Message: "A valid structured submission is required",
		}}, submission, db.WorkflowNodeVerdict{}, nil
	}
	// Declared output fields are a delivery the node owes, and only a real
	// submission carries them: the synthesised one above records that the
	// issues are done and nothing else. Completing on it would leave the
	// variable pool empty, and a downstream gateway would fail closed to its
	// else branch with nobody having made the decision — the silent routing
	// that declared outputs exist to prevent. Held here rather than refused
	// at submit time so the work itself is never blocked, only its handover.
	if missing := workflowdomain.MissingRequiredOutputs(
		nodeDefinition.Outputs, workflowSubmissionOutputs(submission),
	); len(missing) > 0 {
		reasons := make([]workflowdomain.WaitingReason, 0, len(missing))
		for _, key := range missing {
			reasons = append(reasons, workflowdomain.WaitingReason{
				Code:    "output_field_required",
				Field:   key,
				Message: "The activity owes the output field " + key,
			})
		}
		return false, reasons, submission, db.WorkflowNodeVerdict{}, nil
	}
	if !reviewRequired {
		return h.evaluateWorkflowManualCompletion(
			ctx, q, workspaceID, node, nodeDefinition, submission,
			db.WorkflowNodeVerdict{}, includeManualCompletion,
		)
	}
	if workflowdomain.ReviewerAcceptsActor(nodeDefinition) {
		reviewerType, reviewerID, resolved, err := workflowReviewerAssignment(
			ctx, q, node, nodeDefinition,
		)
		if err != nil {
			return false, nil, submission, db.WorkflowNodeVerdict{}, err
		}
		if !resolved {
			return false, []workflowdomain.WaitingReason{{
				Code: "review_required", Message: "Waiting for a reviewer assignment",
			}}, submission, db.WorkflowNodeVerdict{}, nil
		}
		verdicts, err := q.ListWorkflowVerdicts(ctx, db.ListWorkflowVerdictsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return false, nil, submission, db.WorkflowNodeVerdict{}, err
		}
		var verdict db.WorkflowNodeVerdict
		for _, candidate := range verdicts {
			matches, matchErr := workflowActorMatchesReviewer(
				ctx, q, reviewerType, reviewerID,
				candidate.EvaluatorType, candidate.EvaluatorID,
			)
			if matchErr != nil {
				return false, nil, submission, db.WorkflowNodeVerdict{}, matchErr
			}
			if matches && !workflowVerdictIsManualCompletion(candidate) {
				verdict = candidate
				break
			}
		}
		if !verdict.ID.Valid {
			return false, []workflowdomain.WaitingReason{{
				Code: "review_required", Message: "Waiting for the reviewer",
			}}, submission, db.WorkflowNodeVerdict{}, nil
		}
		if !workflowVerdictSatisfies(verdict.Result) {
			return false, []workflowdomain.WaitingReason{
				workflowVerdictWaitingReason(verdict),
			}, submission, verdict, nil
		}
		artifactReasons, err := workflowArtifactApprovalReasons(
			ctx, q, workspaceID, node, nodeDefinition,
		)
		if err != nil {
			return false, nil, submission, verdict, err
		}
		if len(artifactReasons) > 0 {
			return false, artifactReasons, submission, verdict, nil
		}
		return h.evaluateWorkflowManualCompletion(
			ctx, q, workspaceID, node, nodeDefinition, submission, verdict,
			includeManualCompletion,
		)
	}
	if nodeDefinition.Reviewer.Kind == "api" {
		result, reason := h.evaluateAPIWorkflowVerdict(ctx, nodeDefinition.Reviewer.APIURL)
		if !workflowVerdictSatisfies(result) {
			return false, []workflowdomain.WaitingReason{{
				Code: "api_verdict_not_passed", Message: reason,
			}}, submission, db.WorkflowNodeVerdict{}, nil
		}
		return h.evaluateWorkflowManualCompletion(
			ctx, q, workspaceID, node, nodeDefinition, submission,
			db.WorkflowNodeVerdict{}, includeManualCompletion,
		)
	}
	verdictResult, verdictReason, verdictBasis, err :=
		h.evaluateDeterministicWorkflowVerdict(
			ctx,
			q,
			workspaceID,
			instance,
			nodeDefinition.Reviewer.Condition,
		)
	if err != nil {
		return false, nil, submission, db.WorkflowNodeVerdict{}, err
	}
	verdicts, err := q.ListWorkflowVerdicts(ctx, db.ListWorkflowVerdictsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, nil, submission, db.WorkflowNodeVerdict{}, err
	}
	for _, candidate := range verdicts {
		if candidate.EvaluatorType != "deterministic" {
			continue
		}
		var basis map[string]any
		_ = json.Unmarshal(candidate.Basis, &basis)
		sameInput := !submission.ID.Valid ||
			basis["submission_id"] == uuidToString(submission.ID)
		sameEvaluation := true
		if matched, exists := verdictBasis["condition_matched"]; exists {
			sameEvaluation = basis["condition_matched"] == matched
		}
		if sameInput && sameEvaluation {
			if !workflowVerdictSatisfies(candidate.Result) {
				return false, []workflowdomain.WaitingReason{
					workflowVerdictWaitingReason(candidate),
				}, submission, candidate, nil
			}
			return h.evaluateWorkflowManualCompletion(
				ctx, q, workspaceID, node, nodeDefinition, submission, candidate,
				includeManualCompletion,
			)
		}
	}
	revision, err := q.GetNextWorkflowVerdictRevision(ctx, db.GetNextWorkflowVerdictRevisionParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, nil, submission, db.WorkflowNodeVerdict{}, err
	}
	basisMap := map[string]any{"rule": "configured_completion_predicates"}
	if submission.ID.Valid {
		basisMap["submission_id"] = uuidToString(submission.ID)
	}
	for key, value := range verdictBasis {
		basisMap[key] = value
	}
	basis, _ := json.Marshal(basisMap)
	definitionSnapshot, _ := json.Marshal(nodeDefinition.Reviewer)
	verdict, err := q.CreateWorkflowVerdict(ctx, db.CreateWorkflowVerdictParams{
		WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID, WorkflowNodeInstanceID: node.ID,
		Revision: revision, Result: verdictResult, Reason: verdictReason,
		Evidence: []byte("[]"), Basis: basis, EvaluatorType: "deterministic",
		DefinitionSnapshot: definitionSnapshot,
	})
	if err != nil {
		return false, nil, submission, verdict, err
	}
	if err := q.SetWorkflowNodeLatestVerdict(ctx, db.SetWorkflowNodeLatestVerdictParams{
		LatestVerdictID: verdict.ID, ID: node.ID, WorkspaceID: workspaceID,
	}); err != nil {
		return false, nil, submission, verdict, err
	}
	if !workflowVerdictSatisfies(verdict.Result) {
		return false, []workflowdomain.WaitingReason{
			workflowVerdictWaitingReason(verdict),
		}, submission, verdict, nil
	}
	return h.evaluateWorkflowManualCompletion(
		ctx, q, workspaceID, node, nodeDefinition, submission, verdict,
		includeManualCompletion,
	)
}

func (h *Handler) evaluateWorkflowManualCompletion(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	submission db.WorkflowNodeSubmission,
	currentVerdict db.WorkflowNodeVerdict,
	includeManualCompletion bool,
) (bool, []workflowdomain.WaitingReason, db.WorkflowNodeSubmission, db.WorkflowNodeVerdict, error) {
	if !includeManualCompletion ||
		!workflowdomain.RequiresManualCompletion(nodeDefinition) {
		return true, nil, submission, currentVerdict, nil
	}
	verdicts, err := q.ListWorkflowVerdicts(
		ctx,
		db.ListWorkflowVerdictsParams{
			WorkflowNodeInstanceID: node.ID,
			WorkspaceID:            workspaceID,
		},
	)
	if err != nil {
		return false, nil, submission, currentVerdict, err
	}
	for _, verdict := range verdicts {
		if verdict.EvaluatorType == "member" && verdict.Result == "pass" &&
			workflowVerdictIsManualCompletion(verdict) {
			return true, nil, submission, verdict, nil
		}
	}
	return false, []workflowdomain.WaitingReason{{
		Code:    "manual_completion_required",
		Message: "Waiting for the node owner to complete this activity",
	}}, submission, currentVerdict, nil
}

func workflowVerdictIsManualCompletion(verdict db.WorkflowNodeVerdict) bool {
	var basis map[string]any
	_ = json.Unmarshal(verdict.Basis, &basis)
	return basis["kind"] == "manual_completion"
}

// workflowWaitingReasonsAwaitReview reports whether the node is stopped on its
// reviewer rather than on its own work. The distinction is what the canvas
// renders: the executor has delivered, and the flow is waiting on someone else.
//
// Callers pair this with ReviewerAcceptsActor, because "someone else" has to
// be a member or agent. An api or auto reviewer already answered — a rule that
// evaluated to no is a node that is waiting, not one under review.
func workflowWaitingReasonsAwaitReview(
	reasons []workflowdomain.WaitingReason,
) bool {
	for _, reason := range reasons {
		if reason.Code == "review_required" || reason.Code == "verdict_not_passed" {
			return true
		}
	}
	return false
}

func workflowWaitingReasonsNeedReviewer(
	reasons []workflowdomain.WaitingReason,
) bool {
	for _, reason := range reasons {
		if reason.Code == "review_required" {
			return true
		}
	}
	return false
}

func workflowWaitingReasonsBlockNode(
	reasons []workflowdomain.WaitingReason,
) bool {
	for _, reason := range reasons {
		if reason.Code == "required_issue_cancelled" ||
			reason.Code == "verdict_blocked" ||
			// An agent that failed is not a step still in progress. Left as
			// "waiting" the node reads like every other node making its way,
			// and the run sits on work that already stopped until somebody
			// happens to look.
			reason.Code == "direct_execution_failed" {
			return true
		}
	}
	return false
}

func workflowVerdictWaitingReason(
	verdict db.WorkflowNodeVerdict,
) workflowdomain.WaitingReason {
	code := "verdict_not_passed"
	if verdict.Result == "blocked" {
		code = "verdict_blocked"
	}
	return workflowdomain.WaitingReason{Code: code, Message: verdict.Reason}
}

func hasWorkflowJSONValue(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

func (h *Handler) evaluateDeterministicWorkflowVerdict(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	condition json.RawMessage,
) (string, string, map[string]any, error) {
	result := "pass"
	reason := "All deterministic completion conditions passed"
	basis := map[string]any{}
	if !hasWorkflowJSONValue(condition) {
		return result, reason, basis, nil
	}
	nodeRows, err := q.ListWorkflowNodeInstances(
		ctx,
		db.ListWorkflowNodeInstancesParams{
			WorkflowInstanceID: instance.ID,
			WorkspaceID:        workspaceID,
		},
	)
	if err != nil {
		return "", "", nil, err
	}
	resolver, err := workflowConditionResolver(
		ctx,
		q,
		workspaceID,
		instance,
		latestWorkflowNodesByKey(nodeRows),
	)
	if err != nil {
		return "", "", nil, err
	}
	matches, err := workflowdomain.EvaluateCondition(condition, resolver)
	if err != nil {
		return "", "", nil, err
	}
	basis["condition"] = json.RawMessage(condition)
	basis["condition_matched"] = matches
	if !matches {
		result = "fail"
		reason = "The deterministic verdict condition did not match"
	}
	return result, reason, basis, nil
}

// workflowVerdictSatisfies reports whether a verdict releases the node. A
// reviewer either passed the output or did not; the old required_result knob
// let a template accept a failing verdict, which no template ever did and
// which made "reviewed" mean two different things.
func workflowVerdictSatisfies(result string) bool {
	return result == "pass"
}

// workflowSubmissionOutputs reads the delivered field values off a submission.
// An absent submission yields an empty map, so a node with nothing delivered
// reads as "owes everything" rather than panicking.
func workflowSubmissionOutputs(submission db.WorkflowNodeSubmission) map[string]any {
	if !submission.ID.Valid {
		return map[string]any{}
	}
	return decodeWorkflowObject(submission.Payload)
}

func decodeWorkflowObject(raw []byte) map[string]any {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return map[string]any{}
	}
	return value
}

// evaluateAPIWorkflowVerdict asks an external endpoint whether the node passes.
//
// Anything other than a clean "pass" is treated as blocked, never as approval:
// a check that times out, errors, or answers in a shape we do not recognise has
// not approved anything, and defaulting the other way would let an unreachable
// endpoint wave work through.
func (h *Handler) evaluateAPIWorkflowVerdict(
	ctx context.Context,
	apiURL string,
) (string, string) {
	requestCtx, cancel := context.WithTimeout(ctx, workflowAPIVerdictTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "blocked", "Check endpoint address is invalid"
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "blocked", "Check endpoint is unreachable"
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "blocked", fmt.Sprintf("Check endpoint returned %d", response.StatusCode)
	}
	var payload struct {
		Result string `json:"result"`
		Reason string `json:"reason"`
	}
	// Bound the body: an endpoint streaming megabytes is a fault, not a verdict.
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return "blocked", "Check endpoint returned an unreadable body"
	}
	switch payload.Result {
	case "pass", "fail", "blocked":
	default:
		return "blocked", "Check endpoint returned an unknown result"
	}
	reason := strings.TrimSpace(payload.Reason)
	if reason == "" {
		reason = "Check result: " + payload.Result
	}
	return payload.Result, reason
}
