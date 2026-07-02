package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	P4AssessmentTaskType      = "agent_fix_p4_assessment"
	P4AssessmentPromptVersion = "p4-assessment-v1"
	P4AssessmentBackfillLimit = 50
)

type P4AssessmentService struct {
	Queries       *db.Queries
	TxStarter     TxStarter
	Task          *TaskService
	FeishuProject *FeishuProjectClient
}

type P4AssessmentTriggerResult struct {
	Assessment db.AgentFixP4Assessment
	Task       *db.AgentTaskQueue
	Created    bool
	Reason     string
}

type P4AssessmentBackfillResult struct {
	Scanned    int
	Eligible   int
	Triggered  int
	Skipped    int
	Failed     int
	LastReason string
}

type P4AssessmentEvidence struct {
	Binding                map[string]any   `json:"binding"`
	Issue                  map[string]any   `json:"issue"`
	Tasks                  []map[string]any `json:"tasks"`
	Comments               []map[string]any `json:"comments"`
	ExternalComments       []map[string]any `json:"external_comments"`
	ExternalEvidenceErrors []string         `json:"external_evidence_errors,omitempty"`
	PerforceReviews        []map[string]any `json:"perforce_reviews"`
	CLCandidates           []map[string]any `json:"cl_candidates"`
	ReviewCandidates       []map[string]any `json:"review_candidates"`
}

type p4AssessmentContext struct {
	Type            string `json:"type"`
	WorkspaceID     string `json:"workspace_id"`
	IssueID         string `json:"issue_id"`
	FeishuBindingID string `json:"feishu_binding_id"`
	Mode            string `json:"mode"`
	PromptVersion   string `json:"prompt_version"`
}

func NewP4AssessmentService(q *db.Queries, tx TxStarter, task *TaskService) *P4AssessmentService {
	return &P4AssessmentService{Queries: q, TxStarter: tx, Task: task}
}

func IsP4AssessmentTask(task db.AgentTaskQueue) bool {
	var ctx p4AssessmentContext
	return len(task.Context) > 0 &&
		json.Unmarshal(task.Context, &ctx) == nil &&
		ctx.Type == P4AssessmentTaskType
}

// p4AssessmentHandoffNote is the read-only assessment instruction the server
// stores on the task's handoff_note. A NEW daemon builds a dedicated assessment
// prompt from the task kind and ignores this; an OLD daemon (pre-isolation)
// treats the task as a normal assignment and renders handoff_note into the
// opening prompt — so this is the only server-side lever that steers a stale
// daemon into read-only JSON output without a client update.
//
// It is intentionally self-contained: an old runtime may not have synced the
// `multica-agent-fix-p4-assessment` skill, so the output contract (allowed
// enum values, exact key set, JSON-only) is inlined rather than deferred to the
// skill. The parser rejects unknown JSON keys, so the note lists the exact keys
// and forbids extras. Write-side pollution is separately blocked server-side
// (analysis tasks cannot mutate issues or post comments), so a stale daemon
// that ignores these instructions still cannot damage the issue.
func p4AssessmentHandoffNote(bindingID pgtype.UUID) string {
	binding := util.UUIDToString(bindingID)
	var b strings.Builder
	b.WriteString("THIS RUN IS A READ-ONLY P4/SWARM ASSESSMENT, NOT A FIX. ")
	b.WriteString("Do NOT modify code, and do NOT change the issue, comments, status, Feishu/Meego, P4, or Swarm. ")
	b.WriteString("Do not run any repair or feature-fix mission. The server rejects every write from this task, so any edit/comment/status attempt only wastes the run.\n\n")
	b.WriteString("Goal: judge the delivery attribution and quality of the completed external work item, then emit a single assessment JSON.\n\n")
	b.WriteString("Step 1 — read the task-scoped evidence (this is the only required Multica call):\n")
	fmt.Fprintf(&b, "  multica api get /api/operations/agent-fixes/%s/p4-evidence\n\n", binding)
	b.WriteString("Step 2 — you MAY inspect inner-network Swarm/P4 with read-only commands (e.g. `p4 describe -s`) when it helps classify CLs. Never mutate anything.\n")
	b.WriteString("If the `multica-agent-fix-p4-assessment` skill is available, follow it for CL role classification and the full schema.\n\n")
	b.WriteString("Step 3 — FINAL OUTPUT: print exactly one JSON object (or one fenced ```json block containing exactly one JSON object) and NOTHING else — no prose before or after. Unknown keys are rejected, so use only these keys:\n")
	b.WriteString("  delivery_attribution_prediction: one of \"ai_delivered\" | \"ai_assisted\" | \"human_delivered\" | \"conflict\" | \"unattributed\" | \"unknown\"\n")
	b.WriteString("  quality_prediction: one of \"likely_correct\" | \"likely_needs_changes\" | \"likely_wrong\" | \"unknown\"\n")
	b.WriteString("  prediction_reasons: array of short strings\n")
	b.WriteString("  confidence: number in [0,1] or null\n")
	b.WriteString("  workstream: string\n")
	b.WriteString("  swarm_reviews: array   ai_shelved_cls / swarm_change_cls / swarm_committed_cls / external_committed_cls: arrays of integers\n")
	b.WriteString("  evidence: object   summary: string   warnings: array of strings   model: string\n\n")
	b.WriteString("When evidence is missing or ambiguous, use \"unknown\" for the predictions and record why in warnings — never guess.\n")
	return b.String()
}

