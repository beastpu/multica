package service

import (
	"os"
	"strings"
	"testing"
)

// task_category was retired in C-2: assessment tasks hang on their own
// derived agent_work projection issue, so per-issue workflow queries never
// meet them through a real issue and no query may still filter on the
// dropped column.
func TestAgentQueriesNoLongerReferenceTaskCategory(t *testing.T) {
	body, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	if strings.Contains(string(body), "task_category") {
		t.Fatalf("agent.sql must not reference the retired task_category column")
	}
	if strings.Contains(string(body), "leased_until") {
		t.Fatalf("agent.sql must not reference the retired leased_until column")
	}
}

// The native assessment task must run in a fresh session and carry the
// stale-daemon steering handoff_note.
func TestP4AssessmentNativeTaskIsolationInvariants(t *testing.T) {
	body, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	chunk := sqlSection(t, string(body), "CreateP4AssessmentTask")
	for _, want := range []string{
		"TRUE",
		"handoff_note",
	} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("CreateP4AssessmentTask missing %q\n---\n%s", want, chunk)
		}
	}
}

// Plan C-1 single-direction projection: task start marks the row running,
// counts the attempt, and must never clobber a submitted result.
func TestP4AssessmentStartFromTaskInvariants(t *testing.T) {
	body, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	chunk := sqlSection(t, string(body), "StartP4AssessmentFromTask")
	for _, want := range []string{
		"assessment_status = 'running'",
		"attempt_count = attempt_count + 1",
		"assessment_task_id = $2",
		"assessment_status <> 'completed'",
	} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("StartP4AssessmentFromTask missing %q\n---\n%s", want, chunk)
		}
	}
}

// Plan C-1 capability role config: the composite PK is the upsert idempotency
// key, and the Get joins agent + runtime so Trigger can validate agent health.
func TestWorkspaceAgentCapabilityQueryInvariants(t *testing.T) {
	body, err := os.ReadFile("../../pkg/db/queries/workspace_agent_capability.sql")
	if err != nil {
		t.Fatalf("read workspace_agent_capability.sql: %v", err)
	}
	sql := string(body)
	upsert := sqlSection(t, sql, "UpsertWorkspaceAgentCapability")
	if !strings.Contains(upsert, "ON CONFLICT (workspace_id, capability) DO UPDATE") {
		t.Fatalf("UpsertWorkspaceAgentCapability must upsert on the composite PK\n---\n%s", upsert)
	}
	get := sqlSection(t, sql, "GetWorkspaceAgentCapability")
	for _, want := range []string{
		"JOIN agent a ON a.id = c.agent_id",
		"a.archived_at AS agent_archived_at",
		"a.runtime_id AS agent_runtime_id",
		"runtime_mode",
	} {
		if !strings.Contains(get, want) {
			t.Fatalf("GetWorkspaceAgentCapability missing %q\n---\n%s", want, get)
		}
	}
}

func TestP4AssessmentTriggerUsesBindingRowLock(t *testing.T) {
	sql, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	chunk := sqlSection(t, string(sql), "LockP4AssessmentBinding")
	for _, want := range []string{
		"feishu_project_issue_binding",
		"FOR UPDATE",
	} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("LockP4AssessmentBinding missing %q\n---\n%s", want, chunk)
		}
	}
}

func TestAgentFixExternalDoneUsesStatusMappingInputs(t *testing.T) {
	sql, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	chunk := sqlSection(t, string(sql), "ListWorkspaceAgentFixes")
	for _, want := range []string{
		"fib.work_item_type AS external_work_item_type",
		"fpi.status_mapping AS external_status_mapping",
		"fpi.work_item_types AS external_work_item_types",
	} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("ListWorkspaceAgentFixes missing mapping input %q\n---\n%s", want, chunk)
		}
	}
}

func TestOperationsFeedUsesBindingSpineWithoutAssessmentTaskPollution(t *testing.T) {
	sql, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	chunk := sqlSection(t, string(sql), "ListWorkspaceAgentFixes")
	for _, want := range []string{
		"WITH latest AS",
		"spine AS",
		"FROM feishu_project_issue_binding fib",
		"fib.last_external_updated_at",
		"COALESCE(fib.last_external_updated_at, fib.last_synced_at)",
		"false AS has_normal_task",
		// Derived agent_work issues (per-run assessment projections) must not
		// spawn feed rows — the metadata marker is the only isolation anchor
		// now that task_category is gone.
		"NOT jsonb_exists(ti.metadata, 'agent_work')",
	} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("ListWorkspaceAgentFixes missing binding spine invariant %q\n---\n%s", want, chunk)
		}
	}

	src, err := os.ReadFile("../handler/agent.go")
	if err != nil {
		t.Fatalf("read handler agent.go: %v", err)
	}
	handlerChunk := sourceFunction(t, string(src), "ListWorkspaceAgentFixes")
	for _, want := range []string{
		"external := buildAgentFixExternal(row)",
		"!row.HasNormalTask",
		`external.MappedStatus != "done"`,
		"continue",
	} {
		if !strings.Contains(handlerChunk, want) {
			t.Fatalf("ListWorkspaceAgentFixes handler missing no-task done filter %q\n---\n%s", want, handlerChunk)
		}
	}
}

func TestP4AssessmentBackfillScansBindingsWithStatusMapping(t *testing.T) {
	sql, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	chunk := sqlSection(t, string(sql), "ListP4AssessmentBackfillBindings")
	for _, want := range []string{
		"FROM feishu_project_issue_binding fib",
		"JOIN feishu_project_integration fpi",
		"fpi.status_mapping",
		"fpi.work_item_types",
		"LEFT JOIN agent_fix_p4_assessment p4",
		"AND p4.id IS NULL",
	} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("ListP4AssessmentBackfillBindings missing %q\n---\n%s", want, chunk)
		}
	}
	for _, forbidden := range []string{
		"metadata",
		"demo",
		"title",
	} {
		if strings.Contains(chunk, forbidden) {
			t.Fatalf("ListP4AssessmentBackfillBindings must not use %q as a trigger signal\n---\n%s", forbidden, chunk)
		}
	}
}

