package handler

import (
	"context"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func workflowInstanceStatusIsActive(status string) bool {
	switch status {
	case "running", "needs_setup", "paused":
		return true
	default:
		return false
	}
}

func (h *Handler) recordWorkflowStarted(
	ctx context.Context,
	entryPoint string,
	instance db.WorkflowInstance,
	activeNodes []db.WorkflowNodeInstance,
	missingRoleCount int,
) {
	h.Metrics.RecordWorkflowInstance("started")
	h.Metrics.RecordWorkflowAdoption(entryPoint)
	if workflowInstanceStatusIsActive(instance.Status) {
		h.Metrics.RecordWorkflowActiveRun(instance.Status, 1)
	} else if instance.Status == "completed" {
		h.Metrics.RecordWorkflowInstance("completed")
	}
	if missingRoleCount > 0 {
		h.Metrics.RecordWorkflowOperation("role_resolution", "failed")
	} else {
		h.Metrics.RecordWorkflowOperation("role_resolution", "resolved")
	}
	h.recordWorkflowNodesActivated(ctx, activeNodes)
}

func (h *Handler) recordWorkflowInstanceStatusTransition(previous, current string) {
	if previous == current {
		return
	}
	if workflowInstanceStatusIsActive(previous) {
		h.Metrics.RecordWorkflowActiveRun(previous, -1)
	}
	if workflowInstanceStatusIsActive(current) {
		h.Metrics.RecordWorkflowActiveRun(current, 1)
	}
	h.Metrics.RecordWorkflowInstance(current)
}

func (h *Handler) recordWorkflowNodesActivated(
	ctx context.Context,
	nodes []db.WorkflowNodeInstance,
) {
	for _, node := range nodes {
		activatedAt := time.Time{}
		if node.ActivatedAt.Valid {
			activatedAt = node.ActivatedAt.Time
		}
		h.Metrics.RecordWorkflowNode("activated", activatedAt)
		resolutions, err := h.Queries.ListWorkflowExecutorResolutions(
			ctx,
			db.ListWorkflowExecutorResolutionsParams{
				WorkflowNodeInstanceID: node.ID,
				WorkspaceID:            node.WorkspaceID,
			},
		)
		if err != nil {
			continue
		}
		for _, resolution := range resolutions {
			h.Metrics.RecordWorkflowExecutorResolution(
				resolution.Strategy,
				resolution.Status,
			)
		}
	}
}

func (h *Handler) recordWorkflowNodeTransition(
	node db.WorkflowNodeInstance,
	event string,
) {
	activatedAt := time.Time{}
	if node.ActivatedAt.Valid {
		activatedAt = node.ActivatedAt.Time
	}
	h.Metrics.RecordWorkflowNode(event, activatedAt)
}
