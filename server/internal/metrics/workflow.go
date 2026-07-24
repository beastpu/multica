package metrics

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var workflowDurationBuckets = []float64{
	0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
	300, 900, 3600, 6 * 3600, 24 * 3600, 7 * 24 * 3600,
}

type workflowMetrics struct {
	instance               *prometheus.CounterVec
	activeRuns             *prometheus.GaugeVec
	adoption               *prometheus.CounterVec
	node                   *prometheus.CounterVec
	nodeDuration           *prometheus.HistogramVec
	materialization        *prometheus.CounterVec
	materializationLatency *prometheus.HistogramVec
	operation              *prometheus.CounterVec
	executorResolution     *prometheus.CounterVec
	submission             *prometheus.CounterVec
	verdict                *prometheus.CounterVec
	acceptance             *prometheus.CounterVec
	humanIntervention      *prometheus.CounterVec
}

func newWorkflowMetrics() *workflowMetrics {
	return &workflowMetrics{
		instance: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_workflow_instance_transition_total",
			Help: "Workflow instance lifecycle transitions.",
		}, []string{"event"}),
		activeRuns: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "multica_workflow_active_runs",
			Help: "In-process gauge of active Workflow runs by lifecycle status.",
		}, []string{"status"}),
		adoption: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_workflow_adoption_total",
			Help: "Workflow runs started through each product entry point.",
		}, []string{"entry_point"}),
		node: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_workflow_node_transition_total",
			Help: "Workflow node lifecycle transitions.",
		}, []string{"event"}),
		nodeDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "multica_workflow_node_duration_seconds",
			Help:    "Time from node activation to a terminal node state.",
			Buckets: workflowDurationBuckets,
		}, []string{"outcome"}),
		materialization: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_workflow_materialization_total",
			Help: "Workflow Node Task materialization attempts by outcome.",
		}, []string{"outcome"}),
		materializationLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "multica_workflow_materialization_duration_seconds",
			Help:    "Workflow Node Task Issue materialization duration.",
			Buckets: workflowDurationBuckets,
		}, []string{"outcome"}),
		operation: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_workflow_operation_total",
			Help: "Workflow repair, deduplication, and resolution operations.",
		}, []string{"operation", "outcome"}),
		executorResolution: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_workflow_executor_resolution_total",
			Help: "Workflow executor resolution decisions.",
		}, []string{"strategy", "outcome"}),
		submission: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_workflow_submission_total",
			Help: "Workflow structured submissions by validation outcome.",
		}, []string{"outcome"}),
		verdict: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_workflow_verdict_total",
			Help: "Workflow verdicts by evaluator and result.",
		}, []string{"evaluator", "result"}),
		acceptance: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_workflow_acceptance_total",
			Help: "Workflow acceptance decisions, including first-pass and rework.",
		}, []string{"outcome"}),
		humanIntervention: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "multica_workflow_human_intervention_total",
			Help: "Explicit member or admin interventions in workflow execution.",
		}, []string{"action"}),
	}
}

func (m *workflowMetrics) collectors() []prometheus.Collector {
	if m == nil {
		return nil
	}
	return []prometheus.Collector{
		m.instance,
		m.activeRuns,
		m.adoption,
		m.node,
		m.nodeDuration,
		m.materialization,
		m.materializationLatency,
		m.operation,
		m.executorResolution,
		m.submission,
		m.verdict,
		m.acceptance,
		m.humanIntervention,
	}
}

func normalizeWorkflowMetricLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return "other"
		}
	}
	if len(value) > 48 {
		return "other"
	}
	return value
}

func (m *BusinessMetrics) RecordWorkflowActiveRun(status string, delta float64) {
	if m == nil || m.workflow == nil || delta == 0 {
		return
	}
	m.workflow.activeRuns.
		WithLabelValues(normalizeWorkflowMetricLabel(status)).
		Add(delta)
}

func (m *BusinessMetrics) RecordWorkflowAdoption(entryPoint string) {
	if m == nil || m.workflow == nil {
		return
	}
	m.workflow.adoption.
		WithLabelValues(normalizeWorkflowMetricLabel(entryPoint)).
		Inc()
}

func (m *BusinessMetrics) RecordWorkflowInstance(event string) {
	if m == nil || m.workflow == nil {
		return
	}
	m.workflow.instance.WithLabelValues(normalizeWorkflowMetricLabel(event)).Inc()
}

func (m *BusinessMetrics) RecordWorkflowNode(event string, activatedAt time.Time) {
	if m == nil || m.workflow == nil {
		return
	}
	event = normalizeWorkflowMetricLabel(event)
	m.workflow.node.WithLabelValues(event).Inc()
	if !activatedAt.IsZero() &&
		(event == "completed" || event == "blocked" || event == "failed" ||
			event == "skipped" || event == "cancelled") {
		duration := time.Since(activatedAt).Seconds()
		if duration >= 0 {
			m.workflow.nodeDuration.WithLabelValues(event).Observe(duration)
		}
	}
}

func (m *BusinessMetrics) RecordWorkflowMaterialization(
	outcome string,
	duration time.Duration,
) {
	if m == nil || m.workflow == nil {
		return
	}
	outcome = normalizeWorkflowMetricLabel(outcome)
	m.workflow.materialization.WithLabelValues(outcome).Inc()
	if duration >= 0 {
		m.workflow.materializationLatency.
			WithLabelValues(outcome).
			Observe(duration.Seconds())
	}
}

func (m *BusinessMetrics) RecordWorkflowOperation(operation, outcome string) {
	if m == nil || m.workflow == nil {
		return
	}
	m.workflow.operation.WithLabelValues(
		normalizeWorkflowMetricLabel(operation),
		normalizeWorkflowMetricLabel(outcome),
	).Inc()
}

func (m *BusinessMetrics) RecordWorkflowExecutorResolution(strategy, outcome string) {
	if m == nil || m.workflow == nil {
		return
	}
	m.workflow.executorResolution.WithLabelValues(
		normalizeWorkflowMetricLabel(strategy),
		normalizeWorkflowMetricLabel(outcome),
	).Inc()
}

func (m *BusinessMetrics) RecordWorkflowSubmission(outcome string) {
	if m == nil || m.workflow == nil {
		return
	}
	m.workflow.submission.WithLabelValues(normalizeWorkflowMetricLabel(outcome)).Inc()
}

func (m *BusinessMetrics) RecordWorkflowVerdict(evaluator, result string) {
	if m == nil || m.workflow == nil {
		return
	}
	m.workflow.verdict.WithLabelValues(
		normalizeWorkflowMetricLabel(evaluator),
		normalizeWorkflowMetricLabel(result),
	).Inc()
}

func (m *BusinessMetrics) RecordWorkflowAcceptance(outcome string) {
	if m == nil || m.workflow == nil {
		return
	}
	m.workflow.acceptance.WithLabelValues(normalizeWorkflowMetricLabel(outcome)).Inc()
}

func (m *BusinessMetrics) RecordWorkflowHumanIntervention(action string) {
	if m == nil || m.workflow == nil {
		return
	}
	m.workflow.humanIntervention.
		WithLabelValues(normalizeWorkflowMetricLabel(action)).
		Inc()
}
