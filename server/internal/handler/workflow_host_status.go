package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// updateManagedWorkflowHostStatus applies the implicit workflow lifecycle to
// the host issue and is a no-op unless the instance opted into managed mode.
func (h *Handler) updateManagedWorkflowHostStatus(
	ctx context.Context,
	instance db.WorkflowInstance,
	status string,
) error {
	if instance.HostStatusMode != "managed" || !instance.HostIssueID.Valid {
		return nil
	}
	if !h.managedHostWriteAllowed(ctx, instance) {
		return nil
	}
	return h.updateWorkflowHostStatus(ctx, instance, status)
}

// managedHostWriteAllowed asks whether this run still speaks for its host.
//
// An issue accumulates runs, and each managed run maintained the issue as if
// it were the only one — which oscillates rather than merely disagreeing. The
// reconciler claims a completed run whenever its host is not 'done' and a
// running one whenever its host is not 'in_progress', so a new run on an issue
// whose previous run finished leaves the two rewriting the same field on every
// pass. Observed on WTE-14841 with five managed runs: the host flipped to
// in_progress at start and back to done five minutes later, with no node
// having completed in between.
//
// A lookup failure allows the write. The alternative is a run unable to report
// its own state because a query failed, which is a worse silence than a stale
// status.
func (h *Handler) managedHostWriteAllowed(
	ctx context.Context,
	instance db.WorkflowInstance,
) bool {
	writerIsLive := instance.Status == "running" ||
		instance.Status == "needs_setup" ||
		instance.Status == "paused"

	live, err := h.Queries.GetActiveWorkflowInstanceByHost(
		ctx,
		db.GetActiveWorkflowInstanceByHostParams{
			HostIssueID: instance.HostIssueID, WorkspaceID: instance.WorkspaceID,
		},
	)
	liveExists := err == nil && live.ID.Valid
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("managed host status: could not check for a live run",
			"workflow_instance_id", uuidToString(instance.ID),
			"host_issue_id", uuidToString(instance.HostIssueID),
			"error", err)
		return true
	}

	// The live run found may be this one, which is the ordinary case for a run
	// reporting its own progress.
	if liveExists && live.ID == instance.ID {
		writerIsLive = true
	}
	return workflowdomain.ManagedHostStatusWriteAllowed(liveExists, writerIsLive)
}

// updateWorkflowHostStatus is the internal Issue status boundary for
// Workflow-owned changes. It publishes the same issue:updated shape as the
// HTTP update path and records the actor as system; Workflow runtime code
// must not update the host row directly.
func (h *Handler) updateWorkflowHostStatus(
	ctx context.Context,
	instance db.WorkflowInstance,
	status string,
) error {
	if !instance.HostIssueID.Valid {
		return nil
	}
	previous, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID: instance.HostIssueID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		return fmt.Errorf("load managed workflow host: %w", err)
	}
	if previous.Status == status {
		return nil
	}
	updated, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID: previous.ID, Status: status, WorkspaceID: previous.WorkspaceID,
	})
	if err != nil {
		return fmt.Errorf("update managed workflow host status: %w", err)
	}
	prefix := h.getIssuePrefix(ctx, updated.WorkspaceID)
	h.publish(
		protocol.EventIssueUpdated,
		uuidToString(updated.WorkspaceID),
		"system",
		"",
		map[string]any{
			"issue":               issueToResponse(updated, prefix),
			"assignee_changed":    false,
			"status_changed":      true,
			"priority_changed":    false,
			"project_changed":     false,
			"start_date_changed":  false,
			"due_date_changed":    false,
			"description_changed": false,
			"title_changed":       false,
			"prev_title":          previous.Title,
			"prev_assignee_type":  textToPtr(previous.AssigneeType),
			"prev_assignee_id":    uuidToPtr(previous.AssigneeID),
			"prev_status":         previous.Status,
			"prev_priority":       previous.Priority,
			"prev_start_date":     dateToPtr(previous.StartDate),
			"prev_due_date":       dateToPtr(previous.DueDate),
			"prev_description":    textToPtr(previous.Description),
			"creator_type":        previous.CreatorType,
			"creator_id":          uuidToString(previous.CreatorID),
			"source":              "workflow",
		},
	)
	if status == "done" {
		h.notifyParentOfChildDone(ctx, previous, updated)
	}
	return nil
}

// workflowCarrierSync defers a carrier update to after the transaction that
// caused it commits. The transition is the thing that must be durable; the
// mirror follows it, and running the writes inside would hold the lock for the
// length of however many issues a node happens to carry.
type workflowCarrierSync struct {
	node  db.WorkflowNodeInstance
	event string
}

