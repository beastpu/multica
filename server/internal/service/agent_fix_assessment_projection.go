package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Projection of the assessment queue onto derived agent_work issues
// (docs/agent-fix-p4-assessment-issue-design.md, Part 2). The queue table
// `agent_fix_p4_assessment` stays the single source of truth — the issue is a
// user-facing narrative carrier only, and every KPI keeps reading the queue.
//
// Queue-row transition → issue status mapping:
//
//	Trigger/Upsert → pending   | create new issue (todo)
//	Lease → running            | in_progress
//	Release (evidence failed)  | todo + server failure comment
//	Complete                   | done + server result-summary comment
//	Fail                       | cancelled + server failure comment
//	force re-run               | old issue keeps its state; new issue, repoint
//
// The design table says "failed", but issue.status has no such value
// (CHECK: backlog/todo/in_progress/in_review/done/blocked/cancelled), so a
// failed run maps to 'cancelled' — the closest terminal non-success state —
// with the failure comment carrying last_error for the actual reason.
//
// Rows whose assessment_issue_id is NULL (runs that predate the projection)
// are skipped harmlessly everywhere.

// P4 assessment trigger origins, recorded as agent_work.trigger.
const (
	P4AssessmentTriggerManual     = "manual"
	P4AssessmentTriggerScan       = "scan"
	P4AssessmentTriggerForceRerun = "force_rerun"
)

// P4AssessmentActor describes who/what triggered an assessment run. It
// becomes the projection issue's creator (issue.creator_type CHECK only
// allows member/agent — there is no 'system') and the agent_work.trigger
// value. A zero-value actor means "no resolvable creator": the assessment
// still enqueues, it just gets no projection issue for this run.
type P4AssessmentActor struct {
	Trigger     string // manual | scan | force_rerun; derived from force when empty
	CreatorType string // "member" | "agent"
	CreatorID   pgtype.UUID
}

var agentWorkNonAlpha = regexp.MustCompile(`[^a-zA-Z]`)

