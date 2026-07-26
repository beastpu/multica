package handler

import (
	"context"
	"fmt"

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
	if instance.HostStatusMode != "managed" {
		return nil
	}
	return h.updateWorkflowHostStatus(ctx, instance, status)
}

// applyWorkflowNodeActions runs a node's controlled side effects after its
// state transition committed. Explicit template actions apply regardless of
// host_status_mode: the mode governs the implicit lifecycle, not authored
// per-node instructions. Failures are best-effort by design — the node
// transition itself is already durable.
func (h *Handler) applyWorkflowNodeActions(
	ctx context.Context,
	instance db.WorkflowInstance,
	actions []workflowdomain.NodeActionDefinition,
) {
	for _, action := range actions {
		if action.Kind == "set_host_status" {
			_ = h.updateWorkflowHostStatus(ctx, instance, action.Status)
		}
	}
}

// applyWorkflowNodeEnterActions applies on_enter actions for freshly
// activated activity nodes.
func (h *Handler) applyWorkflowNodeEnterActions(
	ctx context.Context,
	instance db.WorkflowInstance,
	definition workflowdomain.Definition,
	nodes []db.WorkflowNodeInstance,
) {
	byKey := make(map[string]workflowdomain.NodeDefinition, len(definition.Nodes))
	for _, node := range definition.Nodes {
		byKey[node.Key] = node
	}
	for _, node := range nodes {
		if nodeDefinition, ok := byKey[node.NodeKey]; ok {
			h.applyWorkflowNodeActions(ctx, instance, nodeDefinition.OnEnter)
		}
	}
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
