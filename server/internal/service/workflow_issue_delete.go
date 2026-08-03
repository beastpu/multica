package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type WorkflowIssueDeleteResult struct {
	CancelledTasks []db.AgentTaskQueue
	AffectedRuns   []db.WorkflowInstance
}

type workflowCancelledTaskBroadcaster interface {
	BroadcastCancelledTasks(ctx context.Context, tasks []db.AgentTaskQueue)
}

func publishWorkflowIssueDeleteResult(
	ctx context.Context,
	tasks IssueTaskCanceller,
	pub FeishuProjectEventPublisher,
	actorType string,
	actorID string,
	result WorkflowIssueDeleteResult,
) {
	if broadcaster, ok := tasks.(workflowCancelledTaskBroadcaster); ok {
		broadcaster.BroadcastCancelledTasks(ctx, result.CancelledTasks)
	}
	if pub == nil {
		return
	}
	for _, instance := range result.AffectedRuns {
		pub.Publish(events.Event{
			Type: protocol.EventWorkflowInstanceUpdated, WorkspaceID: UUIDString(instance.WorkspaceID),
			ActorType: actorType, ActorID: actorID,
			Payload: map[string]any{"workflow_instance_id": UUIDString(instance.ID)},
		})
	}
}

// PreserveWorkflowHistoryForIssue runs inside the issue deletion transaction.
// It detaches issue-facing references while retaining the workflow runtime as
// audit history. Active runs are cancelled before their host is detached.
func PreserveWorkflowHistoryForIssue(
	ctx context.Context,
	q *db.Queries,
	issue db.Issue,
	actorType string,
	actorID pgtype.UUID,
) (WorkflowIssueDeleteResult, error) {
	var result WorkflowIssueDeleteResult
	cancelledRuns, err := q.CancelWorkflowInstancesByHost(ctx, db.CancelWorkflowInstancesByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	})
	if err != nil {
		return result, fmt.Errorf("cancel workflow instances: %w", err)
	}
	cancelled, err := q.CancelAgentTasksByWorkflowHost(
		ctx,
		db.CancelAgentTasksByWorkflowHostParams{
			HostIssueID: issue.ID,
			WorkspaceID: issue.WorkspaceID,
		},
	)
	if err != nil {
		return result, fmt.Errorf("cancel workflow agent tasks: %w", err)
	}
	result.CancelledTasks = cancelled
	if err := q.DetachWorkflowNodeTasksByIssue(ctx, db.DetachWorkflowNodeTasksByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		return result, fmt.Errorf("detach workflow task from issue: %w", err)
	}

	hostParams := db.DetachWorkflowIssuesByHostParams{
		HostIssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}
	if err := q.DetachWorkflowIssuesByHost(ctx, hostParams); err != nil {
		return result, fmt.Errorf("detach workflow issues from host: %w", err)
	}
	if err := q.CancelOpenWorkflowNodesByHost(ctx, db.CancelOpenWorkflowNodesByHostParams{
		WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
	}); err != nil {
		return result, fmt.Errorf("cancel workflow nodes: %w", err)
	}
	for _, instance := range cancelledRuns {
		payload, _ := json.Marshal(map[string]any{
			"reason":        "host_issue_deleted",
			"host_issue_id": UUIDString(issue.ID),
		})
		if _, err := q.CreateWorkflowEvent(ctx, db.CreateWorkflowEventParams{
			WorkspaceID: instance.WorkspaceID, WorkflowInstanceID: instance.ID,
			EventType: "workflow.cancelled", ActorType: actorType, ActorID: actorID,
			IdempotencyKey: "host-issue-deleted:" + UUIDString(issue.ID), Payload: payload,
		}); err != nil {
			return result, fmt.Errorf("record workflow cancellation: %w", err)
		}
	}
	result.AffectedRuns, err = q.DetachWorkflowInstancesByHost(
		ctx,
		db.DetachWorkflowInstancesByHostParams{
			WorkspaceID: issue.WorkspaceID, HostIssueID: issue.ID,
		},
	)
	if err != nil {
		return result, fmt.Errorf("detach workflow instances from host: %w", err)
	}
	return result, nil
}
