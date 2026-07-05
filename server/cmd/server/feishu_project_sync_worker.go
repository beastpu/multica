package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const feishuProjectSyncInterval = 5 * time.Minute

// p4AssessmentWorkspaceAllowlistEnv names the env var holding a comma-separated
// list of workspace UUIDs permitted to auto-trigger P4 assessment on Feishu
// sync. Unset or blank ⇒ the auto-assessment path is off everywhere
// (fail-closed), so a workspace must be opted in explicitly.
const p4AssessmentWorkspaceAllowlistEnv = "P4_ASSESSMENT_WORKSPACE_ALLOWLIST"

// p4AssessmentAllowlistFromEnv parses the workspace allowlist. Entries are
// trimmed and lowercased to match the canonical UUID form the sync path
// carries; blank entries are dropped. Returns nil when unset/blank so the
// downstream Allows() stays fail-closed.
func p4AssessmentAllowlistFromEnv() service.P4AssessmentAllowlist {
	raw := strings.TrimSpace(os.Getenv(p4AssessmentWorkspaceAllowlistEnv))
	if raw == "" {
		return nil
	}
	out := service.P4AssessmentAllowlist{}
	for _, part := range strings.Split(raw, ",") {
		if id := strings.ToLower(strings.TrimSpace(part)); id != "" {
			out[id] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func runFeishuProjectSyncWorker(ctx context.Context, queries *db.Queries, pool *pgxpool.Pool, taskSvc *service.TaskService, bus *events.Bus) {
	store := newStorageFromEnv()
	allowlist := p4AssessmentAllowlistFromEnv()
	if len(allowlist) > 0 {
		slog.Info("P4 assessment auto-trigger allowlist loaded", "workspace_count", len(allowlist))
	} else {
		slog.Info("P4 assessment auto-trigger disabled (empty workspace allowlist)")
	}
	ticker := time.NewTicker(feishuProjectSyncInterval)
	defer ticker.Stop()

	runFeishuProjectSyncOnce(ctx, queries, pool, store, taskSvc, bus, allowlist)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runFeishuProjectSyncOnce(ctx, queries, pool, store, taskSvc, bus, allowlist)
		}
	}
}

// feishuP4AssessmentTrigger resolves the P4 assessment trigger from a
// TaskService, guarding on the concrete *P4AssessmentService pointer rather than
// the interface. Assigning a nil *P4AssessmentService straight into the
// FeishuProjectP4AssessmentTrigger interface would yield a non-nil "typed nil"
// that passes `!= nil` checks and then panics on a nil-receiver method call —
// the startup crash this guards against. Returning a true nil interface keeps
// the downstream backfill/trigger guards honest.
func feishuP4AssessmentTrigger(taskSvc *service.TaskService) service.FeishuProjectP4AssessmentTrigger {
	if taskSvc == nil || taskSvc.P4Assessment == nil {
		return nil
	}
	return taskSvc.P4Assessment
}

func runFeishuProjectSyncOnce(ctx context.Context, queries *db.Queries, pool *pgxpool.Pool, store service.FeishuProjectStorage, taskSvc *service.TaskService, bus *events.Bus, allowlist service.P4AssessmentAllowlist) {
	configs, err := queries.ListEnabledFeishuProjectIntegrations(ctx)
	if err != nil {
		slog.Warn("Feishu Project sync scan failed", "error", err)
		return
	}
	p4Assessment := feishuP4AssessmentTrigger(taskSvc)
	svc := &service.FeishuProjectSyncService{Queries: queries, Tx: pool, Client: service.NewFeishuProjectClient(), Storage: store, TaskService: taskSvc, P4Assessment: p4Assessment, P4AssessmentAllowlist: allowlist, Events: bus}
	now := time.Now()
	for _, cfg := range configs {
		locked, unlock, err := service.TryAcquireFeishuProjectSyncLock(ctx, pool, cfg.ID)
		if err != nil {
			slog.Warn("Feishu Project sync lock failed", "integration_id", service.UUIDString(cfg.ID), "project_key", cfg.ProjectKey, "error", err)
			continue
		}
		if !locked {
			continue
		}
		if _, err := svc.Sync(ctx, cfg, "scheduled"); err != nil {
			slog.Warn("Feishu Project sync failed", "integration_id", service.UUIDString(cfg.ID), "project_key", cfg.ProjectKey, "error", err)
		}
		// Orphan reconcile runs under the same advisory lock so it can't race a
		// concurrent sync that just created a binding. Stamp only on success so
		// a failed probe retries next tick instead of waiting a full interval.
		if feishuProjectOrphanReconcileDue(cfg, now) {
			if err := svc.ReconcileOrphans(ctx, cfg); err != nil {
				slog.Warn("Feishu Project orphan reconcile failed", "integration_id", service.UUIDString(cfg.ID), "project_key", cfg.ProjectKey, "error", err)
			} else if err := queries.MarkFeishuProjectIntegrationOrphanReconciled(ctx, cfg.ID); err != nil {
				slog.Warn("Feishu Project mark orphan-reconciled failed", "integration_id", service.UUIDString(cfg.ID), "error", err)
			}
		}
		// Fail-closed gate (plan C-1): capability role configuration is the
		// authoritative opt-in; the env allowlist is a transitional OR
		// condition (C-2 deletes it together with this env var).
		if p4Assessment != nil && (allowlist.Allows(cfg.WorkspaceID) || service.P4AssessmentCapabilityConfigured(ctx, queries, cfg.WorkspaceID)) {
			result, err := p4Assessment.BackfillDoneBindings(ctx, cfg.WorkspaceID, cfg.ID, service.P4AssessmentBackfillLimit)
			if err != nil {
				slog.Warn("P4 assessment historical binding backfill failed", "integration_id", service.UUIDString(cfg.ID), "project_key", cfg.ProjectKey, "error", err)
			} else if result.Triggered > 0 || result.Failed > 0 {
				slog.Info("P4 assessment historical binding backfill finished", "integration_id", service.UUIDString(cfg.ID), "project_key", cfg.ProjectKey, "scanned", result.Scanned, "eligible", result.Eligible, "triggered", result.Triggered, "skipped", result.Skipped, "failed", result.Failed, "last_reason", result.LastReason)
			}
		}
		unlock()
	}
}

// feishuProjectOrphanReconcileDue reports whether the integration is due for an
// orphan reconcile sweep. A NULL last_orphan_reconciled_at counts as "never",
// forcing a sweep on the first tick after the migration lands.
func feishuProjectOrphanReconcileDue(cfg db.FeishuProjectIntegration, now time.Time) bool {
	return !cfg.LastOrphanReconciledAt.Valid || now.Sub(cfg.LastOrphanReconciledAt.Time) >= service.FeishuProjectOrphanReconcileInterval()
}
