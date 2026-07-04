package service

import (
	"os"
	"strings"
	"testing"
)

// Agent derived-work ("agent_work") SQL invariants. The projection issue is a
// normal issue whose authoritative marker is the reserved metadata key
// `agent_work` — every write query that touches projection issues must be
// guarded by that marker plus the workspace, so a bug can never mutate an
// ordinary user issue (docs/agent-fix-p4-assessment-issue-design.md, Part 1).

func TestAgentWorkIssueStatusUpdateGuardedByMetadataMarker(t *testing.T) {
	body, err := os.ReadFile("../../pkg/db/queries/agent_work.sql")
	if err != nil {
		t.Fatalf("read agent_work.sql: %v", err)
	}
	chunk := sqlSection(t, string(body), "UpdateAgentWorkIssueStatus")
	for _, want := range []string{
		"jsonb_exists(metadata, 'agent_work')",
		"workspace_id",
	} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("UpdateAgentWorkIssueStatus must be guarded by %q so it can never touch an ordinary issue\n---\n%s", want, chunk)
		}
	}
}

func TestAgentWorkProjectInsertIsIdempotent(t *testing.T) {
	body, err := os.ReadFile("../../pkg/db/queries/agent_work.sql")
	if err != nil {
		t.Fatalf("read agent_work.sql: %v", err)
	}
	chunk := sqlSection(t, string(body), "InsertAgentWorkProject")
	if !strings.Contains(chunk, "ON CONFLICT (workspace_id, kind) DO NOTHING") {
		t.Fatalf("InsertAgentWorkProject must be ON CONFLICT DO NOTHING for multi-replica safety\n---\n%s", chunk)
	}
}

func TestCreateAgentWorkIssueWritesMetadataAndNoAssignee(t *testing.T) {
	body, err := os.ReadFile("../../pkg/db/queries/agent_work.sql")
	if err != nil {
		t.Fatalf("read agent_work.sql: %v", err)
	}
	chunk := sqlSection(t, string(body), "CreateAgentWorkIssue")
	if !strings.Contains(chunk, "metadata") {
		t.Fatalf("CreateAgentWorkIssue must stamp the agent_work metadata at insert time\n---\n%s", chunk)
	}
	for _, forbidden := range []string{"assignee_type", "assignee_id"} {
		if strings.Contains(chunk, forbidden) {
			t.Fatalf("CreateAgentWorkIssue must not set %s (phase 2 has no capability agent yet)\n---\n%s", forbidden, chunk)
		}
	}
}

func TestSetP4AssessmentIssueScopedToWorkspaceAndBinding(t *testing.T) {
	body, err := os.ReadFile("../../pkg/db/queries/agent.sql")
	if err != nil {
		t.Fatalf("read agent.sql: %v", err)
	}
	chunk := sqlSection(t, string(body), "SetP4AssessmentIssue")
	for _, want := range []string{"workspace_id", "feishu_binding_id"} {
		if !strings.Contains(chunk, want) {
			t.Fatalf("SetP4AssessmentIssue must be keyed on %q\n---\n%s", want, chunk)
		}
	}
}

func TestAgentWorkProjectMigrationHasCompositePrimaryKey(t *testing.T) {
	body, err := os.ReadFile("../../migrations/134_agent_work_project.up.sql")
	if err != nil {
		t.Fatalf("read migration 134: %v", err)
	}
	if !strings.Contains(string(body), "PRIMARY KEY (workspace_id, kind)") {
		t.Fatalf("agent_work_project must have PRIMARY KEY (workspace_id, kind) — it is the idempotency key\n---\n%s", string(body))
	}
}
