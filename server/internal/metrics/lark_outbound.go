package metrics

import "github.com/prometheus/client_golang/prometheus"

type LarkOutboundMetrics struct {
	Deliveries *prometheus.CounterVec
}

func NewLarkOutboundMetrics() *LarkOutboundMetrics {
	return &LarkOutboundMetrics{
		Deliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica", Subsystem: "lark_outbound", Name: "deliveries_total",
			Help: "Total outbound chat replies, by terminal status and outcome.",
		}, []string{"status", "outcome"}),
	}
}

func (m *LarkOutboundMetrics) Collectors() []prometheus.Collector {
	if m == nil {
		return nil
	}
	return []prometheus.Collector{m.Deliveries}
}

func (m *LarkOutboundMetrics) RecordDelivery(status, outcome string) {
	if m == nil {
		return
	}
	m.Deliveries.WithLabelValues(status, outcome).Inc()
}
