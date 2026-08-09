package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestLarkOutboundMetricsRecordDeliveryOutcomes(t *testing.T) {
	m := NewLarkOutboundMetrics()
	registry := prometheus.NewRegistry()
	registry.MustRegister(m.Collectors()...)

	m.RecordDelivery("final", "sent")
	m.RecordDelivery("error", "sent")
	m.RecordDelivery("final", "failed")

	for _, tc := range [][2]string{{"final", "sent"}, {"error", "sent"}, {"final", "failed"}} {
		if got := testutil.ToFloat64(m.Deliveries.WithLabelValues(tc[0], tc[1])); got != 1 {
			t.Errorf("deliveries%v=%v want 1", tc, got)
		}
	}
	if got, err := testutil.GatherAndCount(registry, "multica_lark_outbound_deliveries_total"); err != nil || got != 3 {
		t.Fatalf("recorded series=%d want 3 (err=%v)", got, err)
	}
}
