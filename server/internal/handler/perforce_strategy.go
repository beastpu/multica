package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/perforce"
	"github.com/multica-ai/multica/server/pkg/protocol"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// perforceStrategyCreatePerEvent creates one Multica issue per distinct Swarm
// review event, assigned to the project's configured default agent. It is the
// bespoke flow for projects that do not reference existing issues in their
// review descriptions — every event spawns a fresh issue.
const perforceStrategyCreatePerEvent = "create_per_event"

// perforceReviewStrategyFunc is one registered capability. The registry below
// is the ONLY place a strategy name maps to behaviour — the code provides
// capabilities, and the perforce_project_strategy row (data) picks which one a
// given project uses.
type perforceReviewStrategyFunc func(*Handler, context.Context, db.PerforceProjectStrategy, perforce.Review) (string, error)

var perforceReviewStrategies = map[string]perforceReviewStrategyFunc{
	perforceStrategyCreatePerEvent: (*Handler).createIssuePerEvent,
}

// dispatchPerforceStrategy routes a review event to a per-project strategy when
// one is configured for its Swarm URL. Returns handled=false when no strategy
// applies, so the caller falls through to the default link-to-existing-issue
// path. The status string is echoed back to Swarm ("processed" / "duplicate" /
// "ignored").
func (h *Handler) dispatchPerforceStrategy(ctx context.Context, swarmURL string, review perforce.Review) (handled bool, status string, err error) {
	strat, err := h.Queries.GetPerforceProjectStrategyBySwarmURL(ctx, swarmURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}

	fn, ok := perforceReviewStrategies[strat.Strategy]
	if !ok {
		// A configured strategy name with no matching capability (e.g. a value
		// written by a newer server, then rolled back). Downgrade to ignored
		// rather than 500 — enum drift must not crash ingestion.
		slog.Warn("perforce: unknown project strategy, ignoring",
			"strategy", strat.Strategy, "swarm_url", swarmURL)
		return true, "ignored", nil
	}

	status, err = fn(h, ctx, strat, review)
	if err != nil {
		return true, "", err
	}
	return true, status, nil
}

// createIssuePerEvent implements perforceStrategyCreatePerEvent. Idempotency is
// enforced by perforce_strategy_created_issue's UNIQUE(strategy_id, review_id,
// review_updated_at): a webhook retry carries an identical review_updated_at and
// is a no-op; a genuinely newer event has a fresh key and creates another issue.
func (h *Handler) createIssuePerEvent(ctx context.Context, strat db.PerforceProjectStrategy, review perforce.Review) (string, error) {
	// Fast-path the common retry before opening a transaction.
	_, err := h.Queries.GetPerforceStrategyCreatedIssue(ctx, db.GetPerforceStrategyCreatedIssueParams{
		StrategyID:      strat.ID,
		ReviewID:        review.ID,
		ReviewUpdatedAt: pgTimestamptz(review.UpdatedAt),
	})
	switch {
	case err == nil:
		return "duplicate", nil
	case errors.Is(err, pgx.ErrNoRows):
		// first time for this (review_id, updated) — proceed
	default:
		return "", fmt.Errorf("lookup created issue: %w", err)
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	number, err := qtx.IncrementIssueCounter(ctx, strat.WorkspaceID)
	if err != nil {
		return "", fmt.Errorf("increment issue counter: %w", err)
	}

	issue, err := qtx.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID:  strat.WorkspaceID,
		Title:        perforceStrategyIssueTitle(review),
		Description:  pgtype.Text{String: perforceStrategyIssueDescription(review), Valid: true},
		Status:       "todo",
		Priority:     "none",
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   strat.DefaultAssigneeID,
		// Creator is the assigned agent (like the autopilot create_issue path)
		// so activity/mentions render with the right author identity.
		CreatorType: "agent",
		CreatorID:   strat.DefaultAssigneeID,
		Position:    0,
		Number:      number,
	})
	if err != nil {
		return "", fmt.Errorf("create issue: %w", err)
	}

	// The ledger insert is the idempotency gate. ON CONFLICT DO NOTHING → no
	// row back means a concurrent identical delivery won the race; roll back
	// this issue (deferred Rollback) and report duplicate.
	if _, err := qtx.RecordPerforceStrategyCreatedIssue(ctx, db.RecordPerforceStrategyCreatedIssueParams{
		StrategyID:      strat.ID,
		ReviewID:        review.ID,
		ReviewUpdatedAt: pgTimestamptz(review.UpdatedAt),
		IssueID:         issue.ID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "duplicate", nil
		}
		return "", fmt.Errorf("record created issue: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit tx: %w", err)
	}

	// Post-commit side effects. The issue is durable; a hiccup here must not
	// fail the webhook (mirrors the autopilot create_issue path).
	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)
	h.publish(protocol.EventIssueCreated, uuidToString(strat.WorkspaceID), "agent", uuidToString(strat.DefaultAssigneeID), map[string]any{
		"issue": issueToResponse(issue, prefix),
	})
	if h.shouldEnqueueAgentTask(ctx, issue) {
		if _, err := h.TaskService.EnqueueTaskForIssue(ctx, issue); err != nil {
			slog.Warn("perforce strategy: enqueue task failed",
				"issue_id", uuidToString(issue.ID), "error", err)
		}
	}
	return "processed", nil
}

// perforceReviewChangelist returns the changelist number to surface — the
// committed CL once submitted, otherwise the shelved CL under review.
func perforceReviewChangelist(review perforce.Review) string {
	if review.CommittedCL != nil {
		return strconv.FormatInt(*review.CommittedCL, 10)
	}
	if review.ShelvedCL != nil {
		return strconv.FormatInt(*review.ShelvedCL, 10)
	}
	return ""
}

// perforceStrategyIssueTitle prefers the Swarm review title, falling back to a
// changelist/author summary so the issue is never blank-titled.
func perforceStrategyIssueTitle(review perforce.Review) string {
	if t := strings.TrimSpace(review.Title); t != "" {
		return t
	}
	cl := perforceReviewChangelist(review)
	switch {
	case cl != "" && review.Author != "":
		return fmt.Sprintf("CL %s by %s", cl, review.Author)
	case cl != "":
		return "CL " + cl
	default:
		return fmt.Sprintf("Perforce review #%d", review.ID)
	}
}

// perforceStrategyIssueDescription carries the changelist and author into the
// issue body (the agreed zero-fidelity requirement), plus review state and the
// original review description for context.
func perforceStrategyIssueDescription(review perforce.Review) string {
	var b strings.Builder
	b.WriteString("Created from a Perforce / Helix Swarm review event.\n\n")
	if cl := perforceReviewChangelist(review); cl != "" {
		b.WriteString("- Changelist: ")
		b.WriteString(cl)
		b.WriteString("\n")
	}
	if review.Author != "" {
		b.WriteString("- Author: ")
		b.WriteString(review.Author)
		b.WriteString("\n")
	}
	if review.State != "" {
		b.WriteString("- State: ")
		b.WriteString(review.State)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "- Review: #%d\n", review.ID)
	if d := strings.TrimSpace(review.Description); d != "" {
		b.WriteString("\n---\n")
		b.WriteString(d)
	}
	return b.String()
}
