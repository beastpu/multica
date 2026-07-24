package handler

import (
	"context"
	"fmt"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// updateManagedWorkflowHostStatus is the internal Issue status boundary for
// Workflow-owned lifecycle changes. It publishes the same issue:updated shape
// as the HTTP update path and records the actor as system; Workflow runtime
// code must not update the host row directly.
func (h *Handler) updateManagedWorkflowHostStatus(
	ctx context.Context,
	instance db.WorkflowInstance,
	status string,
) error {
	if instance.HostStatusMode != "managed" {
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
