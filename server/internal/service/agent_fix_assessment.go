package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	P4AssessmentTaskType      = "agent_fix_p4_assessment"
	P4AssessmentPromptVersion = "p4-assessment-v1"
	P4AssessmentBackfillLimit = 50

	// CapabilityP4Assessment is the workspace_agent_capability key naming the
	// agent that executes P4 assessment runs (plan C-1). Trigger is fail-closed
	// on this configuration.
	CapabilityP4Assessment = "p4_assessment"

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
}

// P4AssessmentCapabilityConfigured reports whether the workspace has a
// p4_assessment capability agent configured. Nil-safe (nil queries ⇒ false)
// and fail-closed on lookup errors so callers can use it as a gate.
func P4AssessmentCapabilityConfigured(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID) bool {
	if q == nil {
		return false
	}
	_, err := q.GetWorkspaceAgentCapability(ctx, db.GetWorkspaceAgentCapabilityParams{
		WorkspaceID: workspaceID,
		Capability:  CapabilityP4Assessment,
	})
	return err == nil
}

// p4CapabilityAgentUnavailable returns a trigger-rejection reason when the
// configured capability agent cannot execute a native assessment task, or ""
// when it is usable. Only daemon-served local runtimes run assessment (the
// skill needs inner-network P4/Swarm access), so a cloud runtime is rejected.
func p4CapabilityAgentUnavailable(c db.GetWorkspaceAgentCapabilityRow, workspaceID pgtype.UUID) string {
	if util.UUIDToString(c.AgentWorkspaceID) != util.UUIDToString(workspaceID) {
		return "capability_agent_not_in_workspace"
	}
	if c.AgentArchivedAt.Valid {
		return "capability_agent_archived"
	}
	if !c.AgentRuntimeID.Valid {
		return "capability_agent_has_no_runtime"
	}
	if c.AgentRuntimeMode != "local" {
		return "capability_agent_runtime_not_local"
	}
	return ""
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

// Trigger runs one assessment through the native task flow (plan C-1):
// upsert the queue row to 'pending', create the run's projection issue
// ASSIGNED to the workspace's p4_assessment capability agent, and create the
// native agent task hanging on that projection issue — all in one
// transaction. "指派即派发": the task is queued in agent_task_queue and the
// daemon claims it through the ordinary prepare-lease channel; there is no
// dispatcher and no batch pending pool on this path.
//
// Fail-closed gate: no workspace_agent_capability row for p4_assessment ⇒ the
// trigger is rejected with reason "p4_assessment_capability_not_configured".
//
// A force re-run creates a NEW projection issue (and task) and repoints,
// leaving the previous run's issue untouched. See
// agent_fix_assessment_projection.go.
func (s *P4AssessmentService) Trigger(ctx context.Context, workspaceID, bindingID pgtype.UUID, force bool, actor P4AssessmentActor) (P4AssessmentTriggerResult, error) {
	if actor.Trigger == "" {
		if force {
			actor.Trigger = P4AssessmentTriggerForceRerun
		} else {
			actor.Trigger = P4AssessmentTriggerManual
		}
	}
	var result P4AssessmentTriggerResult
	err := s.runInTx(ctx, func(q *db.Queries) error {
		capability, capErr := q.GetWorkspaceAgentCapability(ctx, db.GetWorkspaceAgentCapabilityParams{
			WorkspaceID: workspaceID,
			Capability:  CapabilityP4Assessment,
		})
		if capErr != nil && !errors.Is(capErr, pgx.ErrNoRows) {
			return capErr
		}
		if errors.Is(capErr, pgx.ErrNoRows) {
			result = P4AssessmentTriggerResult{Reason: "p4_assessment_capability_not_configured"}
			return nil
		}
		if reason := p4CapabilityAgentUnavailable(capability, workspaceID); reason != "" {
			result = P4AssessmentTriggerResult{Reason: reason}
			return nil
		}
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
		projIssueID, err := s.createAssessmentProjectionIssue(ctx, q, workspaceID, bindingID, row, actor, capability.AgentID)
		if err != nil {
			return err
		}
		if projIssueID.Valid {
			assessment.AssessmentIssueID = projIssueID
		}
		result = P4AssessmentTriggerResult{Assessment: assessment, Created: true}
		// Native task: only when the run has a projection issue to hang the
		// task on. A scan-path run without a resolvable creator gets no
		// projection issue (see createAssessmentProjectionIssue) and therefore
		// no native task — the row stays pending until a manual re-trigger.
		if !projIssueID.Valid {
			return nil
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
			AgentID:     capability.AgentID,
			RuntimeID:   capability.AgentRuntimeID,
			IssueID:     projIssueID,
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
		if projIssueID.Valid {
			assessment.AssessmentIssueID = projIssueID
		}
		result = P4AssessmentTriggerResult{Assessment: assessment, Task: &task, Created: true}
		return nil
	})
	if err != nil {
		return P4AssessmentTriggerResult{}, err
	}
	if result.Task != nil && s.Task != nil {
		// Same observe-order as enqueueIssueTask: publish the queued event
		// before poking the daemon so clients can never see task:dispatch
		// before task:queued.
		s.Task.broadcastTaskEvent(ctx, protocol.EventTaskQueued, *result.Task)
		s.Task.NotifyTaskEnqueued(ctx, *result.Task)
	}
	return result, nil
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
// (analysis tasks can only comment on their own assessment issue), so a stale
// daemon that ignores these instructions still cannot damage real issues.
func p4AssessmentHandoffNote(bindingID pgtype.UUID) string {
	binding := util.UUIDToString(bindingID)
	var b strings.Builder
	b.WriteString("THIS RUN IS A READ-ONLY P4/SWARM ASSESSMENT, NOT A FIX. ")
	b.WriteString("Do NOT modify code, and do NOT change any issue's status, fields, or assignee, and do NOT touch Feishu/Meego, P4, or Swarm. ")
	b.WriteString("You may post plain progress comments ONLY on the assessment issue this task is assigned to; the server rejects every other write.\n\n")
	b.WriteString("Goal: judge the delivery attribution and quality of the completed external work item, then submit a single assessment JSON.\n\n")
	b.WriteString("Step 1 — read the task-scoped evidence (this is the only required Multica call):\n")
	fmt.Fprintf(&b, "  multica api get /api/operations/agent-fixes/%s/p4-evidence\n\n", binding)
	b.WriteString("Step 2 — you MAY inspect inner-network Swarm/P4 with read-only commands (e.g. `p4 describe -s`) when it helps classify CLs. Never mutate anything.\n")
	b.WriteString("If the `multica-agent-fix-p4-assessment` skill is available, follow it for CL role classification and the full schema.\n\n")
	b.WriteString("Attribution rule for a human-submitted final CL:\n")
	b.WriteString("  If it used or materially followed a verified, method-equivalent AI implementation, use \"ai_assisted\", regardless of whether the AI output was produced before or after the human submission.\n")
	b.WriteString("  If it used a materially different implementation, use \"human_delivered\" when human ownership is proven.\n")
	b.WriteString("  If the evidence cannot compare the implementations or establish delivery ownership, use \"unknown\". Base equivalence on implementation diffs, not matching titles or root-cause prose alone.\n\n")
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

	// Backfill is a scan path with no human in the loop: projection issues
	// are created by the integration's creator, mirroring what Feishu sync
	// stamps on synced issues. A legacy integration without created_by_id
	// yields an invalid CreatorID and the projection issue is skipped (the
	// assessment itself still enqueues).
	actor := P4AssessmentActor{Trigger: P4AssessmentTriggerScan, CreatorType: "member"}
	if cfg, cfgErr := s.Queries.GetFeishuProjectIntegrationByID(ctx, integrationID); cfgErr == nil {
		actor.CreatorID = cfg.CreatedByID
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
		result, err := s.Trigger(ctx, row.WorkspaceID, row.BindingID, false, actor)
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

// StartFromTask is the single-direction status projection for the native
// task flow (plan C-1): the daemon flipping the task to running projects the
// queue row to running and the projection issue to in_progress. Best-effort
// by contract — the task start itself is authoritative and must not fail
// because a projection write did; pgx.ErrNoRows means the task has no queue
// row keyed on it (a legacy batch worker task) and is a silent no-op.
func (s *P4AssessmentService) StartFromTask(ctx context.Context, task db.AgentTaskQueue) {
	workspaceID := s.taskWorkspaceID(task)
	row, err := s.Queries.StartP4AssessmentFromTask(ctx, db.StartP4AssessmentFromTaskParams{
		WorkspaceID:      workspaceID,
		AssessmentTaskID: task.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		slog.Warn("p4 assessment projection: start from task failed",
			"task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	if err := projectAgentWorkIssueStatus(ctx, s.Queries, workspaceID, row.AssessmentIssueID, "in_progress"); err != nil {
		slog.Warn("p4 assessment projection: start status failed",
			"task_id", util.UUIDToString(task.ID), "error", err)
	}
}

// FailFromTask projects a terminal task failure onto the queue row (failed +
// last_error) and the projection issue (cancelled + failure comment). Same
// single-direction contract as StartFromTask: pgx.ErrNoRows means either no
// row is keyed on this task or the agent already submitted the result through
// the endpoint (the guarded FailP4AssessmentFromTask never clobbers
// 'completed') — both are silent no-ops.
func (s *P4AssessmentService) FailFromTask(ctx context.Context, task db.AgentTaskQueue, failureReason string) {
	workspaceID := s.taskWorkspaceID(task)
	warnings, _ := json.Marshal([]string{"task_failed: " + failureReason})
	lastError := p4AssessmentLastError("task failed: " + failureReason)
	var failed db.AgentFixP4Assessment
	err := s.runInTx(ctx, func(q *db.Queries) error {
		var innerErr error
		failed, innerErr = q.FailP4AssessmentFromTask(ctx, db.FailP4AssessmentFromTaskParams{
			WorkspaceID:      workspaceID,
			AssessmentTaskID: task.ID,
			Warnings:         warnings,
			LastError:        lastError,
		})
		if innerErr != nil {
			return innerErr
		}
		// issue.status has no 'failed'; 'cancelled' is the closest terminal
		// non-success state (see agent_fix_assessment_projection.go).
		return projectAgentWorkIssueStatus(ctx, q, workspaceID, failed.AssessmentIssueID, "cancelled")
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		slog.Warn("p4 assessment projection: fail from task failed",
			"task_id", util.UUIDToString(task.ID), "error", err)
		return
	}
	s.postAgentWorkComment(ctx, workspaceID, task.ID, failed.AssessmentIssueID,
		p4AssessmentFailureComment("任务失败", lastError))
}

func (s *P4AssessmentService) CompleteTask(ctx context.Context, task db.AgentTaskQueue, result []byte) error {
	parsed, err := parseP4AssessmentTaskOutput(result)
	if err != nil {
		warnings, _ := json.Marshal([]string{"parser_error: " + err.Error()})
		workspaceID := s.taskWorkspaceID(task)
		lastError := p4AssessmentLastError("task output parse failed: " + err.Error())
		var failed db.AgentFixP4Assessment
		failErr := s.runInTx(ctx, func(q *db.Queries) error {
			var innerErr error
			failed, innerErr = q.FailP4AssessmentFromTask(ctx, db.FailP4AssessmentFromTaskParams{
				WorkspaceID:      workspaceID,
				AssessmentTaskID: task.ID,
				Warnings:         warnings,
				LastError:        lastError,
			})
			if innerErr != nil {
				return innerErr
			}
			// issue.status has no 'failed'; 'cancelled' is the closest
			// terminal non-success state (see the mapping table in
			// agent_fix_assessment_projection.go).
			return projectAgentWorkIssueStatus(ctx, q, workspaceID, failed.AssessmentIssueID, "cancelled")
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
			s.postAgentWorkComment(ctx, workspaceID, task.ID, failed.AssessmentIssueID,
				p4AssessmentFailureComment("任务失败（结果解析失败）", lastError))
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
	// Complete + issue projection commit together; the result-summary comment
	// is best-effort after the commit (narration must not roll back a result).
	var completed db.AgentFixP4Assessment
	err = s.runInTx(ctx, func(q *db.Queries) error {
		var completeErr error
		completed, completeErr = q.CompleteP4AssessmentFromTask(ctx, db.CompleteP4AssessmentFromTaskParams{
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
		if completeErr != nil {
			return completeErr
		}
		return projectAgentWorkIssueStatus(ctx, q, workspaceID, completed.AssessmentIssueID, "done")
	})
	if err != nil {
		return err
	}
	s.postAgentWorkComment(ctx, workspaceID, assessmentTaskID, completed.AssessmentIssueID, p4AssessmentResultComment(parsed))
	return nil
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
