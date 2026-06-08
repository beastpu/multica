package main

import (
	"strings"
	"testing"
	"time"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestAgentIssueSummaryRunDate(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)

	beforeHour := time.Date(2026, 6, 8, 8, 59, 0, 0, loc)
	if got := agentIssueSummaryRunDate(beforeHour, loc, 9); got != "" {
		t.Fatalf("before summary hour: got %q, want empty", got)
	}

	afterHour := time.Date(2026, 6, 8, 9, 0, 0, 0, loc)
	if got := agentIssueSummaryRunDate(afterHour, loc, 9); got != "2026-06-07" {
		t.Fatalf("after summary hour: got %q, want previous day", got)
	}
}

func TestFormatAgentIssueSummaryBody(t *testing.T) {
	body := formatAgentIssueSummaryBody(db.ListAgentIssueDailySummariesRow{
		ActiveCount:     6,
		TodoCount:       2,
		InProgressCount: 1,
		InReviewCount:   1,
		BlockedCount:    1,
		BacklogCount:    1,
		TasksStarted:    4,
		TasksCompleted:  3,
		TasksFailed:     1,
		ClosedInWindow:  2,
	}, "2026-06-07")

	for _, want := range []string{
		"2026-06-07 处理状态统计",
		"当前未完成 Issue：6",
		"昨日任务：新建 4，完成 3，失败 1",
		"昨日关闭 Issue：2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}
