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

func TestP4AssessmentHandoffNoteDefinesHumanSubmissionAttribution(t *testing.T) {
	note := p4AssessmentHandoffNote(pgtype.UUID{})
	for _, want := range []string{
		"human-submitted final CL",
		"materially followed a verified, method-equivalent AI implementation",
		"regardless of whether the AI output was produced before or after the human submission",
		"use \"ai_assisted\"",
		"materially different implementation",
		"use \"human_delivered\"",
		"use \"unknown\"",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("handoff note missing %q", want)
		}
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

func TestP4EvidenceCandidatesPreferStructuredFlowMetadata(t *testing.T) {
	metadata := map[string]any{
		"flow_cl":     float64(283198),
		"flow_review": float64(283199),
		"flow_locked": true,
	}
	fields := map[string]any{
		"提交记录": "CL 283198",
	}
	comments := []map[string]any{{
		"id":      "comment-1",
		"content": "Shelved CL: 283198\nSwarm Review: http://w3-swarm.lilithgame.com/reviews/283199",
	}}
	externalComments := []map[string]any{{
		"id":      "external-comment-1",
		"content": "ChangeList: 283198 --Submitted\nSwarm Review: [#283199](http://w3-swarm.lilithgame.com/reviews/283199)",
	}}
	reviews := []map[string]any{{
		"id":         "review-row-1",
		"review_id":  int64(283199),
		"shelved_cl": int32(283198),
		"changes":    []int32{283198},
		"commits":    []int32{},
	}}

	cls := p4EvidenceCLCandidates(metadata, fields, comments, externalComments, reviews)
	if len(cls) != 1 {
		t.Fatalf("cl candidates = %#v", cls)
	}
	if cls[0]["value"] != int64(283198) || cls[0]["source"] != "issue_metadata" || cls[0]["field"] != "flow_cl" {
		t.Fatalf("cl candidate = %#v", cls[0])
	}

	swarmReviews := p4EvidenceReviewCandidates(metadata, fields, comments, externalComments, reviews)
	if len(swarmReviews) != 1 {
		t.Fatalf("review candidates = %#v", swarmReviews)
	}
	if swarmReviews[0]["value"] != int64(283199) || swarmReviews[0]["source"] != "issue_metadata" || swarmReviews[0]["field"] != "flow_review" {
		t.Fatalf("review candidate = %#v", swarmReviews[0])
	}
}

func TestP4EvidenceCandidatesIncludeFeishuProjectComments(t *testing.T) {
	externalComments := []map[string]any{{
		"id":      "external-comment-1",
		"content": "ChangeList: 283198 --Submitted\nSwarm Review: [#283199](http://w3-swarm.lilithgame.com/reviews/283199)",
	}}

	cls := p4EvidenceCLCandidates(map[string]any{}, map[string]any{}, nil, externalComments, nil)
	if len(cls) != 1 || cls[0]["value"] != int64(283198) || cls[0]["source"] != "feishu_project_comment" {
		t.Fatalf("cl candidates = %#v", cls)
	}
	reviews := p4EvidenceReviewCandidates(map[string]any{}, map[string]any{}, nil, externalComments, nil)
	if len(reviews) != 1 || reviews[0]["value"] != int64(283199) || reviews[0]["source"] != "feishu_project_comment" {
		t.Fatalf("review candidates = %#v", reviews)
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

func TestValidateP4AssessmentPayload(t *testing.T) {
	valid := `{
		"delivery_attribution_prediction":"human_delivered",
		"quality_prediction":"likely_wrong",
		"prediction_reasons":["r1"],
		"confidence":0.8,
		"workstream":"rel_1.1.0",
		"swarm_reviews":[],
		"ai_shelved_cls":[280120],
		"swarm_change_cls":[],
		"swarm_committed_cls":[],
		"external_committed_cls":[278969],
		"evidence":{},
		"summary":"ok",
		"warnings":["w1"],
		"model":"gpt-5-codex"
	}`
	out, err := validateP4AssessmentPayload([]byte(valid))
	if err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if out.DeliveryAttributionPrediction != "human_delivered" || out.QualityPrediction != "likely_wrong" {
		t.Fatalf("predictions = %q/%q", out.DeliveryAttributionPrediction, out.QualityPrediction)
	}
	if len(out.ExternalCommittedCLs) != 1 || out.ExternalCommittedCLs[0] != 278969 {
		t.Fatalf("external_committed_cls = %#v", out.ExternalCommittedCLs)
	}

	// Empty predictions default to unknown (agent may omit them on no evidence).
	minimal, err := validateP4AssessmentPayload([]byte(`{"summary":"none"}`))
	if err != nil {
		t.Fatalf("minimal payload rejected: %v", err)
	}
	if minimal.DeliveryAttributionPrediction != "unknown" || minimal.QualityPrediction != "unknown" {
		t.Fatalf("minimal defaults = %q/%q", minimal.DeliveryAttributionPrediction, minimal.QualityPrediction)
	}

	bad := []struct {
		name    string
		payload string
	}{
		{"invalid delivery enum", `{"delivery_attribution_prediction":"teleported"}`},
		{"invalid quality enum", `{"quality_prediction":"perfect"}`},
		{"confidence out of range", `{"confidence":1.5}`},
		{"unknown field", `{"nope":1}`},
		{"swarm_reviews not array", `{"swarm_reviews":"unknown"}`},
		{"warnings not array", `{"warnings":{}}`},
		{"evidence not object", `{"evidence":[]}`},
		{"trailing json", `{}{}`},
		{"not an object", `[]`},
	}
	for _, tc := range bad {
		if _, err := validateP4AssessmentPayload([]byte(tc.payload)); err == nil {
			t.Errorf("%s: expected rejection, got none", tc.name)
		}
	}
}

// The result comment shows Chinese labels with the machine code retained,
// and degrades to the raw code on enum drift instead of crashing or hiding
// the value.
func TestP4AssessmentResultCommentEnumZh(t *testing.T) {
	got := p4AssessmentResultComment(p4AssessmentOutput{
		DeliveryAttributionPrediction: "human_delivered",
		QualityPrediction:             "likely_wrong",
	})
	if !strings.Contains(got, "- 交付归因：人工提交（human_delivered）\n") {
		t.Errorf("delivery line missing zh label, got:\n%s", got)
	}
	if !strings.Contains(got, "- 质量判断：不通过（likely_wrong）\n") {
		t.Errorf("quality line missing zh label, got:\n%s", got)
	}

	// Enum drift: an unmapped code renders as-is.
	drift := p4AssessmentResultComment(p4AssessmentOutput{
		DeliveryAttributionPrediction: "teleported",
		QualityPrediction:             "unknown",
	})
	if !strings.Contains(drift, "- 交付归因：teleported\n") {
		t.Errorf("unknown enum should render raw, got:\n%s", drift)
	}
	if !strings.Contains(drift, "- 质量判断：无法判断（unknown）\n") {
		t.Errorf("unknown quality code should map to 无法判断, got:\n%s", drift)
	}
}
