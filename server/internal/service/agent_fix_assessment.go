package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
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

	// Batch-worker lease: a pulled row must be submitted within this window or
	// it returns to the pending pool for the next pull.
	P4AssessmentLeaseDuration = 30 * time.Minute
	// Evidence is inlined per item, so batches stay small.
	P4AssessmentBatchDefaultLimit = 5
	P4AssessmentBatchMaxLimit     = 10

	// last_error is an operator-facing one-liner, not a log sink — error
	// chains from evidence building can drag whole HTTP bodies along.
	p4AssessmentLastErrorMaxLen = 500
)

// p4AssessmentLastError shapes an error message for the last_error column:
// single line, hard length cap.
func p4AssessmentLastError(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > p4AssessmentLastErrorMaxLen {
		msg = msg[:p4AssessmentLastErrorMaxLen]
	}
	return msg
}

type P4AssessmentService struct {
	Queries       *db.Queries
	TxStarter     TxStarter
	Task          *TaskService
	FeishuProject *FeishuProjectClient
	// Allowlist gates the batch assessment endpoints the same way it gates the
	// sync auto-trigger: fail-closed, only explicitly opted-in workspaces.
	Allowlist P4AssessmentAllowlist
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

// Trigger enqueues a binding for assessment by upserting its row to 'pending'.
// It does NOT spawn an agent task: the pending pool is consumed by a batch
// assessment worker through LeasePending / SubmitBatchResult, so per-binding
// task fan-out (which used to pile hundreds of queued tasks onto one runtime)
// no longer exists. Legacy per-binding tasks already in flight still complete
// through CompleteTask / SubmitResult.
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
		result = P4AssessmentTriggerResult{Assessment: assessment, Created: true}
		return nil
	})
	if err != nil {
		return P4AssessmentTriggerResult{}, err
	}
	return result, nil
}

// P4AssessmentPendingItem is one entry of the batch-worker pull. `Ref` is an
// opaque handle the agent must echo back on submit — internally the binding
// UUID, but the agent never needs to know or construct that. Evidence is
// inlined so the worker needs no follow-up call per item.
type P4AssessmentPendingItem struct {
	Ref            string               `json:"ref"`
	Issue          map[string]any       `json:"issue"`
	LeaseExpiresAt string               `json:"lease_expires_at"`
	Evidence       P4AssessmentEvidence `json:"evidence"`
}

// LeasePending claims up to `limit` assessable rows for the worker task and
// returns them with inlined evidence. Rows whose evidence fails to build are
// released back to the pending pool instead of being returned half-empty.
func (s *P4AssessmentService) LeasePending(ctx context.Context, workspaceID, taskID pgtype.UUID, limit int32) ([]P4AssessmentPendingItem, error) {
	if limit <= 0 {
		limit = P4AssessmentBatchDefaultLimit
	}
	if limit > P4AssessmentBatchMaxLimit {
		limit = P4AssessmentBatchMaxLimit
	}
	leaseUntil := time.Now().Add(P4AssessmentLeaseDuration)
	rows, err := s.Queries.LeaseP4AssessmentsPending(ctx, db.LeaseP4AssessmentsPendingParams{
		WorkspaceID:      workspaceID,
		AssessmentTaskID: taskID,
		LeasedUntil:      pgtype.Timestamptz{Time: leaseUntil, Valid: true},
		Limit:            limit,
	})
	if err != nil {
		return nil, err
	}
	items := make([]P4AssessmentPendingItem, 0, len(rows))
	for _, row := range rows {
		evidence, evidenceErr := s.Evidence(ctx, workspaceID, row.FeishuBindingID)
		if evidenceErr != nil {
			_ = s.Queries.ReleaseP4AssessmentLease(ctx, db.ReleaseP4AssessmentLeaseParams{
				WorkspaceID:      workspaceID,
				FeishuBindingID:  row.FeishuBindingID,
				AssessmentTaskID: taskID,
				LastError:        p4AssessmentLastError("evidence build failed: " + evidenceErr.Error()),
			})
			continue
		}
		items = append(items, P4AssessmentPendingItem{
			Ref:            util.UUIDToString(row.FeishuBindingID),
			Issue:          evidence.Issue,
			LeaseExpiresAt: leaseUntil.UTC().Format(time.RFC3339),
			Evidence:       evidence,
		})
	}
	return items, nil
}

// ErrP4AssessmentRefNotLeased distinguishes "this ref is not leased by your
// task" (lease expired and reclaimed, or a made-up ref) from validation
// failures — the handler maps it to a 409 the worker can react to by
// re-pulling.
var ErrP4AssessmentRefNotLeased = errors.New("ref not leased by this task")

