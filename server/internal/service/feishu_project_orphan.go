package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	// How often the orphan reconcile sweep runs per integration. Orphan
	// detection is not time-sensitive (a Feishu work item that was deleted
	// stays deleted), so this is sparser than the 5m incremental sync.
	feishuProjectOrphanReconcileInterval = 30 * time.Minute
	// Max work item ids per /work_item/filter probe. Must stay <= the filter
	// page size (100) so a batch always fits one page — otherwise a paginated
	// response could omit a live item and we'd false-flag it as an orphan.
	feishuProjectOrphanBatchSize = 50
	// Bindings pulled per keyset page when sweeping a large integration.
	feishuProjectOrphanBindingPageSize = 500
)

// FeishuProjectOrphanReconcileInterval is the cadence at which the orphan
// reconcile sweep is expected to run. Exposed for the cmd-level scheduler.
func FeishuProjectOrphanReconcileInterval() time.Duration {
	return feishuProjectOrphanReconcileInterval
}

// IssueTaskCanceller cancels in-progress agent tasks for an issue about to be
// deleted. *TaskService and FeishuProjectTaskService both satisfy it.
type IssueTaskCanceller interface {
	CancelTasksForIssue(ctx context.Context, issueID pgtype.UUID) error
}

// IssueAttachmentDeleter removes an issue's attachment blobs from object
// storage. storage.Storage and FeishuProjectStorage both satisfy it.
type IssueAttachmentDeleter interface {
	KeyFromURL(rawURL string) string
	DeleteKeys(ctx context.Context, keys []string)
}

// HardDeleteIssueOptions lets callers extend the issue deletion transaction
// with additional application-owned relationship cleanup.
type HardDeleteIssueOptions struct {
	TxStarter TxStarter
	// WithinDeleteTransaction runs immediately before the issue row is
	// deleted. It must only use qtx and must not publish events or perform
	// external side effects.
	WithinDeleteTransaction func(ctx context.Context, qtx *db.Queries, issue db.Issue) error
}

// HardDeleteIssue permanently deletes an issue and its issue-owned dependents.
// Workflow runs are preserved as audit history: active runs are cancelled and
// every run is detached from the deleted host before the issue row is removed.
// It also cancels issue tasks, fails linked autopilot runs, cleans up attachment
// blobs, and publishes issue:deleted so connected clients drop the issue.
//
// tasks, store, and pub may be nil — the corresponding step is skipped. Issue
// task cancellation and autopilot failure are best-effort (matching the
// handler); workflow preservation and issue deletion are transactional when a
// TxStarter is provided. The result contains post-commit workflow fanout data.
func HardDeleteIssue(
	ctx context.Context,
	q *db.Queries,
	tasks IssueTaskCanceller,
	store IssueAttachmentDeleter,
	pub FeishuProjectEventPublisher,
	issue db.Issue,
	actorType, actorID string,
	options ...HardDeleteIssueOptions,
) (WorkflowIssueDeleteResult, error) {
	var workflowResult WorkflowIssueDeleteResult
	if tasks != nil {
		_ = tasks.CancelTasksForIssue(ctx, issue.ID)
	}
	var actorUUID pgtype.UUID
	if actorID != "" {
		if err := actorUUID.Scan(actorID); err != nil {
			return workflowResult, fmt.Errorf("parse issue delete actor: %w", err)
		}
	}

	var opts HardDeleteIssueOptions
	if len(options) > 0 {
		opts = options[0]
	}
	preserveWorkflow := func(ctx context.Context, qtx *db.Queries, issue db.Issue) error {
		var err error
		workflowResult, err = PreserveWorkflowHistoryForIssue(
			ctx, qtx, issue, actorType, actorUUID,
		)
		if err != nil {
			return err
		}
		if opts.WithinDeleteTransaction != nil {
			return opts.WithinDeleteTransaction(ctx, qtx, issue)
		}
		return nil
	}

	var attachmentURLs []string
	if opts.TxStarter != nil {
		tx, err := opts.TxStarter.Begin(ctx)
		if err != nil {
			return workflowResult, fmt.Errorf("begin issue delete transaction: %w", err)
		}
		defer tx.Rollback(ctx)
		qtx := q.WithTx(tx)
		attachmentURLs, err = hardDeleteIssueRecords(ctx, qtx, issue, preserveWorkflow)
		if err != nil {
			return workflowResult, err
		}
		if err := tx.Commit(ctx); err != nil {
			return workflowResult, fmt.Errorf("commit issue delete transaction: %w", err)
		}
	} else {
		var err error
		attachmentURLs, err = hardDeleteIssueRecords(ctx, q, issue, preserveWorkflow)
		if err != nil {
			return workflowResult, err
		}
	}

	if store != nil && len(attachmentURLs) > 0 {
		keys := make([]string, len(attachmentURLs))
		for i, u := range attachmentURLs {
			keys[i] = store.KeyFromURL(u)
		}
		store.DeleteKeys(ctx, keys)
	}

	if pub != nil {
		pub.Publish(events.Event{
			Type:        protocol.EventIssueDeleted,
			WorkspaceID: UUIDString(issue.WorkspaceID),
			ActorType:   actorType,
			ActorID:     actorID,
			Payload:     map[string]any{"issue_id": UUIDString(issue.ID)},
		})
	}
	return workflowResult, nil
}

