package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) notifyWorkflowNeedsSetup(
	ctx context.Context,
	instance db.WorkflowInstance,
	missingRoles []string,
) {
	if len(missingRoles) == 0 {
		return
	}
	h.notifyWorkflowActionRequired(
		ctx,
		instance,
		nil,
		"needs_setup",
		"Workflow setup required",
		"Assign the missing workflow roles: "+strings.Join(missingRoles, ", "),
		map[string]any{"missing_roles": missingRoles},
	)
}

func (h *Handler) notifyWorkflowIntervention(
	ctx context.Context,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	reasons []workflowWaitingReasonJSON,
) {
	if len(reasons) == 0 {
		return
	}
	codes := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		codes = append(codes, reason.Code)
	}
	h.notifyWorkflowActionRequired(
		ctx,
		instance,
		&node,
		"intervention",
		"Workflow activity needs attention",
		"Review activity "+node.NameSnapshot+": "+strings.Join(codes, ", "),
		map[string]any{"waiting_reasons": reasons},
	)
}

type workflowWaitingReasonJSON struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

func workflowWaitingReasonsForNotification(
	reasons []workflowdomain.WaitingReason,
) []workflowWaitingReasonJSON {
	items := make([]workflowWaitingReasonJSON, len(reasons))
	for i, reason := range reasons {
		items[i] = workflowWaitingReasonJSON{
			Code: reason.Code, Field: reason.Field, Message: reason.Message,
		}
	}
	return items
}

func (h *Handler) notifyWorkflowActionRequired(
	ctx context.Context,
	instance db.WorkflowInstance,
	node *db.WorkflowNodeInstance,
	reasonCode string,
	title string,
	body string,
	extraDetails map[string]any,
) {
	host, err := h.Queries.GetIssueInWorkspace(
		ctx,
		db.GetIssueInWorkspaceParams{
			ID: instance.HostIssueID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		slog.Warn(
			"workflow inbox notification host lookup failed",
			"workspace_id", uuidToString(instance.WorkspaceID),
			"workflow_template_id", uuidToString(instance.TemplateID),
			"workflow_version_id", uuidToString(instance.TemplateVersionID),
			"workflow_instance_id", uuidToString(instance.ID),
			"host_issue_id", uuidToString(instance.HostIssueID),
			"failure_type", "host_lookup_failed",
			"error", err,
		)
		return
	}

	recipients := make(map[pgtype.UUID]struct{})
	if instance.StartedByType == "member" && instance.StartedByID.Valid {
		recipients[instance.StartedByID] = struct{}{}
	}
	if host.AssigneeType.Valid && host.AssigneeType.String == "member" &&
		host.AssigneeID.Valid {
		recipients[host.AssigneeID] = struct{}{}
	}
	members, membersErr := h.Queries.ListMembers(ctx, instance.WorkspaceID)
	if membersErr != nil {
		slog.Warn(
			"workflow inbox notification member lookup failed",
			"workspace_id", uuidToString(instance.WorkspaceID),
			"workflow_template_id", uuidToString(instance.TemplateID),
			"workflow_version_id", uuidToString(instance.TemplateVersionID),
			"workflow_instance_id", uuidToString(instance.ID),
			"host_issue_id", uuidToString(instance.HostIssueID),
			"failure_type", "member_lookup_failed",
			"error", membersErr,
		)
	} else {
		for _, member := range members {
			if roleAllowed(member.Role, "owner", "admin") {
				recipients[member.UserID] = struct{}{}
			}
		}
	}
	if node != nil {
		participants, participantErr := h.Queries.ListWorkflowNodeParticipants(
			ctx,
			db.ListWorkflowNodeParticipantsParams{
				WorkflowNodeInstanceID: node.ID,
				WorkspaceID:            node.WorkspaceID,
			},
		)
		if participantErr != nil {
			slog.Warn(
				"workflow inbox notification participant lookup failed",
				"workspace_id", uuidToString(instance.WorkspaceID),
				"workflow_template_id", uuidToString(instance.TemplateID),
				"workflow_version_id", uuidToString(instance.TemplateVersionID),
				"workflow_instance_id", uuidToString(instance.ID),
				"host_issue_id", uuidToString(instance.HostIssueID),
				"node_key", node.NodeKey,
				"workflow_node_instance_id", uuidToString(node.ID),
				"node_attempt", node.Attempt,
				"failure_type", "participant_lookup_failed",
				"error", participantErr,
			)
		} else {
			for _, participant := range participants {
				if participant.Role == "owner" &&
					participant.ActorType == "member" {
					recipients[participant.ActorID] = struct{}{}
				}
			}
		}
	}

	details := map[string]any{
		"reason":               reasonCode,
		"workflow_instance_id": uuidToString(instance.ID),
	}
	if node != nil {
		details["workflow_node_instance_id"] = uuidToString(node.ID)
		details["activity_key"] = node.NodeKey
	}
	for key, value := range extraDetails {
		details[key] = value
	}
	encodedDetails, _ := json.Marshal(details)

	for recipientID := range recipients {
		item, createErr := h.Queries.CreateInboxItem(
			ctx,
			db.CreateInboxItemParams{
				WorkspaceID:   instance.WorkspaceID,
				RecipientType: "member",
				RecipientID:   recipientID,
				Type:          "workflow_action_required",
				Severity:      "action_required",
				IssueID:       host.ID,
				Title:         title,
				Body: pgtype.Text{
					String: body,
					Valid:  strings.TrimSpace(body) != "",
				},
				ActorType: pgtype.Text{String: "system", Valid: true},
				Details:   encodedDetails,
			},
		)
		if createErr != nil {
			slog.Warn(
				"workflow inbox notification write failed",
				"workspace_id", uuidToString(instance.WorkspaceID),
				"workflow_template_id", uuidToString(instance.TemplateID),
				"workflow_version_id", uuidToString(instance.TemplateVersionID),
				"workflow_instance_id", uuidToString(instance.ID),
				"host_issue_id", uuidToString(instance.HostIssueID),
				"recipient_id", uuidToString(recipientID),
				"failure_type", "inbox_write_failed",
				"error", createErr,
			)
			continue
		}
		response := inboxToResponse(item)
		issueStatus := host.Status
		response.IssueStatus = &issueStatus
		h.publish(
			protocol.EventInboxNew,
			uuidToString(instance.WorkspaceID),
			"system",
			"",
			map[string]any{"item": response},
		)
	}
}