// SubmitBatchResult stores an assessment result the batch worker POSTed with
// an opaque `ref` (as returned by LeasePending). Everything except `ref` is
// exactly the single-result schema, validated by the same code path.
func (s *P4AssessmentService) SubmitBatchResult(ctx context.Context, workspaceID, taskID pgtype.UUID, payload []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return err
	}
	refRaw, ok := fields["ref"]
	if !ok {
		return fmt.Errorf("missing ref")
	}
	var ref string
	if err := json.Unmarshal(refRaw, &ref); err != nil {
		return fmt.Errorf("ref must be a string")
	}
	bindingID, err := util.ParseUUID(strings.TrimSpace(ref))
	if err != nil {
		return fmt.Errorf("invalid ref")
	}
	delete(fields, "ref")
	rest, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	parsed, err := validateP4AssessmentPayload(rest)
	if err != nil {
		return err
	}
	confidence, err := p4AssessmentConfidenceNumeric(parsed.Confidence)
	if err != nil {
		return err
	}
	_, err = s.Queries.CompleteP4AssessmentFromBinding(ctx, db.CompleteP4AssessmentFromBindingParams{
		WorkspaceID:                   workspaceID,
		FeishuBindingID:               bindingID,
		AssessmentTaskID:              taskID,
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
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrP4AssessmentRefNotLeased
	}
	return err
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
			LastError:        p4AssessmentLastError("task output parse failed: " + err.Error()),
		})
		// pgx.ErrNoRows means the row was already 'completed' — the agent
		// submitted the result through the /p4-assessment/result endpoint, so
		// the guarded FailP4AssessmentFromTask matched nothing. That is the
		// happy path now, not a failure: the parse fallback simply had nothing
		// to do.
		if failErr != nil && !errors.Is(failErr, pgx.ErrNoRows) {
			return failErr
		}
		if failErr == nil {
			return err
		}
		return nil
	}
	return s.writeCompletedAssessment(ctx, s.taskWorkspaceID(task), task.ID, parsed)
}

// SubmitResult stores an assessment result the agent POSTed to the
// /p4-assessment/result endpoint. The payload is the bare result JSON (no
// {"output": ...} envelope). Validation errors are returned verbatim so the
// handler can surface them as a 400 the agent can self-correct against;
// assessmentTaskID is the agent's own task (already authorized by the handler),
// which is the row's assessment_task_id.
func (s *P4AssessmentService) SubmitResult(ctx context.Context, workspaceID, assessmentTaskID pgtype.UUID, payload []byte) error {
	parsed, err := validateP4AssessmentPayload(payload)
	if err != nil {
		return err
	}
	return s.writeCompletedAssessment(ctx, workspaceID, assessmentTaskID, parsed)
}

// writeCompletedAssessment maps a validated result onto the completed row,
// keyed on (workspace, assessment_task_id). Shared by the task-output fallback
// and the submit endpoint so both write identical columns.
// p4AssessmentConfidenceNumeric converts the optional confidence into a
// pgtype.Numeric. pgtype.Numeric.Scan does not accept a float64 (it
// panics/errors with "cannot scan float64"); feed it the decimal string form
// instead.
func p4AssessmentConfidenceNumeric(confidence *float64) (pgtype.Numeric, error) {
	out := pgtype.Numeric{}
	if confidence == nil {
		return out, nil
	}
	if err := out.Scan(strconv.FormatFloat(*confidence, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}, err
	}
	return out, nil
}

func (s *P4AssessmentService) writeCompletedAssessment(ctx context.Context, workspaceID, assessmentTaskID pgtype.UUID, parsed p4AssessmentOutput) error {
	confidence, err := p4AssessmentConfidenceNumeric(parsed.Confidence)
	if err != nil {
		return err
	}
	_, err = s.Queries.CompleteP4AssessmentFromTask(ctx, db.CompleteP4AssessmentFromTaskParams{
		WorkspaceID:                   workspaceID,
		AssessmentTaskID:              assessmentTaskID,
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
	return validateP4AssessmentPayload(payload)
}

// validateP4AssessmentPayload validates a bare assessment-result JSON object
// (exactly the schema the skill documents) and fills the same defaults the
// task-output parser applies. It is shared by the task-completion fallback
// path and the /p4-assessment/result submit endpoint, so the agent gets the
// identical contract whether it returns the JSON or POSTs it. Errors are
// phrased for the agent to self-correct on a 400.
func validateP4AssessmentPayload(payload []byte) (p4AssessmentOutput, error) {
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
	// The array columns are NOT NULL DEFAULT '{}'; an omitted field decodes to a
	// nil slice, which would write SQL NULL and violate the constraint. Normalize
	// to empty slices so a sparse (evidence-poor) result still stores cleanly.
	if out.PredictionReasons == nil {
		out.PredictionReasons = []string{}
	}
	if out.AIShelvedCLs == nil {
		out.AIShelvedCLs = []int32{}
	}
	if out.SwarmChangeCLs == nil {
		out.SwarmChangeCLs = []int32{}
	}
	if out.SwarmCommittedCLs == nil {
		out.SwarmCommittedCLs = []int32{}
	}
	if out.ExternalCommittedCLs == nil {
		out.ExternalCommittedCLs = []int32{}
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