func (s *P4AssessmentService) Trigger(ctx context.Context, workspaceID, bindingID pgtype.UUID, force bool) (P4AssessmentTriggerResult, error) {
	var result P4AssessmentTriggerResult
	err := s.runInTx(ctx, func(q *db.Queries) error {
		if err := q.LockP4AssessmentBinding(ctx, db.LockP4AssessmentBindingParams{
			ID:          bindingID,
			WorkspaceID: workspaceID,
		}); err != nil {
			return err
		}
		row, err := q.GetP4AssessmentBinding(ctx, db.GetP4AssessmentBindingParams{
			ID:          bindingID,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			return err
		}
		if mapped := P4AssessmentMappedStatus(row.WorkItemType, row.ExternalStatusLabel.String, row.StatusMapping, row.WorkItemTypes); mapped != "done" {
			result = P4AssessmentTriggerResult{Reason: "external_status_not_done"}
			return nil
		}
		if !row.AssigneeType.Valid || row.AssigneeType.String != "agent" || !row.AssigneeID.Valid {
			result = P4AssessmentTriggerResult{Reason: "issue_has_no_agent_assignee"}
			return nil
		}
		if !row.AgentRuntimeID.Valid {
			result = P4AssessmentTriggerResult{Reason: "agent_has_no_runtime"}
			return nil
		}
		if row.AgentArchivedAt.Valid {
			result = P4AssessmentTriggerResult{Reason: "agent_archived"}
			return nil
		}

		existing, existingErr := q.GetP4AssessmentByBinding(ctx, db.GetP4AssessmentByBindingParams{
			WorkspaceID:     workspaceID,
			FeishuBindingID: bindingID,
		})
		if existingErr == nil && !force {
			switch existing.AssessmentStatus {
			case "pending", "running", "completed", "failed", "stale":
				result = P4AssessmentTriggerResult{Assessment: existing, Reason: "existing_assessment"}
				return nil
			}
		} else if existingErr == nil && force {
			switch existing.AssessmentStatus {
			case "pending", "running":
				result = P4AssessmentTriggerResult{Assessment: existing, Reason: "assessment_in_progress"}
				return nil
			}
		} else if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
			return existingErr
		}

		assessment, err := q.UpsertP4AssessmentPending(ctx, db.UpsertP4AssessmentPendingParams{
			WorkspaceID:     workspaceID,
			IssueID:         row.IssueID,
			FeishuBindingID: bindingID,
			PromptVersion:   P4AssessmentPromptVersion,
		})
		if err != nil {
			return err
		}

		taskContext, err := json.Marshal(p4AssessmentContext{
			Type:            P4AssessmentTaskType,
			WorkspaceID:     util.UUIDToString(workspaceID),
			IssueID:         util.UUIDToString(row.IssueID),
			FeishuBindingID: util.UUIDToString(bindingID),
			Mode:            "assess_only",
			PromptVersion:   P4AssessmentPromptVersion,
		})
		if err != nil {
			return err
		}
		task, err := q.CreateP4AssessmentTask(ctx, db.CreateP4AssessmentTaskParams{
			AgentID:     row.AssigneeID,
			RuntimeID:   row.AgentRuntimeID,
			IssueID:     row.IssueID,
			Priority:    int32(1),
			Context:     taskContext,
			HandoffNote: pgtype.Text{String: p4AssessmentHandoffNote(bindingID), Valid: true},
		})
		if err != nil {
			return err
		}
		assessment, err = q.SetP4AssessmentTask(ctx, db.SetP4AssessmentTaskParams{
			WorkspaceID:      workspaceID,
			FeishuBindingID:  bindingID,
			AssessmentTaskID: task.ID,
		})
		if err != nil {
			return err
		}
		result = P4AssessmentTriggerResult{Assessment: assessment, Task: &task, Created: true}
		return nil
	})
	if err != nil {
		return P4AssessmentTriggerResult{}, err
	}
	if result.Task != nil && s.Task != nil {
		s.Task.NotifyTaskEnqueued(ctx, *result.Task)
	}
	return result, nil
}

