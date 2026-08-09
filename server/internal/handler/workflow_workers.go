package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	workflowMaterializerPollInterval = time.Second
	workflowMaterializerMaxAttempts  = 5
	workflowReconcilerPollInterval   = 2 * time.Second
	workflowReconcilerDeferInterval  = 30 * time.Second
	workflowSweeperInterval          = 30 * time.Second
	workflowStaleClaimAfter          = 2 * time.Minute
)

var errWorkflowProgressionPaused = errors.New("workflow progression is paused")

type WorkflowMaterializer struct {
	h      *Handler
	notify chan struct{}
	done   chan struct{}
	once   sync.Once
}

func NewWorkflowMaterializer(h *Handler) *WorkflowMaterializer {
	return &WorkflowMaterializer{
		h: h, notify: make(chan struct{}, 1), done: make(chan struct{}),
	}
}

func (w *WorkflowMaterializer) Notify() {
	if w == nil {
		return
	}
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func (w *WorkflowMaterializer) Run(ctx context.Context) {
	if w == nil {
		return
	}
	defer w.once.Do(func() { close(w.done) })
	ticker := time.NewTicker(workflowMaterializerPollInterval)
	defer ticker.Stop()
	for {
		worked, err := w.ProcessNext(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("workflow materializer failed", "error", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.notify:
		case <-ticker.C:
		}
	}
}

func (w *WorkflowMaterializer) ProcessNext(ctx context.Context) (bool, error) {
	if w == nil || w.h == nil || w.h.Queries == nil {
		return false, nil
	}
	task, err := w.h.Queries.ClaimWorkflowNodeTaskForMaterialization(
		ctx, workflowMaterializerMaxAttempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim workflow node task: %w", err)
	}
	workspaceID := uuidToString(task.WorkspaceID)
	if !featureflags.WorkflowsActivityEngineEnabledForWorkspace(
		ctx, w.h.FeatureFlags, workspaceID,
	) || featureflags.WorkflowProgressionPaused(ctx, w.h.FeatureFlags, workspaceID) {
		_, resetErr := w.h.Queries.ReleaseWorkflowNodeTaskMaterializationClaim(
			ctx,
			db.ReleaseWorkflowNodeTaskMaterializationClaimParams{
				ID: task.ID, WorkspaceID: task.WorkspaceID,
			},
		)
		// This claim did not perform a materialization attempt. Returning false
		// makes the worker wait for its poll interval instead of hot-looping on
		// the same paused task.
		return false, resetErr
	}
	node, err := w.h.Queries.GetWorkflowNodeInstanceInWorkspace(
		ctx,
		db.GetWorkflowNodeInstanceInWorkspaceParams{
			ID: task.WorkflowNodeInstanceID, WorkspaceID: task.WorkspaceID,
		},
	)
	if err != nil {
		return true, w.h.failWorkflowTaskMaterialization(
			ctx, task.WorkspaceID, task.ID, "workflow node not found",
		)
	}
	instance, err := w.h.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{
			ID: task.WorkflowInstanceID, WorkspaceID: task.WorkspaceID,
		},
	)
	if err != nil {
		return true, w.h.failWorkflowTaskMaterialization(
			ctx, task.WorkspaceID, task.ID, "workflow instance not found",
		)
	}
	if instance.Status != "running" ||
		(node.Status != "active" && node.Status != "waiting" && node.Status != "blocked") {
		return true, w.h.failWorkflowTaskMaterialization(
			ctx, task.WorkspaceID, task.ID, "workflow node is no longer active",
		)
	}
	err = w.h.materializeWorkflowTask(
		ctx, task.WorkspaceID, instance, node, task,
	)
	w.h.publishWorkflowTaskUpdated(
		workspaceID, "system", "", uuidToString(instance.ID),
		uuidToString(node.ID), uuidToString(task.ID),
	)
	if err != nil {
		slog.Warn(
			"workflow materialization deferred",
			"workspace_id", workspaceID,
			"workflow_template_id", uuidToString(instance.WorkflowID),
			"workflow_version_id", uuidToString(instance.WorkflowVersionID),
			"workflow_instance_id", uuidToString(instance.ID),
			"host_issue_id", uuidToString(instance.HostIssueID),
			"node_key", node.NodeKey,
			"node_instance_id", uuidToString(node.ID),
			"node_attempt", node.Attempt,
			"node_task_id", uuidToString(task.ID),
			"issue_id", uuidToString(task.IssueID),
			"failure_type", "materialization_deferred",
			"retry_count", task.AttemptCount,
			"error", err,
		)
		return true, nil
	}
	return true, nil
}

func (w *WorkflowMaterializer) WaitWithTimeout(timeout time.Duration) bool {
	if w == nil {
		return true
	}
	return waitWorkflowWorker(w.done, timeout)
}

type WorkflowReconciler struct {
	h      *Handler
	notify chan struct{}
	done   chan struct{}
	once   sync.Once
}

func NewWorkflowReconciler(h *Handler) *WorkflowReconciler {
	return &WorkflowReconciler{
		h: h, notify: make(chan struct{}, 1), done: make(chan struct{}),
	}
}

func (w *WorkflowReconciler) Notify() {
	if w == nil {
		return
	}
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func (w *WorkflowReconciler) Run(ctx context.Context) {
	if w == nil {
		return
	}
	defer w.once.Do(func() { close(w.done) })
	ticker := time.NewTicker(workflowReconcilerPollInterval)
	defer ticker.Stop()
	for {
		worked, err := w.ProcessNext(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("workflow reconciler failed", "error", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.notify:
		case <-ticker.C:
		}
	}
}

func (w *WorkflowReconciler) ProcessNext(ctx context.Context) (bool, error) {
	if w == nil || w.h == nil || w.h.Queries == nil {
		return false, nil
	}
	instance, err := w.h.Queries.ClaimWorkflowInstanceForReconcile(
		ctx, workflowReconcilerPollInterval.Seconds(),
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim workflow instance: %w", err)
	}
	workspaceID := uuidToString(instance.WorkspaceID)
	if !featureflags.WorkflowsActivityEngineEnabledForWorkspace(
		ctx, w.h.FeatureFlags, workspaceID,
	) || featureflags.WorkflowProgressionPaused(ctx, w.h.FeatureFlags, workspaceID) {
		_, releaseErr := w.h.Queries.DeferWorkflowInstanceReconcile(
			ctx,
			db.DeferWorkflowInstanceReconcileParams{
				ID: instance.ID, WorkspaceID: instance.WorkspaceID,
				DeferSeconds: workflowReconcilerDeferInterval.Seconds(),
			},
		)
		// Preserve the pending reconcile signal while progression is paused and
		// defer this instance so other workspaces can make progress. A successful
		// deferral counts as work so the loop immediately claims the next due row.
		return releaseErr == nil, releaseErr
	}
	if instance.Status == "completed" {
		if err := w.h.updateManagedWorkflowHostStatus(ctx, instance, "done"); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := w.h.updateManagedWorkflowHostStatus(
		ctx,
		instance,
		"in_progress",
	); err != nil {
		return true, err
	}
	updated, reconcileErr := w.h.reconcileWorkflowInstance(
		ctx, instance.WorkspaceID, instance.ID, "system", pgtype.UUID{},
		"worker-reconcile:"+uuidToString(instance.ID),
	)
	if errors.Is(reconcileErr, errWorkflowTransitionLimit) {
		// Already recorded as an event and raised for intervention, so this is
		// handled rather than failed. It still has to back off: the claim wakes
		// on node updated_at, and the transitions the run just wrote make it due
		// again immediately, which would spend the whole limit every cycle.
		slog.Warn(
			"workflow reconcile hit the transition safety limit",
			"workspace_id", workspaceID,
			"workflow_template_id", uuidToString(instance.WorkflowID),
			"workflow_version_id", uuidToString(instance.WorkflowVersionID),
			"workflow_instance_id", uuidToString(instance.ID),
			"host_issue_id", uuidToString(instance.HostIssueID),
			"failure_type", "transition_limit_exceeded",
		)
		_, deferErr := w.h.Queries.DeferWorkflowInstanceReconcile(
			ctx,
			db.DeferWorkflowInstanceReconcileParams{
				ID: instance.ID, WorkspaceID: instance.WorkspaceID,
				DeferSeconds: workflowReconcilerDeferInterval.Seconds(),
			},
		)
		return deferErr == nil, deferErr
	}
	if reconcileErr != nil && !errors.Is(reconcileErr, errWorkflowNoop) {
		slog.Warn(
			"workflow reconcile failed",
			"workspace_id", workspaceID,
			"workflow_template_id", uuidToString(instance.WorkflowID),
			"workflow_version_id", uuidToString(instance.WorkflowVersionID),
			"workflow_instance_id", uuidToString(instance.ID),
			"host_issue_id", uuidToString(instance.HostIssueID),
			"failure_type", "reconcile_failed",
			"error", reconcileErr,
		)
		return true, reconcileErr
	}
	if updated.ID.Valid {
		w.h.publishWorkflowInstanceUpdated(
			workspaceID, "system", "", uuidToString(updated.ID), "",
		)
	}
	return true, nil
}

func (w *WorkflowReconciler) WaitWithTimeout(timeout time.Duration) bool {
	if w == nil {
		return true
	}
	return waitWorkflowWorker(w.done, timeout)
}

type WorkflowSweeper struct {
	h    *Handler
	done chan struct{}
	once sync.Once
}

func NewWorkflowSweeper(h *Handler) *WorkflowSweeper {
	return &WorkflowSweeper{h: h, done: make(chan struct{})}
}

func (w *WorkflowSweeper) Run(ctx context.Context) {
	if w == nil {
		return
	}
	defer w.once.Do(func() { close(w.done) })
	ticker := time.NewTicker(workflowSweeperInterval)
	defer ticker.Stop()
	for {
		if err := w.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("workflow sweeper failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *WorkflowSweeper) SweepOnce(ctx context.Context) error {
	if w == nil || w.h == nil || w.h.Queries == nil {
		return nil
	}
	nodes, err := w.h.Queries.ListWorkflowNodesForSweep(ctx, 200)
	if err != nil {
		return fmt.Errorf("list workflow nodes for sweep: %w", err)
	}
	for _, node := range nodes {
		var definition workflowdomain.NodeDefinition
		if json.Unmarshal(node.DefinitionSnapshot, &definition) != nil {
			continue
		}
		tasks, taskErr := w.h.Queries.ListWorkflowNodeTasks(
			ctx,
			db.ListWorkflowNodeTasksParams{
				WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
			},
		)
		if taskErr != nil {
			return taskErr
		}
		reasons := decodeWorkflowWaitingReasons(node.WaitingReasons)
		existingTaskKeys := make(map[string]struct{}, len(tasks))
		hasUnresolvedExecutor := false
		for _, task := range tasks {
			existingTaskKeys[task.TaskKey] = struct{}{}
			if !task.ExecutorResolutionID.Valid &&
				task.MaterializationStatus != "cancelled" {
				hasUnresolvedExecutor = true
			}
		}
		hasMissingTask := false
		for _, template := range workflowdomain.NodeIssueTemplates(definition) {
			if _, exists := existingTaskKeys[template.Key]; !exists {
				hasMissingTask = true
				break
			}
		}
		if hasMissingTask {
			reasons = appendWorkflowWaitingReason(reasons, workflowdomain.WaitingReason{
				Code: "missing_node_task", Message: "An active activity is missing a declared task",
			})
		}
		if hasUnresolvedExecutor {
			reasons = appendWorkflowWaitingReason(reasons, workflowdomain.WaitingReason{
				Code: "executor_unresolved", Message: "A workflow task needs a manual executor",
			})
		}
		if reason, timedOut := workflowNodeTimeoutReason(node, definition); timedOut {
			reasons = appendWorkflowWaitingReason(reasons, reason)
		}
		if !workflowWaitingReasonsEqual(node.WaitingReasons, reasons) {
			encoded := workflowdomain.EncodeWaitingReasons(reasons)
			updated, updateErr := w.h.Queries.SetWorkflowNodeWaitingReasons(
				ctx,
				db.SetWorkflowNodeWaitingReasonsParams{
					WaitingReasons: encoded, ID: node.ID, WorkspaceID: node.WorkspaceID,
				},
			)
			if updateErr != nil && !errors.Is(updateErr, pgx.ErrNoRows) {
				return updateErr
			}
			if updated.ID.Valid {
				action := "observe_only"
				if hasMissingTask &&
					featureflags.WorkflowsActivityEngineEnabledForWorkspace(
						ctx, w.h.FeatureFlags, uuidToString(node.WorkspaceID),
					) &&
					!featureflags.WorkflowProgressionPaused(
						ctx, w.h.FeatureFlags, uuidToString(node.WorkspaceID),
					) {
					action = "notify_reconciler"
				}
				payload, _ := json.Marshal(map[string]any{
					"waiting_reasons": reasons,
					"action":          action,
				})
				digest := sha256.Sum256(encoded)
				event, eventErr := w.h.Queries.CreateWorkflowEvent(
					ctx,
					db.CreateWorkflowEventParams{
						WorkspaceID:            node.WorkspaceID,
						WorkflowInstanceID:     node.WorkflowInstanceID,
						WorkflowNodeInstanceID: node.ID,
						EventType:              "workflow.sweeper_anomaly",
						ActorType:              "system",
						IdempotencyKey: fmt.Sprintf(
							"sweeper:%s:%x",
							uuidToString(node.ID),
							digest[:8],
						),
						Payload: payload,
					},
				)
				if eventErr == nil && event.ID.Valid {
					instance, instanceErr := w.h.Queries.GetWorkflowInstanceInWorkspace(
						ctx,
						db.GetWorkflowInstanceInWorkspaceParams{
							ID:          node.WorkflowInstanceID,
							WorkspaceID: node.WorkspaceID,
						},
					)
					if instanceErr == nil {
						w.h.notifyWorkflowIntervention(
							ctx,
							instance,
							node,
							workflowWaitingReasonsForNotification(reasons),
						)
					}
				}
				w.h.publishWorkflowNodeUpdated(
					uuidToString(node.WorkspaceID), "system", "",
					uuidToString(node.WorkflowInstanceID), uuidToString(node.ID),
				)
			}
		}
		if hasMissingTask &&
			featureflags.WorkflowsActivityEngineEnabledForWorkspace(
				ctx, w.h.FeatureFlags, uuidToString(node.WorkspaceID),
			) &&
			!featureflags.WorkflowProgressionPaused(
				ctx, w.h.FeatureFlags, uuidToString(node.WorkspaceID),
			) {
			w.h.WorkflowReconciler.Notify()
		}
	}
	stale, err := w.h.Queries.ListStaleWorkflowMaterializations(
		ctx,
		db.ListStaleWorkflowMaterializationsParams{
			StaleAfterSeconds: workflowStaleClaimAfter.Seconds(), RowLimit: 200,
		},
	)
	if err != nil {
		return fmt.Errorf("list stale workflow materializations: %w", err)
	}
	for _, task := range stale {
		workspaceID := uuidToString(task.WorkspaceID)
		canRepair := featureflags.WorkflowsActivityEngineEnabledForWorkspace(
			ctx, w.h.FeatureFlags, workspaceID,
		) && !featureflags.WorkflowProgressionPaused(ctx, w.h.FeatureFlags, workspaceID)
		if canRepair {
			if _, resetErr := w.h.Queries.ResetWorkflowNodeTaskMaterialization(
				ctx,
				db.ResetWorkflowNodeTaskMaterializationParams{
					ID: task.ID, WorkspaceID: task.WorkspaceID,
				},
			); resetErr != nil {
				return resetErr
			}
			w.h.WorkflowMaterializer.Notify()
		}
		node, nodeErr := w.h.Queries.GetWorkflowNodeInstanceInWorkspace(
			ctx,
			db.GetWorkflowNodeInstanceInWorkspaceParams{
				ID: task.WorkflowNodeInstanceID, WorkspaceID: task.WorkspaceID,
			},
		)
		if nodeErr != nil {
			continue
		}
		reasons := appendWorkflowWaitingReason(
			decodeWorkflowWaitingReasons(node.WaitingReasons),
			workflowdomain.WaitingReason{
				Code:    "stale_materialization",
				Field:   task.TaskKey,
				Message: "A workflow task materialization lease became stale",
			},
		)
		_, _ = w.h.Queries.SetWorkflowNodeWaitingReasons(
			ctx,
			db.SetWorkflowNodeWaitingReasonsParams{
				WaitingReasons: workflowdomain.EncodeWaitingReasons(reasons),
				ID:             node.ID, WorkspaceID: node.WorkspaceID,
			},
		)
		payload, _ := json.Marshal(map[string]any{
			"waiting_reason": map[string]any{
				"code": "stale_materialization", "field": task.TaskKey,
			},
			"action": map[bool]string{
				true: "reset_materialization", false: "observe_only",
			}[canRepair],
			"attempt_count": task.AttemptCount,
		})
		event, eventErr := w.h.Queries.CreateWorkflowEvent(
			ctx,
			db.CreateWorkflowEventParams{
				WorkspaceID:            task.WorkspaceID,
				WorkflowInstanceID:     task.WorkflowInstanceID,
				WorkflowNodeInstanceID: task.WorkflowNodeInstanceID,
				EventType:              "workflow.sweeper_anomaly",
				ActorType:              "system",
				IdempotencyKey: fmt.Sprintf(
					"sweeper:stale:%s:%d",
					uuidToString(task.ID),
					task.AttemptCount,
				),
				Payload: payload,
			},
		)
		if eventErr == nil && event.ID.Valid {
			instance, instanceErr := w.h.Queries.GetWorkflowInstanceInWorkspace(
				ctx,
				db.GetWorkflowInstanceInWorkspaceParams{
					ID:          task.WorkflowInstanceID,
					WorkspaceID: task.WorkspaceID,
				},
			)
			if instanceErr == nil {
				w.h.notifyWorkflowIntervention(
					ctx,
					instance,
					node,
					workflowWaitingReasonsForNotification(reasons),
				)
			}
		}
	}
	return nil
}

func (w *WorkflowSweeper) WaitWithTimeout(timeout time.Duration) bool {
	if w == nil {
		return true
	}
	return waitWorkflowWorker(w.done, timeout)
}

func waitWorkflowWorker(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func decodeWorkflowWaitingReasons(raw []byte) []workflowdomain.WaitingReason {
	var reasons []workflowdomain.WaitingReason
	if json.Unmarshal(raw, &reasons) != nil {
		return nil
	}
	return reasons
}

func appendWorkflowWaitingReason(
	reasons []workflowdomain.WaitingReason,
	reason workflowdomain.WaitingReason,
) []workflowdomain.WaitingReason {
	for _, existing := range reasons {
		if existing.Code == reason.Code && existing.Field == reason.Field {
			return reasons
		}
	}
	return append(reasons, reason)
}

func workflowWaitingReasonsEqual(
	raw []byte,
	reasons []workflowdomain.WaitingReason,
) bool {
	current := decodeWorkflowWaitingReasons(raw)
	return slices.EqualFunc(current, reasons, func(a, b workflowdomain.WaitingReason) bool {
		return a == b
	})
}
