package service

import (
	"encoding/json"
	"strings"
	"testing"
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