func (s *P4AssessmentService) BackfillDoneBindings(ctx context.Context, workspaceID, integrationID pgtype.UUID, limit int32) (P4AssessmentBackfillResult, error) {
	if limit <= 0 {
		limit = P4AssessmentBackfillLimit
	}
	rows, err := s.Queries.ListP4AssessmentBackfillBindings(ctx, db.ListP4AssessmentBackfillBindingsParams{
		WorkspaceID:   workspaceID,
		IntegrationID: integrationID,
		Limit:         limit,
	})
	if err != nil {
		return P4AssessmentBackfillResult{}, err
	}

	var out P4AssessmentBackfillResult
	for _, row := range rows {
		out.Scanned++
		if mapped := P4AssessmentMappedStatus(row.WorkItemType, row.ExternalStatusLabel.String, row.StatusMapping, row.WorkItemTypes); mapped != "done" {
			out.Skipped++
			out.LastReason = "external_status_not_done"
			continue
		}
		out.Eligible++
		result, err := s.Trigger(ctx, row.WorkspaceID, row.BindingID, false)
		if err != nil {
			out.Failed++
			out.LastReason = err.Error()
			continue
		}
		if result.Created {
			out.Triggered++
		} else {
			out.Skipped++
		}
		if result.Reason != "" {
			out.LastReason = result.Reason
		}
	}
	return out, nil
}

