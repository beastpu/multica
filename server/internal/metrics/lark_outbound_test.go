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

	m.RecordCardStarted(7.25)
	m.RecordDelivery("streaming", "sent")
	m.RecordDelivery("streaming", "repaint")
	m.RecordDelivery("final", "patched")

	if got := testutil.ToFloat64(m.CardsStarted); got != 1 {
		t.Fatalf("cards started=%v want 1", got)
	}
	if got := testutil.ToFloat64(m.Deliveries.WithLabelValues("streaming", "sent")); got != 1 {
		t.Fatalf("sent deliveries=%v want 1", got)
	}
	if got := testutil.ToFloat64(m.Deliveries.WithLabelValues("streaming", "repaint")); got != 1 {
		t.Fatalf("repaint deliveries=%v want 1", got)
	}
	if got := testutil.ToFloat64(m.Deliveries.WithLabelValues("final", "patched")); got != 1 {
		t.Fatalf("patched deliveries=%v want 1", got)
	}
	wanted := []string{
		"multica_lark_outbound_cards_started_total",
		"multica_lark_outbound_first_feedback_seconds",
		"multica_lark_outbound_deliveries_total",
	}
	if got, err := testutil.GatherAndCount(registry, wanted...); err != nil || got < len(wanted) {
		t.Fatalf("metric families=%d want at least %d (err=%v)", got, len(wanted), err)
	}
}
