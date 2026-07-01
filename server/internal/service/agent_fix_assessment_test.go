package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestParseP4AssessmentTaskOutputStrictJSON(t *testing.T) {
	result, _ := json.Marshal(map[string]string{
		"output": `{
			"delivery_attribution_prediction":"unknown",
			"quality_prediction":"unknown",
			"prediction_reasons":["missing_external_cl"],
			"confidence":0.5,
			"workstream":"",
			"swarm_reviews":[],
			"ai_shelved_cls":[280825],
			"swarm_change_cls":[],
			"swarm_committed_cls":[],
			"external_committed_cls":[],
			"summary":"Evidence is incomplete.",
			"warnings":["missing_external_cl"]
		}`,
	})

	out, err := parseP4AssessmentTaskOutput(result)
	if err != nil {
		t.Fatalf("parseP4AssessmentTaskOutput: %v", err)
	}
	if out.DeliveryAttributionPrediction != "unknown" || out.QualityPrediction != "unknown" {
		t.Fatalf("predictions = %q/%q", out.DeliveryAttributionPrediction, out.QualityPrediction)
	}
	if len(out.AIShelvedCLs) != 1 || out.AIShelvedCLs[0] != 280825 {
		t.Fatalf("ai_shelved_cls = %#v", out.AIShelvedCLs)
	}
}

func TestParseP4AssessmentTaskOutputRejectsNaturalLanguage(t *testing.T) {
	result, _ := json.Marshal(map[string]string{
		"output": "The fix looks good. final CL 123456.",
	})

	if _, err := parseP4AssessmentTaskOutput(result); err == nil {
		t.Fatal("expected natural-language output to be rejected")
	}
}

func TestParseP4AssessmentTaskOutputAcceptsSingleFencedJSONBlock(t *testing.T) {
	result, _ := json.Marshal(map[string]string{
		"output": "```json\n{\"delivery_attribution_prediction\":\"unknown\",\"quality_prediction\":\"unknown\",\"prediction_reasons\":[],\"confidence\":null,\"workstream\":\"\",\"swarm_reviews\":[],\"ai_shelved_cls\":[],\"swarm_change_cls\":[],\"swarm_committed_cls\":[],\"external_committed_cls\":[],\"summary\":\"\",\"warnings\":[]}\n```",
	})

	if _, err := parseP4AssessmentTaskOutput(result); err != nil {
		t.Fatalf("parse fenced JSON: %v", err)
	}
}

func TestParseP4AssessmentTaskOutputRejectsInvalidShape(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{
			name:   "prose around fenced block",
			output: "Here is the result:\n```json\n{\"delivery_attribution_prediction\":\"unknown\",\"quality_prediction\":\"unknown\"}\n```",
		},
		{
			name:   "multiple fenced blocks",
			output: "```json\n{\"delivery_attribution_prediction\":\"unknown\"}\n```\n```json\n{\"quality_prediction\":\"unknown\"}\n```",
		},
		{
			name:   "trailing prose after object",
			output: "{\"delivery_attribution_prediction\":\"unknown\",\"quality_prediction\":\"unknown\"}\nDone.",
		},
		{
			name:   "bad delivery enum",
			output: "{\"delivery_attribution_prediction\":\"sure\",\"quality_prediction\":\"unknown\"}",
		},
		{
			name:   "bad quality enum",
			output: "{\"delivery_attribution_prediction\":\"unknown\",\"quality_prediction\":\"fine\"}",
		},
		{
			name:   "swarm reviews not array",
			output: "{\"delivery_attribution_prediction\":\"unknown\",\"quality_prediction\":\"unknown\",\"swarm_reviews\":{}}",
		},
		{
			name:   "evidence not object",
			output: "{\"delivery_attribution_prediction\":\"unknown\",\"quality_prediction\":\"unknown\",\"evidence\":[]}",
		},
		{
			name:   "warnings not array",
			output: "{\"delivery_attribution_prediction\":\"unknown\",\"quality_prediction\":\"unknown\",\"warnings\":{}}",
		},
		{
			name:   "non integer cl",
			output: "{\"delivery_attribution_prediction\":\"unknown\",\"quality_prediction\":\"unknown\",\"ai_shelved_cls\":[1.5]}",
		},
		{
			name:   "confidence out of range",
			output: "{\"delivery_attribution_prediction\":\"unknown\",\"quality_prediction\":\"unknown\",\"confidence\":1.5}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := json.Marshal(map[string]string{"output": tt.output})
			if _, err := parseP4AssessmentTaskOutput(result); err == nil {
				t.Fatalf("expected parse error for %s", tt.name)
			}
		})
	}
}

