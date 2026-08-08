package metrics

import "github.com/prometheus/client_golang/prometheus"

type LarkOutboundMetrics struct {
	CardsStarted         prometheus.Counter
	FirstFeedbackSeconds prometheus.Histogram
	Deliveries           *prometheus.CounterVec
}

func NewLarkOutboundMetrics() *LarkOutboundMetrics {
	return &LarkOutboundMetrics{
		CardsStarted: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "multica", Subsystem: "lark_outbound", Name: "cards_started_total",
			Help: "Total long-running task cards first delivered to Feishu.",
		}),
		FirstFeedbackSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "multica", Subsystem: "lark_outbound", Name: "first_feedback_seconds",
			Help:    "Seconds from a task opening its card until that card is delivered.",
			Buckets: []float64{1, 3, 5, 7, 10, 15, 30, 60},
		}),
		Deliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica", Subsystem: "lark_outbound", Name: "deliveries_total",
			Help: "Total outbound card paint outcomes, by card status and what was done.",
		}, []string{"status", "outcome"}),
	}
}

func (m *LarkOutboundMetrics) Collectors() []prometheus.Collector {
	if m == nil {
		return nil
	}
	return []prometheus.Collector{m.CardsStarted, m.FirstFeedbackSeconds, m.Deliveries}
}

func (m *LarkOutboundMetrics) RecordCardStarted(seconds float64) {
	if m == nil {
		return
	}
	m.CardsStarted.Inc()
	m.FirstFeedbackSeconds.Observe(seconds)
}

func (m *LarkOutboundMetrics) RecordDelivery(status, outcome string) {
	if m == nil {
		return
	}
	m.Deliveries.WithLabelValues(status, outcome).Inc()
}
