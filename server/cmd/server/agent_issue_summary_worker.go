package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const agentIssueSummaryType = "agent_issue_summary"

type agentIssueSummaryConfig struct {
	Enabled  bool
	Timezone string
	Hour     int
	Interval time.Duration
}

func defaultAgentIssueSummaryConfig() agentIssueSummaryConfig {
	return agentIssueSummaryConfig{
		Enabled:  false,
		Timezone: "Asia/Shanghai",
		Hour:     9,
		Interval: time.Hour,
	}
}

func envAgentIssueSummaryConfig() agentIssueSummaryConfig {
	cfg := defaultAgentIssueSummaryConfig()
	if v := strings.TrimSpace(os.Getenv("AGENT_ISSUE_SUMMARY_ENABLED")); v != "" {
		cfg.Enabled = v != "0" && !strings.EqualFold(v, "false")
	}
	if v := strings.TrimSpace(os.Getenv("AGENT_ISSUE_SUMMARY_TIMEZONE")); v != "" {
		cfg.Timezone = v
	}
	if v := strings.TrimSpace(os.Getenv("AGENT_ISSUE_SUMMARY_HOUR")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 23 {
			cfg.Hour = n
		}
	}
	return cfg
}

func runAgentIssueSummaryWorker(ctx context.Context, queries *db.Queries, pool *pgxpool.Pool, bus *events.Bus, cfg agentIssueSummaryConfig) {
	if !cfg.Enabled || cfg.Interval <= 0 {
		return
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		slog.Warn("agent issue summary: invalid timezone, falling back to UTC", "timezone", cfg.Timezone, "error", err)
		loc = time.UTC
	}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	lastRunDate := ""
	for {
		now := time.Now()
		runDate := agentIssueSummaryRunDate(now, loc, cfg.Hour)
		if runDate != "" && runDate != lastRunDate {
			if err := runAgentIssueSummaryOnce(ctx, queries, pool, bus, runDate, loc); err != nil {
				slog.Warn("agent issue summary: run failed", "date", runDate, "error", err)
			} else {
				lastRunDate = runDate
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func agentIssueSummaryRunDate(now time.Time, loc *time.Location, hour int) string {
	local := now.In(loc)
	if local.Hour() < hour {
		return ""
	}
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	return day.Format(time.DateOnly)
}

func runAgentIssueSummaryOnce(ctx context.Context, queries *db.Queries, pool *pgxpool.Pool, bus *events.Bus, date string, loc *time.Location) error {
	day, err := time.ParseInLocation(time.DateOnly, date, loc)
	if err != nil {
		return fmt.Errorf("parse date: %w", err)
	}
	start := pgtype.Timestamptz{Time: day, Valid: true}
	end := pgtype.Timestamptz{Time: day.AddDate(0, 0, 1), Valid: true}

	rows, err := queries.ListAgentIssueDailySummaries(ctx, db.ListAgentIssueDailySummariesParams{
		WindowStart: start,
		WindowEnd:   end,
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !row.OwnerID.Valid {
			continue
		}
		if agentIssueSummaryMuted(ctx, queries, row) {
			continue
		}
		locked, unlock, err := tryAgentIssueSummaryLock(ctx, pool, row, date)
		if err != nil {
			slog.Warn("agent issue summary: lock failed",
				"workspace_id", util.UUIDToString(row.WorkspaceID),
				"agent_id", util.UUIDToString(row.AgentID),
				"date", date,
				"error", err,
			)
			continue
		}
		if !locked {
			continue
		}
		if err := createAgentIssueSummaryInbox(ctx, queries, bus, row, date); err != nil {
			slog.Warn("agent issue summary: inbox write failed",
				"workspace_id", util.UUIDToString(row.WorkspaceID),
				"agent_id", util.UUIDToString(row.AgentID),
				"date", date,
				"error", err,
			)
		}
		unlock()
	}
	return nil
}

func agentIssueSummaryMuted(ctx context.Context, queries *db.Queries, row db.ListAgentIssueDailySummariesRow) bool {
	ownerID := util.UUIDToString(row.OwnerID)
	prefs := loadUserPrefs(ctx, queries, util.UUIDToString(row.WorkspaceID), []string{ownerID})
	if p, ok := prefs[ownerID]; ok && isNotifMuted(p, agentIssueSummaryType) {
		return true
	}
	return false
}

func tryAgentIssueSummaryLock(ctx context.Context, pool *pgxpool.Pool, row db.ListAgentIssueDailySummariesRow, date string) (bool, func(), error) {
	if pool == nil {
		return true, func() {}, nil
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, nil, err
	}
	key := fmt.Sprintf("agent_issue_summary:%s:%s:%s",
		util.UUIDToString(row.WorkspaceID),
		util.UUIDToString(row.AgentID),
		date,
	)
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", key).Scan(&locked); err != nil {
		conn.Release()
		return false, nil, err
	}
	if !locked {
		conn.Release()
		return false, func() {}, nil
	}
	return true, func() {
		if _, err := conn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", key); err != nil {
			slog.Warn("agent issue summary: unlock failed", "lock_key", key, "error", err)
		}
		conn.Release()
	}, nil
}

func createAgentIssueSummaryInbox(ctx context.Context, queries *db.Queries, bus *events.Bus, row db.ListAgentIssueDailySummariesRow, date string) error {
	dedupe, err := json.Marshal(map[string]any{
		"kind":     "agent_issue_daily_summary",
		"date":     date,
		"agent_id": util.UUIDToString(row.AgentID),
	})
	if err != nil {
		return err
	}
	existing, err := queries.CountAgentIssueSummaryInbox(ctx, db.CountAgentIssueSummaryInboxParams{
		WorkspaceID:   row.WorkspaceID,
		RecipientID:   row.OwnerID,
		ActorID:       row.AgentID,
		DetailsFilter: dedupe,
	})
	if err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}

	details, err := json.Marshal(agentIssueSummaryDetails(row, date))
	if err != nil {
		return err
	}
	item, err := queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
		WorkspaceID:   row.WorkspaceID,
		RecipientType: "member",
		RecipientID:   row.OwnerID,
		Type:          agentIssueSummaryType,
		Severity:      "info",
		IssueID:       pgtype.UUID{},
		Title:         fmt.Sprintf("智能体日报：%s（%s）", row.AgentName, date),
		Body:          util.StrToText(formatAgentIssueSummaryBody(row, date)),
		ActorType:     util.StrToText("agent"),
		ActorID:       row.AgentID,
		Details:       details,
	})
	if err != nil {
		return err
	}
	bus.Publish(events.Event{
		Type:        protocol.EventInboxNew,
		WorkspaceID: util.UUIDToString(row.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(row.AgentID),
		Payload:     map[string]any{"item": inboxItemToResponse(item)},
	})
	return nil
}

func agentIssueSummaryDetails(row db.ListAgentIssueDailySummariesRow, date string) map[string]any {
	return map[string]any{
		"kind":              "agent_issue_daily_summary",
		"date":              date,
		"agent_id":          util.UUIDToString(row.AgentID),
		"agent_name":        row.AgentName,
		"active_count":      row.ActiveCount,
		"backlog_count":     row.BacklogCount,
		"todo_count":        row.TodoCount,
		"in_progress_count": row.InProgressCount,
		"in_review_count":   row.InReviewCount,
		"blocked_count":     row.BlockedCount,
		"closed_in_window":  row.ClosedInWindow,
		"tasks_started":     row.TasksStarted,
		"tasks_completed":   row.TasksCompleted,
		"tasks_failed":      row.TasksFailed,
	}
}

func formatAgentIssueSummaryBody(row db.ListAgentIssueDailySummariesRow, date string) string {
	return fmt.Sprintf(
		"%s 处理状态统计\n\n当前未完成 Issue：%d（待办 %d / 进行中 %d / 评审 %d / 阻塞 %d / backlog %d）\n昨日任务：新建 %d，完成 %d，失败 %d\n昨日关闭 Issue：%d",
		date,
		row.ActiveCount,
		row.TodoCount,
		row.InProgressCount,
		row.InReviewCount,
		row.BlockedCount,
		row.BacklogCount,
		row.TasksStarted,
		row.TasksCompleted,
		row.TasksFailed,
		row.ClosedInWindow,
	)
}
