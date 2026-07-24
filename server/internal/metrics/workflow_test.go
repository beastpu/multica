package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestWorkflowMetricsAreRegisteredAndLowCardinality(t *testing.T) {
	metrics := NewBusinessMetrics()
	registry := prometheus.NewRegistry()
	for _, collector := range metrics.Collectors() {
		registry.MustRegister(collector)
	}

	metrics.RecordWorkflowInstance("started")
	metrics.RecordWorkflowActiveRun("running", 1)
	metrics.RecordWorkflowAdoption("new_workflow")
	metrics.RecordWorkflowNode("completed", time.Now().Add(-time.Second))
	metrics.RecordWorkflowMaterialization("success", 25*time.Millisecond)
	metrics.RecordWorkflowOperation("reconcile", "repaired")
	metrics.RecordWorkflowExecutorResolution("fixed_role", "resolved")
	metrics.RecordWorkflowSubmission("invalid")
	metrics.RecordWorkflowVerdict("deterministic", "pass")
	metrics.RecordWorkflowAcceptance("first_pass")
	metrics.RecordWorkflowHumanIntervention("rollback")

	recorder := httptest.NewRecorder()
	NewHandler(registry).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)
	body := recorder.Body.String()
	for _, metricName := range []string{
		"multica_workflow_instance_transition_total",
		"multica_workflow_active_runs",
		"multica_workflow_adoption_total",
		"multica_workflow_node_transition_total",
		"multica_workflow_node_duration_seconds",
		"multica_workflow_materialization_total",
		"multica_workflow_materialization_duration_seconds",
		"multica_workflow_operation_total",
		"multica_workflow_executor_resolution_total",
		"multica_workflow_submission_total",
		"multica_workflow_verdict_total",
		"multica_workflow_acceptance_total",
		"multica_workflow_human_intervention_total",
	} {
		if !strings.Contains(body, metricName) {
			t.Fatalf("/metrics missing %q\n%s", metricName, body)
		}
	}
	if strings.Contains(body, "workflow_instance_id") ||
		strings.Contains(body, "host_issue_id") {
		t.Fatalf("workflow metrics leaked high-cardinality IDs\n%s", body)
	}
}

func TestWorkflowMetricMethodsAreNilSafe(t *testing.T) {
	var metrics *BusinessMetrics
	metrics.RecordWorkflowInstance("started")
	metrics.RecordWorkflowActiveRun("running", 1)
	metrics.RecordWorkflowAdoption("existing_issue")
	metrics.RecordWorkflowNode("completed", time.Time{})
	metrics.RecordWorkflowMaterialization("failed", 0)
	metrics.RecordWorkflowOperation("duplicate", "prevented")
	metrics.RecordWorkflowExecutorResolution("manual", "needs_setup")
	metrics.RecordWorkflowSubmission("invalid")
	metrics.RecordWorkflowVerdict("member", "blocked")
	metrics.RecordWorkflowAcceptance("rework")
	metrics.RecordWorkflowHumanIntervention("skip")
}
