package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const feishuProjectSyncInterval = 5 * time.Minute

func runFeishuProjectSyncWorker(ctx context.Context, queries *db.Queries, pool *pgxpool.Pool, taskSvc *service.TaskService, bus *events.Bus) {
	store := newStorageFromEnv()
	ticker := time.NewTicker(feishuProjectSyncInterval)
	defer ticker.Stop()

	runFeishuProjectSyncOnce(ctx, queries, pool, store, taskSvc, bus)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runFeishuProjectSyncOnce(ctx, queries, pool, store, taskSvc, bus)
		}
	}
}

func runFeishuProjectSyncOnce(ctx context.Context, queries *db.Queries, pool *pgxpool.Pool, store service.FeishuProjectStorage, taskSvc *service.TaskService, bus *events.Bus) {
	configs, err := queries.ListEnabledFeishuProjectIntegrations(ctx)
	if err != nil {
		slog.Warn("Feishu Project sync scan failed", "error", err)
		return
	}
	svc := &service.FeishuProjectSyncService{Queries: queries, Tx: pool, Client: service.NewFeishuProjectClient(), Storage: store, TaskService: taskSvc, Events: bus}
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
		unlock()
	}
}

// feishuProjectOrphanReconcileDue reports whether the integration is due for an
// orphan reconcile sweep. A NULL last_orphan_reconciled_at counts as "never",
// forcing a sweep on the first tick after the migration lands.
func feishuProjectOrphanReconcileDue(cfg db.FeishuProjectIntegration, now time.Time) bool {
	return !cfg.LastOrphanReconciledAt.Valid || now.Sub(cfg.LastOrphanReconciledAt.Time) >= service.FeishuProjectOrphanReconcileInterval()
}
