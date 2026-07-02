package main

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
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

// TestP4AssessmentAllowlistFromEnv locks the fail-closed opt-in switch: unset or
// blank ⇒ no workspace triggers assessment; a comma list is trimmed, lowercased,
// and blank entries are dropped so only the named workspaces are gated in.
func TestP4AssessmentAllowlistFromEnv(t *testing.T) {
	const idA = "FEFE70D0-844D-4F4A-85C8-F995666DAF1F"
	const idB = "015959f4-69b9-4f21-85b0-ff8188d22c78"

	t.Run("unset disables everywhere", func(t *testing.T) {
		t.Setenv(p4AssessmentWorkspaceAllowlistEnv, "")
		if got := p4AssessmentAllowlistFromEnv(); got != nil {
			t.Fatalf("blank env: want nil allowlist, got %v", got)
		}
	})

	t.Run("blank and separator-only entries drop to nil", func(t *testing.T) {
		t.Setenv(p4AssessmentWorkspaceAllowlistEnv, " , ,")
		if got := p4AssessmentAllowlistFromEnv(); got != nil {
			t.Fatalf("separator-only env: want nil allowlist, got %v", got)
		}
	})

	t.Run("trims, lowercases, dedupes and gates only listed workspaces", func(t *testing.T) {
		t.Setenv(p4AssessmentWorkspaceAllowlistEnv, "  "+idA+" , "+idB+" , ")
		list := p4AssessmentAllowlistFromEnv()
		if len(list) != 2 {
			t.Fatalf("want 2 entries, got %d (%v)", len(list), list)
		}
		if !list.Allows(util.MustParseUUID(idA)) {
			t.Fatal("uppercased env entry must match canonical lowercase workspace UUID")
		}
		if !list.Allows(util.MustParseUUID(idB)) {
			t.Fatal("listed workspace must be permitted")
		}
		if list.Allows(util.MustParseUUID("11111111-1111-1111-1111-111111111111")) {
			t.Fatal("unlisted workspace must be denied")
		}
	})
}
