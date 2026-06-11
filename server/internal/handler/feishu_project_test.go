package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestFeishuProjectNameFromRequestPrefersProjectName(t *testing.T) {
	got := feishuProjectNameFromRequest(UpdateFeishuProjectIntegrationRequest{
		ProjectName: "  space_name  ",
		ProjectKey:  "old-space-key",
	})
	if got != "space_name" {
		t.Fatalf("project name = %q, want space_name", got)
	}
}

func TestFeishuProjectNameFromRequestFallsBackToProjectKey(t *testing.T) {
	got := feishuProjectNameFromRequest(UpdateFeishuProjectIntegrationRequest{
		ProjectKey: "  old-space-key  ",
	})
	if got != "old-space-key" {
		t.Fatalf("project name fallback = %q, want old-space-key", got)
	}
}

func TestMergeLegacyIssueMappingsUpdatesExistingIssueEntry(t *testing.T) {
	configs := []service.FeishuProjectWorkItemTypeConfig{
		{TypeKey: "issue", StatusMapping: map[string]string{"OLD": "todo"}},
		{TypeKey: "ticket-type", StatusMapping: map[string]string{"待处理": "todo"}},
	}
	out := mergeLegacyIssueMappings(configs, UpdateFeishuProjectIntegrationRequest{
		SyncIssue:            true,
		StatusMapping:        map[string]string{"OPEN": "todo"},
		ReverseStatusMapping: map[string]string{"todo": "OPEN"},
	})
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].StatusMapping["OPEN"] != "todo" || out[0].StatusMapping["OLD"] != "" {
		t.Fatalf("issue mapping not replaced: %v", out[0].StatusMapping)
	}
	if out[1].StatusMapping["待处理"] != "todo" {
		t.Fatalf("non-issue entry must be untouched: %v", out[1].StatusMapping)
	}
}

func TestMergeLegacyIssueMappingsAddsIssueEntryForLegacyClients(t *testing.T) {
	out := mergeLegacyIssueMappings(nil, UpdateFeishuProjectIntegrationRequest{
		SyncIssue:     true,
		StatusMapping: map[string]string{"OPEN": "todo"},
	})
	if len(out) != 1 || out[0].TypeKey != "issue" || out[0].IdentifierPrefix != "BUG" {
		t.Fatalf("issue entry not seeded: %+v", out)
	}
	if out[0].StatusMapping["OPEN"] != "todo" {
		t.Fatalf("issue mapping not applied: %v", out[0].StatusMapping)
	}
	// sync_issue=false from a legacy client must not resurrect the entry.
	if out := mergeLegacyIssueMappings(nil, UpdateFeishuProjectIntegrationRequest{SyncIssue: false}); len(out) != 0 {
		t.Fatalf("unexpected entries: %+v", out)
	}
}