func hardDeleteIssueRecords(
	ctx context.Context,
	q *db.Queries,
	issue db.Issue,
	withinDeleteTransaction func(context.Context, *db.Queries, db.Issue) error,
) ([]string, error) {
	if withinDeleteTransaction != nil {
		if err := withinDeleteTransaction(ctx, q, issue); err != nil {
			return nil, fmt.Errorf("extend issue delete transaction: %w", err)
		}
	}

	// Fail any linked autopilot runs before delete (ON DELETE SET NULL clears
	// issue_id). Best-effort, mirroring the original HTTP handler behavior.
	_ = q.FailAutopilotRunsByIssue(ctx, issue.ID)

	// Collect attachment URLs (issue-level + comment-level) before the issue
	// delete removes the rows.
	attachmentURLs, _ := q.ListAttachmentURLsByIssueOrComments(ctx, issue.ID)
	if err := q.DeleteIssue(ctx, db.DeleteIssueParams{
		ID:          issue.ID,
		WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		return nil, err
	}
	return attachmentURLs, nil
}

// detectOrphanBindings asks Feishu Project which of the given bindings' work
// items still exist and returns the bindings whose work item is gone (deleted
// or archived). It queries by work_item_ids in batches, bypassing status and
// updated_at filters so "absent from the response" means "no longer exists",
// not "filtered out".
//
// If ANY batch probe fails it returns the error and no orphans — a transient
// Feishu/network error must never be read as "all these issues were deleted".
func (s *FeishuProjectSyncService) detectOrphanBindings(ctx context.Context, cfg db.FeishuProjectIntegration, bindings []db.FeishuProjectIssueBinding) ([]db.FeishuProjectIssueBinding, error) {
	byType := map[string][]db.FeishuProjectIssueBinding{}
	for _, b := range bindings {
		byType[b.WorkItemType] = append(byType[b.WorkItemType], b)
	}

	var orphans []db.FeishuProjectIssueBinding
	for typ, group := range byType {
		for start := 0; start < len(group); start += feishuProjectOrphanBatchSize {
			end := start + feishuProjectOrphanBatchSize
			if end > len(group) {
				end = len(group)
			}
			batch := group[start:end]
			ids := make([]string, len(batch))
			for i, b := range batch {
				ids[i] = b.WorkItemID
			}

			present := make(map[string]bool, len(batch))
			err := s.Client.QueryWorkItemPagesWithOptions(ctx, cfg, typ, false, FeishuProjectSyncOptions{WorkItemIDs: ids}, func(page FeishuProjectWorkItemPage) error {
				for _, item := range page.Items {
					present[item.ID] = true
				}
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("feishu orphan probe failed (type=%s): %w", typ, err)
			}

			for _, b := range batch {
				if !present[b.WorkItemID] {
					orphans = append(orphans, b)
				}
			}
		}
	}
	return orphans, nil
}

// ReconcileOrphans sweeps every binding of an integration and hard-deletes the
// Multica issue of any work item that no longer exists in Feishu Project. A
// failed probe aborts the run without deleting anything; the next sweep retries.
func (s *FeishuProjectSyncService) ReconcileOrphans(ctx context.Context, cfg db.FeishuProjectIntegration) error {
	if s.Client == nil {
		s.Client = NewFeishuProjectClient()
	}

	// Keyset cursor starts at the all-zero UUID (Valid:true so the comparison
	// isn't against SQL NULL).
	cursor := pgtype.UUID{Valid: true}
	deleted, failed := 0, 0
	for {
		bindings, err := s.Queries.ListFeishuProjectIssueBindingsByIntegration(ctx, db.ListFeishuProjectIssueBindingsByIntegrationParams{
			IntegrationID: cfg.ID,
			ID:            cursor,
			Limit:         feishuProjectOrphanBindingPageSize,
		})
		if err != nil {
			return fmt.Errorf("list feishu bindings: %w", err)
		}
		if len(bindings) == 0 {
			break
		}

		orphans, err := s.detectOrphanBindings(ctx, cfg, bindings)
		if err != nil {
			return err
		}
		for _, b := range orphans {
			workflowResult, err := HardDeleteIssue(
				ctx, s.Queries, s.TaskService, s.Storage, s.Events,
				db.Issue{ID: b.IssueID, WorkspaceID: b.WorkspaceID}, "system", "",
				HardDeleteIssueOptions{TxStarter: s.Tx},
			)
			if err != nil {
				slog.Warn("Feishu Project orphan delete failed", "integration_id", UUIDString(cfg.ID), "issue_id", UUIDString(b.IssueID), "work_item_id", b.WorkItemID, "error", err)
				failed++
				continue
			}
			publishWorkflowIssueDeleteResult(
				ctx, s.TaskService, s.Events, "system", "", workflowResult,
			)
			deleted++
		}

		cursor = bindings[len(bindings)-1].ID
		if len(bindings) < feishuProjectOrphanBindingPageSize {
			break
		}
	}

	if deleted > 0 || failed > 0 {
		slog.Info("Feishu Project orphan reconcile", "integration_id", UUIDString(cfg.ID), "deleted", deleted, "failed", failed)
	}
	return nil
}
