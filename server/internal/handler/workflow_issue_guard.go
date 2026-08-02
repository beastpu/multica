package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

// cleanupWorkflowRelationshipsForIssue is called inside the same transaction
// that deletes the issue. Runtime relationships intentionally have no foreign
// keys, so every reference is detached or deleted explicitly. Cancelled tasks
// are returned for post-commit status reconciliation and realtime fanout.
func cleanupWorkflowRelationshipsForIssue(
	ctx context.Context,
	q *db.Queries,
	issue db.Issue,
) ([]db.AgentTaskQueue, error) {
	cancelled, err := q.CancelAgentTasksByWorkflowHost(
		ctx,
		db.CancelAgentTasksByWorkflowHostParams{
			HostIssueID: issue.ID,
			WorkspaceID: issue.WorkspaceID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("cancel workflow agent tasks: %w", err)
	}
	if err := q.DetachAgentTasksByWorkflowHost(
		ctx,
		db.DetachAgentTasksByWorkflowHostParams{
			HostIssueID: issue.ID,
			WorkspaceID: issue.WorkspaceID,
		},
	); err != nil {
		return nil, fmt.Errorf("detach workflow agent tasks: %w", err)
	}
	if err := q.DetachWorkflowNodeTasksByIssue(ctx, db.DetachWorkflowNodeTasksByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		return nil, fmt.Errorf("detach workflow task from issue: %w", err)
	}

	hostParams := db.DetachWorkflowIssuesByHostParams{
		HostIssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}
	if err := q.DetachWorkflowIssuesByHost(ctx, hostParams); err != nil {
		return nil, fmt.Errorf("detach workflow issues from host: %w", err)
	}
	if err := q.DeleteWorkflowEventsByHost(ctx, db.DeleteWorkflowEventsByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return nil, fmt.Errorf("delete workflow events: %w", err)
	}
	if err := q.DeleteWorkflowAcceptancesByHost(ctx, db.DeleteWorkflowAcceptancesByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return nil, fmt.Errorf("delete workflow acceptances: %w", err)
	}
	if err := q.DeleteWorkflowVerdictsByHost(ctx, db.DeleteWorkflowVerdictsByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return nil, fmt.Errorf("delete workflow verdicts: %w", err)
	}
	if err := q.DeleteWorkflowSubmissionsByHost(ctx, db.DeleteWorkflowSubmissionsByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return nil, fmt.Errorf("delete workflow submissions: %w", err)
	}
	if err := q.DeleteWorkflowExecutorResolutionsByHost(ctx, db.DeleteWorkflowExecutorResolutionsByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return nil, fmt.Errorf("delete workflow executor resolutions: %w", err)
	}
	if err := q.DeleteWorkflowNodeTasksByHost(ctx, db.DeleteWorkflowNodeTasksByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return nil, fmt.Errorf("delete workflow node tasks: %w", err)
	}
	if err := q.DeleteWorkflowNodeParticipantsByHost(ctx, db.DeleteWorkflowNodeParticipantsByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return nil, fmt.Errorf("delete workflow node participants: %w", err)
	}
	// Artifacts are keyed by instance, so they have to go before the instances
	// do — nothing else would be able to find them afterwards.
	if err := q.DeleteWorkflowArtifactsByHost(ctx, db.DeleteWorkflowArtifactsByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return nil, fmt.Errorf("delete workflow artifacts: %w", err)
	}
	if err := q.DeleteWorkflowNodesByHost(ctx, db.DeleteWorkflowNodesByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return nil, fmt.Errorf("delete workflow nodes: %w", err)
	}
	if err := q.DeleteWorkflowRoleAssignmentsByHost(ctx, db.DeleteWorkflowRoleAssignmentsByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return nil, fmt.Errorf("delete workflow role assignments: %w", err)
	}
	if err := q.DeleteWorkflowInstancesByHost(ctx, db.DeleteWorkflowInstancesByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return nil, fmt.Errorf("delete workflow instances: %w", err)
	}
	return cancelled, nil
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
