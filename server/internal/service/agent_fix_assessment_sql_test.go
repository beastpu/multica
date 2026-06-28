package service

import (
	"os"
	"strings"
	"testing"
)

func TestP4AssessmentTaskIsolationSQLInvariants(t *testing.T) {
	body, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	sql := string(body)
	for _, section := range []string{
		"CancelAgentTasksByIssue",
		"CancelAgentTasksByIssueAndAgent",
		"GetLastTaskSession",
		"GetLastTaskStartedAtForIssueAndAgent",
		"HasActiveTaskForIssue",
		"HasPendingTaskForIssue",
		"HasPendingTaskForIssueAndAgent",
		"HasTaskForIssueAndAgent",
		"HasPendingTaskForIssueAndAgentExcludingTriggerComment",
		"GetLatestTaskIsLeaderForIssueAndAgent",
		"ListWorkspaceAgentFixes",
	} {
		chunk := sqlSection(t, sql, section)
		if !strings.Contains(chunk, "agent_fix_p4_assessment") {
			t.Fatalf("%s must explicitly exclude P4 assessment tasks\n---\n%s", section, chunk)
		}
	}
}

func TestClaimSerializationSeparatesP4AssessmentFromNormalIssueTasks(t *testing.T) {
	sql, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	chunk := sqlSection(t, string(sql), "ClaimAgentTask")
	for _, want := range []string{
		"active.issue_id = atq.issue_id",
		"COALESCE(active.context->>'type', '') = COALESCE(atq.context->>'type', '')",
	} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("ClaimAgentTask missing %q\n---\n%s", want, chunk)
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