// workspaceIssuePrefix mirrors handler.generateIssuePrefix (the handler
// package imports this one, so the helper cannot be shared without a cycle):
// explicit prefix if set, else 2-3 uppercase letters from the workspace name.
func workspaceIssuePrefix(ws db.Workspace) string {
	if ws.IssuePrefix != "" {
		return ws.IssuePrefix
	}
	letters := agentWorkNonAlpha.ReplaceAllString(ws.Name, "")
	if letters == "" {
		return "WS"
	}
	letters = strings.ToUpper(letters)
	if len(letters) > 3 {
		letters = letters[:3]
	}
	return letters
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// createAssessmentProjectionIssue creates the derived issue for one
// assessment run and points the queue row at it. Runs inside Trigger's
// transaction so a failed insert rolls the whole trigger back. On force
// re-run the previous issue is left untouched (its terminal state is the run
// history); SetP4AssessmentIssue repoints the row to the new issue.
//
// assigneeID is the workspace's p4_assessment capability agent (plan C-1);
// the caller creates the native task hanging on this issue in the same
// transaction. A zero assigneeID (legacy env-allowlist path) leaves the issue
// unassigned.
func (s *P4AssessmentService) createAssessmentProjectionIssue(
	ctx context.Context,
	q *db.Queries,
	workspaceID, bindingID pgtype.UUID,
	row db.GetP4AssessmentBindingRow,
	actor P4AssessmentActor,
	assigneeID pgtype.UUID,
) (pgtype.UUID, error) {
	if actor.CreatorType == "" || !actor.CreatorID.Valid {
		// Conservative: a scan path without a resolvable creator (e.g. a
		// legacy integration missing created_by_id) still enqueues the
		// assessment; the run simply has no projection issue, exactly like
		// historical rows.
		return pgtype.UUID{}, nil
	}
	projectID, err := ensureAgentWorkProject(ctx, q, workspaceID, AgentWorkKindP4Assessment, "P4 评估")
	if err != nil {
		return pgtype.UUID{}, err
	}
	ws, err := q.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	src, err := q.GetIssue(ctx, row.IssueID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	number, err := q.IncrementIssueCounter(ctx, workspaceID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	metadata, err := agentWorkIssueMetadata(AgentWorkMetadata{
		Kind:          AgentWorkKindP4Assessment,
		SourceIssueID: util.UUIDToString(row.IssueID),
		SourceRef:     util.UUIDToString(bindingID),
		Trigger:       actor.Trigger,
		Extra:         map[string]string{"prompt_version": P4AssessmentPromptVersion},
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	identifier := fmt.Sprintf("%s-%d", workspaceIssuePrefix(ws), src.Number)
	var assigneeType pgtype.Text
	if assigneeID.Valid {
		assigneeType = pgtype.Text{String: "agent", Valid: true}
	}
	issue, err := q.CreateAgentWorkIssue(ctx, db.CreateAgentWorkIssueParams{
		WorkspaceID:  workspaceID,
		Title:        fmt.Sprintf("评估 %s【%s】%s", identifier, row.WorkItemID, truncateRunes(src.Title, 60)),
		Description:  pgtype.Text{String: p4AssessmentIssueDescription(identifier, src.Title, row, actor.Trigger), Valid: true},
		Status:       "todo",
		Priority:     "none",
		CreatorType:  actor.CreatorType,
		CreatorID:    actor.CreatorID,
		Number:       number,
		ProjectID:    projectID,
		Metadata:     metadata,
		AssigneeType: assigneeType,
		AssigneeID:   assigneeID,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	if err := q.SetP4AssessmentIssue(ctx, db.SetP4AssessmentIssueParams{
		WorkspaceID:       workspaceID,
		FeishuBindingID:   bindingID,
		AssessmentIssueID: issue.ID,
	}); err != nil {
		return pgtype.UUID{}, err
	}
	return issue.ID, nil
}

func p4AssessmentIssueDescription(identifier, srcTitle string, row db.GetP4AssessmentBindingRow, trigger string) string {
	var b strings.Builder
	b.WriteString("本 issue 是一次 P4 交付评估运行的投影（`agent_work.kind = p4_assessment`）。\n\n")
	fmt.Fprintf(&b, "- 来源 issue：%s %s\n", identifier, srcTitle)
	if row.ExternalIdentifier != "" {
		if url := textString(row.ExternalUrl); url != "" {
			fmt.Fprintf(&b, "- 外部单：[%s](%s)\n", row.ExternalIdentifier, url)
		} else {
			fmt.Fprintf(&b, "- 外部单：%s\n", row.ExternalIdentifier)
		}
	}
	fmt.Fprintf(&b, "- 触发方式：%s\n", trigger)
	fmt.Fprintf(&b, "- prompt version：%s\n\n", P4AssessmentPromptVersion)
	b.WriteString("状态由服务端按评估队列自动投影（todo → in_progress → done / cancelled），请勿手工修改状态或指派。评估智能体的过程叙事与服务端结果摘要会以评论追加在本 issue。")
	return b.String()
}

// projectAgentWorkIssueStatus projects a queue-row transition onto the
// derived issue. A NULL issue id (historical rows) is a silent no-op, and the
// query's jsonb_exists(metadata, 'agent_work') guard turns a stale or wrong
// id into 0 affected rows instead of mutating an ordinary issue.
func projectAgentWorkIssueStatus(ctx context.Context, q *db.Queries, workspaceID, issueID pgtype.UUID, status string) error {
	if !issueID.Valid {
		return nil
	}
	_, err := q.UpdateAgentWorkIssueStatus(ctx, db.UpdateAgentWorkIssueStatusParams{
		ID:          issueID,
		Status:      status,
		WorkspaceID: workspaceID,
	})
	return err
}

// postAgentWorkComment writes a server-side comment on the projection issue,
// authored as the worker task's agent (comment.author_type CHECK only allows
// member/agent; the leased task is the only agent identity available here).
// Best-effort by contract: it runs outside the queue transaction and never
// propagates errors — narration must not fail or roll back a queue
// transition. Skipped when the issue is NULL or the task/agent is unknown.
func (s *P4AssessmentService) postAgentWorkComment(ctx context.Context, workspaceID, taskID, issueID pgtype.UUID, content string) {
	if !issueID.Valid || content == "" || !taskID.Valid {
		return
	}
	task, err := s.Queries.GetAgentTask(ctx, taskID)
	if err != nil {
		slog.Warn("p4 assessment projection: comment author lookup failed",
			"task_id", util.UUIDToString(taskID), "error", err)
		return
	}
	if _, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:      issueID,
		WorkspaceID:  workspaceID,
		AuthorType:   "agent",
		AuthorID:     task.AgentID,
		Content:      content,
		Type:         "comment",
		SourceTaskID: taskID,
	}); err != nil {
		slog.Warn("p4 assessment projection: result comment failed",
			"issue_id", util.UUIDToString(issueID), "error", err)
	}
}

// p4AssessmentResultComment renders the machine-written result summary from
// the already-validated result payload — every projection issue gets at least
// one structured record of the outcome even if the agent never narrates.
func p4AssessmentResultComment(parsed p4AssessmentOutput) string {
	var b strings.Builder
	b.WriteString("**P4 评估结果**\n\n")
	fmt.Fprintf(&b, "- 交付归因：`%s`\n", parsed.DeliveryAttributionPrediction)
	fmt.Fprintf(&b, "- 质量判断：`%s`\n", parsed.QualityPrediction)
	if parsed.Confidence != nil {
		fmt.Fprintf(&b, "- 置信度：%.2f\n", *parsed.Confidence)
	}
	if parsed.Workstream != "" {
		fmt.Fprintf(&b, "- workstream：%s\n", parsed.Workstream)
	}
	appendCLLine(&b, "AI shelve CL", parsed.AIShelvedCLs)
	appendCLLine(&b, "Swarm change CL", parsed.SwarmChangeCLs)
	appendCLLine(&b, "Swarm committed CL", parsed.SwarmCommittedCLs)
	appendCLLine(&b, "External committed CL", parsed.ExternalCommittedCLs)
	var warnings []string
	_ = json.Unmarshal(parsed.Warnings, &warnings)
	if len(warnings) > 0 {
		fmt.Fprintf(&b, "- warnings：%s\n", strings.Join(warnings, ", "))
	}
	if len(parsed.PredictionReasons) > 0 {
		b.WriteString("\n判断依据：\n")
		for _, reason := range parsed.PredictionReasons {
			fmt.Fprintf(&b, "- %s\n", reason)
		}
	}
	if parsed.Summary != "" {
		b.WriteString("\n摘要：\n")
		b.WriteString(parsed.Summary)
	}
	return b.String()
}

func appendCLLine(b *strings.Builder, label string, cls []int32) {
	if len(cls) == 0 {
		return
	}
	parts := make([]string, 0, len(cls))
	for _, cl := range cls {
		parts = append(parts, fmt.Sprintf("%d", cl))
	}
	fmt.Fprintf(b, "- %s：%s\n", label, strings.Join(parts, ", "))
}

// p4AssessmentFailureComment renders the machine-written failure/release
// record; lastError is the same operator-facing one-liner stored in the
// queue row's last_error column.
func p4AssessmentFailureComment(stage, lastError string) string {
	var b strings.Builder
	b.WriteString("**P4 评估未完成**\n\n")
	fmt.Fprintf(&b, "- 阶段：%s\n", stage)
	if lastError != "" {
		fmt.Fprintf(&b, "- 原因：%s\n", lastError)
	}
	return b.String()
}