// syncWorkflowNodeIssueStatus pushes a node's state onto the issues that carry
// its work. The issue is where the work happens; the node is the record of
// whether it finished. Leaving the two to drift meant an issue sat in todo
// while an agent worked it and stayed in progress after the node was done —
// a board describing a run that had moved on without it.
//
// Best effort, like the node's other side effects: the transition is already
// durable, and a failed mirror must not undo it. A failure is logged rather
// than swallowed, because a carrier showing the wrong state is exactly the
// kind of thing nobody notices until they are debugging something else.
func (h *Handler) syncWorkflowNodeIssueStatus(
	ctx context.Context,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
	event string,
) {
	tasks, err := h.Queries.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		slog.Warn("workflow node issue sync could not list tasks",
			"workflow_node_instance_id", uuidToString(node.ID),
			"event", event, "error", err)
		return
	}
	for _, task := range tasks {
		if !task.IssueID.Valid {
			continue
		}
		previous, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
			ID: task.IssueID, WorkspaceID: workspaceID,
		})
		if err != nil {
			continue
		}
		target, ok := workflowdomain.NodeIssueStatus(event, previous.Status)
		if !ok || target == previous.Status {
			continue
		}
		updated, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
			ID: previous.ID, Status: target, WorkspaceID: workspaceID,
		})
		if err != nil {
			slog.Warn("workflow node issue sync could not set the status",
				"issue_id", uuidToString(previous.ID),
				"event", event, "target", target, "error", err)
			continue
		}
		h.publishWorkflowNodeIssueStatus(ctx, previous, updated)
	}
}

func (h *Handler) publishWorkflowNodeIssueStatus(
	ctx context.Context,
	previous, updated db.Issue,
) {
	prefix := h.getIssuePrefix(ctx, updated.WorkspaceID)
	h.publish(
		protocol.EventIssueUpdated,
		uuidToString(updated.WorkspaceID),
		"system",
		"",
		map[string]any{
			"issue":               issueToResponse(updated, prefix),
			"assignee_changed":    false,
			"status_changed":      true,
			"priority_changed":    false,
			"project_changed":     false,
			"start_date_changed":  false,
			"due_date_changed":    false,
			"description_changed": false,
			"title_changed":       false,
			"prev_title":          previous.Title,
			"prev_assignee_type":  textToPtr(previous.AssigneeType),
			"prev_assignee_id":    uuidToPtr(previous.AssigneeID),
			"prev_status":         previous.Status,
			"prev_priority":       previous.Priority,
			"prev_start_date":     dateToPtr(previous.StartDate),
			"prev_due_date":       dateToPtr(previous.DueDate),
			"prev_description":    textToPtr(previous.Description),
			"creator_type":        previous.CreatorType,
			"creator_id":          uuidToString(previous.CreatorID),
			"source":              "workflow",
		},
	)
}

// cancelWorkflowNodeCarriers closes the issues that carried a cancelled run's
// work. Cancelling the run already cancelled its nodes; without this the
// carriers stay open, and an issue nobody will ever work sits on a board
// looking like something somebody should.
//
// Terminal carriers are left alone by the mapping, so work finished before the
// cancellation stays finished.
func (h *Handler) cancelWorkflowNodeCarriers(
	ctx context.Context,
	instance db.WorkflowInstance,
) {
	nodes, err := h.Queries.ListWorkflowNodeInstances(ctx, db.ListWorkflowNodeInstancesParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		slog.Warn("workflow cancellation could not list nodes to cancel their carriers",
			"workflow_instance_id", uuidToString(instance.ID), "error", err)
		return
	}
	for _, node := range nodes {
		h.syncWorkflowNodeIssueStatus(ctx, instance.WorkspaceID, node, "cancelled")
	}
}

// syncWorkflowNodeIssueStatusTx is the in-transaction half of the carrier
// mirror, for transitions the graph propagation performs while it holds the
// transaction. Writing the carrier here keeps it atomic with the transition
// that caused it: the two cannot disagree because a later step failed.
//
// It does not publish. The realtime update for these paths rides on the
// workflow event the same transaction writes, which is what the run view
// listens to; emitting a second one from inside the transaction would announce
// a state that a rollback could still take back.
func syncWorkflowNodeIssueStatusTx(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
	event string,
) error {
	tasks, err := q.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if !task.IssueID.Valid {
			continue
		}
		previous, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
			ID: task.IssueID, WorkspaceID: workspaceID,
		})
		if err != nil {
			continue
		}
		target, ok := workflowdomain.NodeIssueStatus(event, previous.Status)
		if !ok || target == previous.Status {
			continue
		}
		if _, err := q.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
			ID: previous.ID, Status: target, WorkspaceID: workspaceID,
		}); err != nil {
			return err
		}
	}
	return nil
}
