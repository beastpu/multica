package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

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
	Queries   *db.Queries
	TxStarter TxStarter
	Task      *TaskService
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
	Binding map[string]any `json:"binding"`
	Issue   map[string]any `json:"issue"`
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
			AgentID:   row.AssigneeID,
			RuntimeID: row.AgentRuntimeID,
			IssueID:   row.IssueID,
			Priority:  int32(1),
			Context:   taskContext,
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
		},
	}, nil
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
		payload = []byte(matches[0][1])
	}
	var out p4AssessmentOutput
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return p4AssessmentOutput{}, err
	}
	if out.DeliveryAttributionPrediction == "" {
		out.DeliveryAttributionPrediction = "unknown"
	}
	if out.QualityPrediction == "" {
		out.QualityPrediction = "unknown"
	}
	if out.Confidence != nil && (*out.Confidence < 0 || *out.Confidence > 1) {
		return p4AssessmentOutput{}, fmt.Errorf("confidence out of range")
	}
	if len(out.SwarmReviews) == 0 {
		out.SwarmReviews = []byte("[]")
	}
	if len(out.Evidence) == 0 {
		out.Evidence = []byte("{}")
	}
	if len(out.Warnings) == 0 {
		out.Warnings = []byte("[]")
	}
	return out, nil
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
