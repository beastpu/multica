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

// TestFeishuP4AssessmentTriggerAvoidsTypedNil locks the fix for the new-image
// startup panic. main.go now wires taskSvc.P4Assessment, but the worker must
// also refuse to box a nil *P4AssessmentService into the trigger interface: a
// typed-nil interface passes the downstream `!= nil` guards and then nil-derefs
// inside BackfillDoneBindings on the first done binding.
func TestFeishuP4AssessmentTriggerAvoidsTypedNil(t *testing.T) {
	t.Parallel()
	if got := feishuP4AssessmentTrigger(nil); got != nil {
		t.Fatalf("nil taskSvc: want nil trigger, got non-nil interface")
	}
	if got := feishuP4AssessmentTrigger(&service.TaskService{}); got != nil {
		t.Fatalf("nil P4Assessment pointer must not box into a typed-nil interface")
	}
	wired := &service.TaskService{P4Assessment: service.NewP4AssessmentService(nil, nil, nil)}
	if got := feishuP4AssessmentTrigger(wired); got == nil {
		t.Fatalf("wired service: want non-nil trigger, got nil")
	}
}
