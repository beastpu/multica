package main

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The worker should run the orphan reconcile sweep when last_orphan_reconciled_at
// is older than the configured interval (or NULL — first tick after deploy) and
// otherwise skip it.
func TestFeishuProjectOrphanReconcileDue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	interval := service.FeishuProjectOrphanReconcileInterval()

	tests := []struct {
		name string
		cfg  db.FeishuProjectIntegration
		want bool
	}{
		{
			name: "never reconciled → due on first tick",
			cfg:  db.FeishuProjectIntegration{},
			want: true,
		},
		{
			name: "exactly at interval boundary → due",
			cfg: db.FeishuProjectIntegration{LastOrphanReconciledAt: pgtype.Timestamptz{
				Time: now.Add(-interval), Valid: true,
			}},
			want: true,
		},
		{
			name: "slightly past interval → due",
			cfg: db.FeishuProjectIntegration{LastOrphanReconciledAt: pgtype.Timestamptz{
				Time: now.Add(-interval - time.Minute), Valid: true,
			}},
			want: true,
		},
		{
			name: "within interval → not due",
			cfg: db.FeishuProjectIntegration{LastOrphanReconciledAt: pgtype.Timestamptz{
				Time: now.Add(-interval + time.Minute), Valid: true,
			}},
			want: false,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := feishuProjectOrphanReconcileDue(tc.cfg, now); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
