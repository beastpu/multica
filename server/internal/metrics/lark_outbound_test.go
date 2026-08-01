package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestLarkOutboundMetricsRecordBoundedDeliverySignals(t *testing.T) {
	m := NewLarkOutboundMetrics()
	registry := prometheus.NewRegistry()
	registry.MustRegister(m.Collectors()...)

	m.RecordCardStarted("cardkit", 7.25)
	m.RecordDelivery("cardkit", "streaming", "success", 0.4)
	m.RecordDelivery("legacy", "final", "retry", 2)
	m.RecordFallback("permission_denied")

	if got := testutil.ToFloat64(m.CardsStarted.WithLabelValues("cardkit")); got != 1 {
		t.Fatalf("cards started=%v want 1", got)
	}
	if got := testutil.ToFloat64(m.Deliveries.WithLabelValues("cardkit", "streaming", "success")); got != 1 {
		t.Fatalf("successful deliveries=%v want 1", got)
	}
	if got := testutil.ToFloat64(m.Deliveries.WithLabelValues("legacy", "final", "retry")); got != 1 {
		t.Fatalf("retry deliveries=%v want 1", got)
	}
	if got := testutil.ToFloat64(m.Fallbacks.WithLabelValues("permission_denied")); got != 1 {
		t.Fatalf("fallbacks=%v want 1", got)
	}
	wanted := []string{
		"multica_lark_outbound_cards_started_total",
		"multica_lark_outbound_first_feedback_seconds",
		"multica_lark_outbound_deliveries_total",
		"multica_lark_outbound_patch_lag_seconds",
		"multica_lark_outbound_fallbacks_total",
	}
	if got, err := testutil.GatherAndCount(registry, wanted...); err != nil || got < len(wanted) {
		t.Fatalf("metric families=%d want at least %d (err=%v)", got, len(wanted), err)
	}
}