func TestAgentFixReviewByBindingUsesBindingAsWriteSpine(t *testing.T) {
	sql, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	chunk := sqlSection(t, string(sql), "UpsertAgentFixReviewByBinding")
	for _, want := range []string{
		"FROM feishu_project_issue_binding fib",
		"fib.workspace_id = sqlc.arg('workspace_id')",
		"fib.id = sqlc.arg('feishu_binding_id')",
		"ON CONFLICT (workspace_id, feishu_binding_id)",
		"agent_fix_review",
	} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("UpsertAgentFixReviewByBinding missing %q\n---\n%s", want, chunk)
		}
	}
	for _, forbidden := range []string{
		"metadata",
		"demo",
		"title",
		"issue_id = sqlc.arg('issue_id')",
	} {
		if strings.Contains(chunk, forbidden) {
			t.Fatalf("UpsertAgentFixReviewByBinding must not use %q as a write signal\n---\n%s", forbidden, chunk)
		}
	}
}

func TestP4EvidenceQueriesAreScopedAndBounded(t *testing.T) {
	sql, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	taskChunk := sqlSection(t, string(sql), "ListP4EvidenceTasksByIssue")
	for _, want := range []string{
		"JOIN agent a ON a.id = atq.agent_id",
		"atq.issue_id = sqlc.arg('issue_id')",
		"a.workspace_id = sqlc.arg('workspace_id')",
		"COALESCE(atq.context->>'type', '') = 'agent_fix_p4_assessment' AS is_p4_assessment",
		"LIMIT 20",
	} {
		if !strings.Contains(taskChunk, want) {
			t.Fatalf("ListP4EvidenceTasksByIssue missing %q\n---\n%s", want, taskChunk)
		}
	}
	for _, forbidden := range []string{
		"atq.result",
		"atq.context,",
		"atq.work_dir",
		"atq.session_id",
	} {
		if strings.Contains(taskChunk, forbidden) {
			t.Fatalf("ListP4EvidenceTasksByIssue must not expose %q\n---\n%s", forbidden, taskChunk)
		}
	}

	commentChunk := sqlSection(t, string(sql), "ListP4EvidenceCommentsByIssue")
	for _, want := range []string{
		"c.issue_id = sqlc.arg('issue_id')",
		"c.workspace_id = sqlc.arg('workspace_id')",
		"c.type = 'comment'",
		"c.author_type = 'agent'",
		"LIMIT 20",
	} {
		if !strings.Contains(commentChunk, want) {
			t.Fatalf("ListP4EvidenceCommentsByIssue missing %q\n---\n%s", want, commentChunk)
		}
	}
}

func TestP4EvidenceHandlerRequiresTaskScopedBindingForAgents(t *testing.T) {
	src, err := os.ReadFile("../handler/agent.go")
	if err != nil {
		t.Fatalf("read handler agent.go: %v", err)
	}
	chunk := sourceFunction(t, string(src), "requestTaskCanReadP4Evidence")
	for _, want := range []string{
		`r.Header.Get("X-Task-ID")`,
		"util.ParseUUID(taskID)",
		"h.Queries.GetAgentTask",
		"uuidToString(task.AgentID) != actorID",
		"ctx.Type != service.P4AssessmentTaskType",
		"ctx.WorkspaceID == workspaceID",
		"ctx.FeishuBindingID == uuidToString(bindingID)",
	} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("requestTaskCanReadP4Evidence missing %q\n---\n%s", want, chunk)
		}
	}
}

// The observability columns (attempt_count / last_error) must be maintained by
// every queue transition, otherwise "why is this row stuck" goes back to being
// unanswerable: task start counts attempts, failure records the reason,
// completion clears it, and the operations feed exposes them per row.
func TestP4AssessmentObservabilityColumnsMaintained(t *testing.T) {
	body, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	sql := string(body)

	fail := sqlSection(t, sql, "FailP4AssessmentFromTask")
	if !strings.Contains(fail, "last_error =") {
		t.Fatalf("FailP4AssessmentFromTask must record last_error\n---\n%s", fail)
	}

	complete := sqlSection(t, sql, "CompleteP4AssessmentFromTask")
	if !strings.Contains(complete, "last_error = ''") {
		t.Fatalf("CompleteP4AssessmentFromTask must clear last_error on success\n---\n%s", complete)
	}

	feed := sqlSection(t, sql, "ListWorkspaceAgentFixes")
	for _, want := range []string{
		"AS p4_attempt_count",
		"AS p4_last_error",
		"AS p4_assessment_agent_name",
	} {
		if !strings.Contains(feed, want) {
			t.Fatalf("ListWorkspaceAgentFixes must expose %q for the operations detail rows\n---\n%s", want, feed)
		}
	}
}

func sqlSection(t *testing.T, sql, name string) string {
	t.Helper()
	marker := "-- name: " + name + " "
	start := strings.Index(sql, marker)
	if start < 0 {
		t.Fatalf("section %s not found", name)
	}
	rest := sql[start+len(marker):]
	end := strings.Index(rest, "\n-- name: ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func sourceFunction(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "func (h *Handler) "+name)
	if start < 0 {
		t.Fatalf("function %s not found", name)
	}
	rest := src[start:]
	next := strings.Index(rest[len("func "):], "\nfunc ")
	if next < 0 {
		return rest
	}
	return rest[:next+len("func ")]
}
