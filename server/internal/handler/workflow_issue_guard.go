package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	activeWorkflowIssueDeleteMessage   = "cannot delete a required issue from an active workflow; detach it from the workflow first"
	activeWorkflowIssueReparentMessage = "cannot change the parent of an issue bound to an active workflow; detach it from the workflow first"
)

func (h *Handler) rejectActiveWorkflowIssueDelete(w http.ResponseWriter, r *http.Request, issue db.Issue) bool {
	binding, err := h.Queries.GetActiveWorkflowIssueBinding(r.Context(), db.GetActiveWorkflowIssueBindingParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate workflow issue binding")
		return true
	}
	if binding.Required {
		writeError(w, http.StatusConflict, activeWorkflowIssueDeleteMessage)
		return true
	}
	return false
}

func (h *Handler) rejectActiveWorkflowIssueReparent(w http.ResponseWriter, r *http.Request, issue db.Issue) bool {
	_, err := h.Queries.GetActiveWorkflowIssueBinding(r.Context(), db.GetActiveWorkflowIssueBindingParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate workflow issue binding")
		return true
	}
	writeError(w, http.StatusConflict, activeWorkflowIssueReparentMessage)
	return true
}

func (h *Handler) publishWorkflowIssueDeleteResult(
	ctx context.Context,
	actorType string,
	actorID string,
	result service.WorkflowIssueDeleteResult,
) {
	h.TaskService.BroadcastCancelledTasks(ctx, result.CancelledTasks)
	for _, instance := range result.AffectedRuns {
		h.publishWorkflowInstanceUpdated(
			uuidToString(instance.WorkspaceID), actorType, actorID,
			uuidToString(instance.ID), "",
		)
	}
}

func parentIssueIDChanged(current pgtype.UUID, requested *string) bool {
	if requested == nil {
		return current.Valid
	}
	parsed, err := parseUUIDValue(*requested)
	if err != nil {
		// The regular request validator will emit the more useful 400.
		return false
	}
	return !current.Valid || current != parsed
}

func parseUUIDValue(value string) (pgtype.UUID, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}
	return parsed, nil
}