func (s *P4AssessmentService) runInTx(ctx context.Context, fn func(*db.Queries) error) error {
	if s.TxStarter == nil {
		return fn(s.Queries)
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *P4AssessmentService) Evidence(ctx context.Context, workspaceID, bindingID pgtype.UUID) (P4AssessmentEvidence, error) {
	row, err := s.Queries.GetP4AssessmentBinding(ctx, db.GetP4AssessmentBindingParams{
		ID:          bindingID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return P4AssessmentEvidence{}, err
	}
	fields := map[string]any{}
	_ = json.Unmarshal(row.ExternalFields, &fields)
	metadata := map[string]any{}
	_ = json.Unmarshal(row.IssueMetadata, &metadata)
	tasks, err := s.Queries.ListP4EvidenceTasksByIssue(ctx, db.ListP4EvidenceTasksByIssueParams{
		IssueID:     row.IssueID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return P4AssessmentEvidence{}, err
	}
	comments, err := s.Queries.ListP4EvidenceCommentsByIssue(ctx, db.ListP4EvidenceCommentsByIssueParams{
		IssueID:     row.IssueID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return P4AssessmentEvidence{}, err
	}
	reviews, err := s.Queries.ListReviewsByIssue(ctx, row.IssueID)
	if err != nil {
		return P4AssessmentEvidence{}, err
	}
	commentMaps := p4EvidenceCommentMaps(comments)
	reviewMaps := p4EvidenceReviewMaps(reviews)
	externalComments, externalErrors := s.p4EvidenceExternalComments(ctx, row)
	return P4AssessmentEvidence{
		Binding: map[string]any{
			"id":                    util.UUIDToString(row.BindingID),
			"work_item_type":        row.WorkItemType,
			"work_item_id":          row.WorkItemID,
			"external_identifier":   row.ExternalIdentifier,
			"external_url":          textString(row.ExternalUrl),
			"external_status_label": textString(row.ExternalStatusLabel),
			"mapped_status":         P4AssessmentMappedStatus(row.WorkItemType, row.ExternalStatusLabel.String, row.StatusMapping, row.WorkItemTypes),
			"external_fields":       fields,
		},
		Issue: map[string]any{
			"id":          util.UUIDToString(row.IssueID),
			"title":       row.IssueTitle,
			"status":      row.IssueStatus,
			"description": textString(row.IssueDescription),
			"metadata":    metadata,
		},
		Tasks:                  p4EvidenceTaskMaps(tasks),
		Comments:               commentMaps,
		ExternalComments:       externalComments,
		ExternalEvidenceErrors: externalErrors,
		PerforceReviews:        reviewMaps,
		CLCandidates:           p4EvidenceCLCandidates(metadata, fields, commentMaps, externalComments, reviewMaps),
		ReviewCandidates:       p4EvidenceReviewCandidates(metadata, fields, commentMaps, externalComments, reviewMaps),
	}, nil
}

func (s *P4AssessmentService) p4EvidenceExternalComments(ctx context.Context, row db.GetP4AssessmentBindingRow) ([]map[string]any, []string) {
	if s == nil || s.Queries == nil {
		return nil, nil
	}
	cfg, err := s.Queries.GetFeishuProjectIntegrationByID(ctx, row.IntegrationID)
	if err != nil {
		return nil, []string{"feishu_project_integration_unavailable: " + err.Error()}
	}
	client := s.FeishuProject
	if client == nil {
		client = NewFeishuProjectClient()
	}
	comments, err := client.ListWorkItemComments(ctx, cfg, row.WorkItemType, row.WorkItemID)
	if err != nil {
		return nil, []string{"feishu_project_comments_unavailable: " + err.Error()}
	}
	out := p4EvidenceExternalCommentMaps(comments, row.WorkItemType, row.WorkItemID, "binding")
	related, err := client.ListRelatedWorkItems(ctx, cfg, row.WorkItemType, row.WorkItemID)
	if err != nil {
		return out, []string{"feishu_project_related_work_items_unavailable: " + err.Error()}
	}
	var errs []string
	for _, item := range related {
		linkedComments, err := client.ListWorkItemComments(ctx, cfg, item.Type, item.ID)
		if err != nil {
			errs = append(errs, "feishu_project_linked_comments_unavailable: "+item.Type+"/"+item.ID+": "+err.Error())
			continue
		}
		out = append(out, p4EvidenceExternalCommentMaps(linkedComments, item.Type, item.ID, item.Source)...)
	}
	return out, errs
}

func p4EvidenceTaskMaps(tasks []db.ListP4EvidenceTasksByIssueRow) []map[string]any {
	out := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, map[string]any{
			"id":               util.UUIDToString(task.ID),
			"agent_id":         util.UUIDToString(task.AgentID),
			"status":           task.Status,
			"is_p4_assessment": task.IsP4Assessment,
			"failure_reason":   textString(task.FailureReason),
			"error":            textString(task.Error),
			"created_at":       timeString(task.CreatedAt),
			"started_at":       timeString(task.StartedAt),
			"completed_at":     timeString(task.CompletedAt),
		})
	}
	return out
}

func p4EvidenceCommentMaps(comments []db.ListP4EvidenceCommentsByIssueRow) []map[string]any {
	out := make([]map[string]any, 0, len(comments))
	for _, comment := range comments {
		out = append(out, map[string]any{
			"id":             util.UUIDToString(comment.ID),
			"author_type":    comment.AuthorType,
			"author_id":      util.UUIDToString(comment.AuthorID),
			"type":           comment.Type,
			"content":        comment.Content,
			"source_task_id": uuidStringOrEmpty(comment.SourceTaskID),
			"created_at":     timeString(comment.CreatedAt),
		})
	}
	return out
}

func p4EvidenceExternalCommentMaps(comments []FeishuProjectComment, workItemType, workItemID, source string) []map[string]any {
	out := make([]map[string]any, 0, len(comments))
	for _, comment := range comments {
		out = append(out, map[string]any{
			"id":             comment.ID,
			"work_item_type": workItemType,
			"work_item_id":   workItemID,
			"source":         source,
			"operator":       comment.Operator,
			"content":        comment.Content,
			"created_at":     timeString(pgtype.Timestamptz{Time: comment.CreatedAt, Valid: !comment.CreatedAt.IsZero()}),
		})
	}
	return out
}

func p4EvidenceCLCandidates(metadata map[string]any, fields map[string]any, comments []map[string]any, externalComments []map[string]any, reviews []map[string]any) []map[string]any {
	seen := map[int64]bool{}
	var out []map[string]any
	add := func(value int64, source, field string) {
		if value <= 0 || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, map[string]any{
			"value":  value,
			"source": source,
			"field":  field,
		})
	}
	add(anyInt64(metadata["flow_cl"]), "issue_metadata", "flow_cl")
	add(anyInt64(metadata["p4_cl"]), "issue_metadata", "p4_cl")
	add(anyInt64(metadata["shelved_cl"]), "issue_metadata", "shelved_cl")
	for field, value := range fields {
		for _, cl := range extractP4CLs(fmt.Sprint(value)) {
			add(cl, "external_fields", field)
		}
	}
	for _, comment := range comments {
		for _, cl := range extractP4CLs(fmt.Sprint(comment["content"])) {
			add(cl, "multica_comment", fmt.Sprint(comment["id"]))
		}
	}
	for _, comment := range externalComments {
		for _, cl := range extractP4CLs(fmt.Sprint(comment["content"])) {
			add(cl, "feishu_project_comment", fmt.Sprint(comment["id"]))
		}
	}
	for _, review := range reviews {
		add(anyInt64(review["shelved_cl"]), "perforce_review", fmt.Sprint(review["review_id"]))
		add(anyInt64(review["committed_cl"]), "perforce_review", fmt.Sprint(review["review_id"]))
		for _, cl := range anyInt64Slice(review["changes"]) {
			add(cl, "perforce_review_changes", fmt.Sprint(review["review_id"]))
		}
		for _, cl := range anyInt64Slice(review["commits"]) {
			add(cl, "perforce_review_commits", fmt.Sprint(review["review_id"]))
		}
	}
	return out
}

func p4EvidenceReviewCandidates(metadata map[string]any, fields map[string]any, comments []map[string]any, externalComments []map[string]any, reviews []map[string]any) []map[string]any {
	seen := map[int64]bool{}
	var out []map[string]any
	add := func(value int64, source, field string) {
		if value <= 0 || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, map[string]any{
			"value":  value,
			"source": source,
			"field":  field,
		})
	}
	add(anyInt64(metadata["flow_review"]), "issue_metadata", "flow_review")
	add(anyInt64(metadata["swarm_review"]), "issue_metadata", "swarm_review")
	for field, value := range fields {
		for _, review := range extractSwarmReviews(fmt.Sprint(value)) {
			add(review, "external_fields", field)
		}
	}
	for _, comment := range comments {
		for _, review := range extractSwarmReviews(fmt.Sprint(comment["content"])) {
			add(review, "multica_comment", fmt.Sprint(comment["id"]))
		}
	}
	for _, comment := range externalComments {
		for _, review := range extractSwarmReviews(fmt.Sprint(comment["content"])) {
			add(review, "feishu_project_comment", fmt.Sprint(comment["id"]))
		}
	}
	for _, review := range reviews {
		add(anyInt64(review["review_id"]), "perforce_review", fmt.Sprint(review["id"]))
	}
	return out
}