func TestFeishuProjectExternalFieldsKeepsSubmitRecord(t *testing.T) {
	item := FeishuProjectWorkItem{
		FieldValues: map[string][]string{
			"field_6e908d": {"CL 123456"},
			"提交记录":         {"CL 234567"},
			"开发分支":         {"rel_1.7.2"},
		},
	}

	fields := feishuProjectExternalFields(item)
	if !strings.Contains(fields["提交记录"], "CL 123456") || !strings.Contains(fields["提交记录"], "CL 234567") {
		t.Fatalf("提交记录 = %q", fields["提交记录"])
	}
	if fields["开发分支"] != "rel_1.7.2" {
		t.Fatalf("开发分支 = %q", fields["开发分支"])
	}
}

func TestP4EvidenceTaskProjectionDoesNotExposeRawTaskInternals(t *testing.T) {
	taskID := mustTestUUID(t, "00000000-0000-0000-0000-000000000001")
	agentID := mustTestUUID(t, "00000000-0000-0000-0000-000000000002")
	issueID := mustTestUUID(t, "00000000-0000-0000-0000-000000000003")
	ts := pgtype.Timestamptz{Time: time.Date(2026, 6, 29, 3, 4, 5, 0, time.UTC), Valid: true}

	rows := []db.ListP4EvidenceTasksByIssueRow{{
		ID:             taskID,
		AgentID:        agentID,
		IssueID:        issueID,
		Status:         "failed",
		CreatedAt:      ts,
		StartedAt:      ts,
		CompletedAt:    ts,
		FailureReason:  pgtype.Text{String: "agent_error", Valid: true},
		Error:          pgtype.Text{String: "short failure", Valid: true},
		IsP4Assessment: true,
	}}

	got := p4EvidenceTaskMaps(rows)
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	task := got[0]
	for _, key := range []string{"result", "context", "work_dir", "session_id", "runtime_id"} {
		if _, ok := task[key]; ok {
			t.Fatalf("task projection leaked %q: %#v", key, task)
		}
	}
	if task["id"] != util.UUIDToString(taskID) || task["agent_id"] != util.UUIDToString(agentID) {
		t.Fatalf("task ids = %#v", task)
	}
	if task["is_p4_assessment"] != true || task["failure_reason"] != "agent_error" || task["error"] != "short failure" {
		t.Fatalf("task summary = %#v", task)
	}
	if task["completed_at"] != "2026-06-29T03:04:05Z" {
		t.Fatalf("completed_at = %#v", task["completed_at"])
	}
}

func TestP4EvidenceReviewProjectionKeepsSwarmAndCLFields(t *testing.T) {
	reviewID := mustTestUUID(t, "00000000-0000-0000-0000-000000000004")
	ts := pgtype.Timestamptz{Time: time.Date(2026, 6, 29, 4, 5, 6, 0, time.UTC), Valid: true}
	rows := []db.PerforceReview{{
		ID:              reviewID,
		ReviewID:        267641,
		Title:           "WAR-7512 fix",
		State:           "needsReview",
		HtmlUrl:         "https://swarm/reviews/267641",
		Author:          pgtype.Text{String: "svr_ci", Valid: true},
		ShelvedCl:       pgtype.Int4{Int32: 267639, Valid: true},
		CommittedCl:     pgtype.Int4{},
		Changes:         []int32{267639, 267642},
		Commits:         []int32{},
		SwarmBranch:     pgtype.Text{String: "main", Valid: true},
		EventType:       pgtype.Text{String: "review.updated", Valid: true},
		SentAt:          ts,
		RawPayload:      []byte(`{"event_type":"review.updated","review":{"id":267641,"changes":[267639,267642],"commits":[]}}`),
		ReviewCreatedAt: ts,
		ReviewUpdatedAt: ts,
	}}

	got := p4EvidenceReviewMaps(rows)
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	review := got[0]
	if review["review_id"] != int64(267641) || review["state"] != "needsReview" || review["shelved_cl"] != int32(267639) {
		t.Fatalf("review evidence = %#v", review)
	}
	if review["committed_cl"] != nil {
		t.Fatalf("committed_cl = %#v, want nil", review["committed_cl"])
	}
	if got, ok := review["changes"].([]int32); !ok || len(got) != 2 || got[0] != 267639 || got[1] != 267642 {
		t.Fatalf("changes = %#v", review["changes"])
	}
	if review["swarm_branch"] != "main" || review["event_type"] != "review.updated" || review["sent_at"] != "2026-06-29T04:05:06Z" {
		t.Fatalf("complete review evidence missing: %#v", review)
	}
	raw, ok := review["raw_payload"].(map[string]any)
	if !ok || raw["event_type"] != "review.updated" {
		t.Fatalf("raw_payload = %#v", review["raw_payload"])
	}
}

func mustTestUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	id, err := util.ParseUUID(s)
	if err != nil {
		t.Fatalf("parse uuid %s: %v", s, err)
	}
	return id
}
