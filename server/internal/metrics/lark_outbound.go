package metrics

import "github.com/prometheus/client_golang/prometheus"

type LarkOutboundMetrics struct {
	CardsStarted         *prometheus.CounterVec
	FirstFeedbackSeconds *prometheus.HistogramVec
	Deliveries           *prometheus.CounterVec
	PatchLagSeconds      *prometheus.HistogramVec
	Fallbacks            *prometheus.CounterVec
}

func NewLarkOutboundMetrics() *LarkOutboundMetrics {
	return &LarkOutboundMetrics{
		CardsStarted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica", Subsystem: "lark_outbound", Name: "cards_started_total",
			Help: "Total long-running task cards first delivered to Feishu.",
		}, []string{"transport"}),
		FirstFeedbackSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "multica", Subsystem: "lark_outbound", Name: "first_feedback_seconds",
			Help:    "Seconds from task creation until its first Feishu progress card is delivered.",
			Buckets: []float64{1, 3, 5, 7, 10, 15, 30, 60},
		}, []string{"transport"}),
		Deliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica", Subsystem: "lark_outbound", Name: "deliveries_total",
			Help: "Total durable outbound revision delivery outcomes.",
		}, []string{"transport", "status", "outcome"}),
		PatchLagSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "multica", Subsystem: "lark_outbound", Name: "patch_lag_seconds",
			Help:    "Delay between an outbound revision becoming due and being processed.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		}, []string{"transport"}),
		Fallbacks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "multica", Subsystem: "lark_outbound", Name: "fallbacks_total",
			Help: "Total CardKit to legacy transport fallbacks.",
		}, []string{"reason"}),
	}
}

func (m *LarkOutboundMetrics) Collectors() []prometheus.Collector {
	if m == nil {
		return nil
	}
	return []prometheus.Collector{m.CardsStarted, m.FirstFeedbackSeconds, m.Deliveries, m.PatchLagSeconds, m.Fallbacks}
}

func (m *LarkOutboundMetrics) RecordCardStarted(transport string, seconds float64) {
	if m == nil {
		return
	}
	m.CardsStarted.WithLabelValues(transport).Inc()
	m.FirstFeedbackSeconds.WithLabelValues(transport).Observe(seconds)
}

func (m *LarkOutboundMetrics) RecordDelivery(transport, status, outcome string, lagSeconds float64) {
	if m == nil {
		return
	}
	m.Deliveries.WithLabelValues(transport, status, outcome).Inc()
	if lagSeconds >= 0 {
		m.PatchLagSeconds.WithLabelValues(transport).Observe(lagSeconds)
	}
}

func (m *LarkOutboundMetrics) RecordFallback(reason string) {
	if m == nil {
		return
	}
	m.Fallbacks.WithLabelValues(reason).Inc()
}