var (
	p4CLCandidateRe        = regexp.MustCompile(`(?i)\b(?:shelved\s+cl|submitted\s+cl|committed\s+cl|final\s+cl|changelist|cl)\s*[:=#-]?\s*(\d{4,})\b`)
	swarmReviewCandidateRe = regexp.MustCompile(`(?i)\b(?:swarm\s+review|review)\s*[:=#/-]?\s*(\d{4,})\b|/reviews?/(\d{4,})\b`)
)

func extractP4CLs(text string) []int64 {
	return extractCandidateInts(p4CLCandidateRe, text)
}

func extractSwarmReviews(text string) []int64 {
	return extractCandidateInts(swarmReviewCandidateRe, text)
}

func extractCandidateInts(re *regexp.Regexp, text string) []int64 {
	matches := re.FindAllStringSubmatch(text, -1)
	out := make([]int64, 0, len(matches))
	for _, match := range matches {
		for _, group := range match[1:] {
			if v := parsePositiveInt64(group); v > 0 {
				out = append(out, v)
				break
			}
		}
	}
	return out
}

func parsePositiveInt64(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	var out int64
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0
		}
		out = out*10 + int64(r-'0')
	}
	return out
}

func anyInt64(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case float64:
		if v == float64(int64(v)) {
			return int64(v)
		}
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		return parsePositiveInt64(v)
	}
	return 0
}

func anyInt64Slice(value any) []int64 {
	switch v := value.(type) {
	case []int32:
		out := make([]int64, 0, len(v))
		for _, item := range v {
			out = append(out, int64(item))
		}
		return out
	case []int64:
		return v
	case []any:
		out := make([]int64, 0, len(v))
		for _, item := range v {
			if n := anyInt64(item); n > 0 {
				out = append(out, n)
			}
		}
		return out
	default:
		return nil
	}
}

func p4EvidenceReviewMaps(reviews []db.PerforceReview) []map[string]any {
	out := make([]map[string]any, 0, len(reviews))
	for _, review := range reviews {
		out = append(out, map[string]any{
			"id":                util.UUIDToString(review.ID),
			"review_id":         review.ReviewID,
			"title":             review.Title,
			"state":             review.State,
			"html_url":          review.HtmlUrl,
			"author":            textString(review.Author),
			"shelved_cl":        int32OrNil(review.ShelvedCl),
			"committed_cl":      int32OrNil(review.CommittedCl),
			"changes":           review.Changes,
			"commits":           review.Commits,
			"swarm_branch":      textString(review.SwarmBranch),
			"event_type":        textString(review.EventType),
			"sent_at":           timeString(review.SentAt),
			"raw_payload":       jsonObjectMap(review.RawPayload),
			"review_created_at": timeString(review.ReviewCreatedAt),
			"review_updated_at": timeString(review.ReviewUpdatedAt),
		})
	}
	return out
}

func (s *P4AssessmentService) CompleteTask(ctx context.Context, task db.AgentTaskQueue, result []byte) error {
	parsed, err := parseP4AssessmentTaskOutput(result)
	if err != nil {
		warnings, _ := json.Marshal([]string{"parser_error: " + err.Error()})
		_, failErr := s.Queries.FailP4AssessmentFromTask(ctx, db.FailP4AssessmentFromTaskParams{
			WorkspaceID:      s.taskWorkspaceID(task),
			AssessmentTaskID: task.ID,
			Warnings:         warnings,
		})
		if failErr != nil {
			return failErr
		}
		return err
	}
	confidence := pgtype.Numeric{}
	if parsed.Confidence != nil {
		if err := confidence.Scan(*parsed.Confidence); err != nil {
			return err
		}
	}
	_, err = s.Queries.CompleteP4AssessmentFromTask(ctx, db.CompleteP4AssessmentFromTaskParams{
		WorkspaceID:                   s.taskWorkspaceID(task),
		AssessmentTaskID:              task.ID,
		DeliveryAttributionPrediction: parsed.DeliveryAttributionPrediction,
		QualityPrediction:             parsed.QualityPrediction,
		PredictionReasons:             parsed.PredictionReasons,
		Confidence:                    confidence,
		Workstream:                    parsed.Workstream,
		SwarmReviews:                  parsed.SwarmReviews,
		AiShelvedCls:                  parsed.AIShelvedCLs,
		SwarmChangeCls:                parsed.SwarmChangeCLs,
		SwarmCommittedCls:             parsed.SwarmCommittedCLs,
		ExternalCommittedCls:          parsed.ExternalCommittedCLs,
		Evidence:                      parsed.Evidence,
		Summary:                       parsed.Summary,
		Warnings:                      parsed.Warnings,
		Model:                         pgtype.Text{String: parsed.Model, Valid: parsed.Model != ""},
	})
	return err
}

func (s *P4AssessmentService) taskWorkspaceID(task db.AgentTaskQueue) pgtype.UUID {
	var taskCtx p4AssessmentContext
	if json.Unmarshal(task.Context, &taskCtx) == nil && taskCtx.WorkspaceID != "" {
		if id, err := util.ParseUUID(taskCtx.WorkspaceID); err == nil {
			return id
		}
	}
	return pgtype.UUID{}
}

type p4AssessmentOutput struct {
	DeliveryAttributionPrediction string          `json:"delivery_attribution_prediction"`
	QualityPrediction             string          `json:"quality_prediction"`
	PredictionReasons             []string        `json:"prediction_reasons"`
	Confidence                    *float64        `json:"confidence"`
	Workstream                    string          `json:"workstream"`
	SwarmReviews                  json.RawMessage `json:"swarm_reviews"`
	AIShelvedCLs                  []int32         `json:"ai_shelved_cls"`
	SwarmChangeCLs                []int32         `json:"swarm_change_cls"`
	SwarmCommittedCLs             []int32         `json:"swarm_committed_cls"`
	ExternalCommittedCLs          []int32         `json:"external_committed_cls"`
	Evidence                      json.RawMessage `json:"evidence"`
	Summary                       string          `json:"summary"`
	Warnings                      json.RawMessage `json:"warnings"`
	Model                         string          `json:"model"`
}

var fencedJSONBlockRE = regexp.MustCompile("(?s)```json\\s*(\\{.*?\\})\\s*```")

func parseP4AssessmentTaskOutput(result []byte) (p4AssessmentOutput, error) {
	var envelope struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(result, &envelope); err != nil {
		return p4AssessmentOutput{}, err
	}
	raw := strings.TrimSpace(envelope.Output)
	if raw == "" {
		return p4AssessmentOutput{}, fmt.Errorf("empty output")
	}
	payload := []byte(raw)
	if !bytes.HasPrefix(payload, []byte("{")) {
		matches := fencedJSONBlockRE.FindAllStringSubmatch(raw, -1)
		if len(matches) != 1 {
			return p4AssessmentOutput{}, fmt.Errorf("expected a JSON object or one fenced json block")
		}
		if raw != matches[0][0] {
			return p4AssessmentOutput{}, fmt.Errorf("fenced json block must be the entire output")
		}
		payload = []byte(matches[0][1])
	}
	var out p4AssessmentOutput
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return p4AssessmentOutput{}, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return p4AssessmentOutput{}, fmt.Errorf("unexpected trailing json")
	}
	if out.DeliveryAttributionPrediction == "" {
		out.DeliveryAttributionPrediction = "unknown"
	}
	if out.QualityPrediction == "" {
		out.QualityPrediction = "unknown"
	}
	if !validP4DeliveryPrediction(out.DeliveryAttributionPrediction) {
		return p4AssessmentOutput{}, fmt.Errorf("invalid delivery_attribution_prediction")
	}
	if !validP4QualityPrediction(out.QualityPrediction) {
		return p4AssessmentOutput{}, fmt.Errorf("invalid quality_prediction")
	}
	if out.Confidence != nil && (*out.Confidence < 0 || *out.Confidence > 1) {
		return p4AssessmentOutput{}, fmt.Errorf("confidence out of range")
	}
	if len(out.SwarmReviews) == 0 {
		out.SwarmReviews = []byte("[]")
	} else if !jsonRawHasShape(out.SwarmReviews, []byte("[")) {
		return p4AssessmentOutput{}, fmt.Errorf("swarm_reviews must be an array")
	}
	if len(out.Evidence) == 0 {
		out.Evidence = []byte("{}")
	} else if !jsonRawHasShape(out.Evidence, []byte("{")) {
		return p4AssessmentOutput{}, fmt.Errorf("evidence must be an object")
	}
	if len(out.Warnings) == 0 {
		out.Warnings = []byte("[]")
	} else if !jsonRawHasShape(out.Warnings, []byte("[")) {
		return p4AssessmentOutput{}, fmt.Errorf("warnings must be an array")
	}
	return out, nil
}

func validP4DeliveryPrediction(v string) bool {
	switch v {
	case "ai_delivered", "ai_assisted", "human_delivered", "conflict", "unattributed", "unknown":
		return true
	default:
		return false
	}
}

func validP4QualityPrediction(v string) bool {
	switch v {
	case "likely_correct", "likely_needs_changes", "likely_wrong", "unknown":
		return true
	default:
		return false
	}
}

func jsonRawHasShape(raw json.RawMessage, prefix []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return bytes.HasPrefix(trimmed, prefix) && json.Valid(trimmed)
}

func P4AssessmentMappedStatus(workItemType, externalStatus string, statusMapping, workItemTypes []byte) string {
	cfg := db.FeishuProjectIntegration{
		StatusMapping: statusMapping,
		WorkItemTypes: workItemTypes,
	}
	mapped, ok := feishuProjectMappedLocalStatus(FeishuProjectStatusMappingFor(cfg, workItemType), externalStatus)
	if !ok {
		return ""
	}
	return mapped
}

func textString(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func uuidStringOrEmpty(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return util.UUIDToString(id)
}

func int32OrNil(v pgtype.Int4) any {
	if !v.Valid {
		return nil
	}
	return v.Int32
}

func timeString(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339Nano)
}

func jsonObjectMap(raw []byte) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}
