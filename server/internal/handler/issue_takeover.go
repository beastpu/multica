package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Takeover is an assignment fact, not a workflow state: stopping the agent's
// in-flight tasks and reassigning the issue to the caller is the whole state
// change. Nothing here touches workflow node status — the node keeps waiting on
// its issue outcome, and the person finishing the issue is what advances it.
//
// The endpoint exists to make those two steps atomic. Reassigning alone leaves
// the agent's task running (a status change deliberately does not cancel tasks,
// MUL-4465), so the agent would keep writing to an issue a person now owns.

// IssueTakeoverCard is the work-scene handoff: where the agent was working,
// so the person can pick the work up instead of starting over. Every field is
// best-effort — a task that died before pinning its workdir simply hands over
// less.
type IssueTakeoverCard struct {
	FromAgent *TakeoverAgentRef   `json:"from_agent,omitempty"`
	TaskID    string              `json:"task_id,omitempty"`
	Runtime   *TakeoverRuntimeRef `json:"runtime,omitempty"`
	// WorkDir follows the RelativeWorkDir privacy rules: the path is stripped
	// to a display-safe suffix, matching what the execution log already shows.
	WorkDir string `json:"work_dir,omitempty"`
	// SessionID names the provider session on the runtime machine. Handing it
	// over is what lets a person on that machine resume the agent's own
	// conversation (e.g. `claude --resume <id>`).
	SessionID string `json:"session_id,omitempty"`
}

type TakeoverAgentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TakeoverRuntimeRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TakeoverIssue stops the agent and hands the issue to the caller.
//
// POST /api/issues/{id}/takeover
//
// Idempotent by construction rather than by key: cancelling is a no-op when
// nothing is in flight, reassigning is a no-op when the issue is already the
// caller's, and the note is written only when the assignee actually changed —
// so a double-click produces one note and two identical responses.
func (h *Handler) TakeoverIssue(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user_id")
	if !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	var fromAgent db.Agent
	takingOverFromAgent := issue.AssigneeType.Valid && issue.AssigneeType.String == "agent"
	if takingOverFromAgent {
		agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID: issue.AssigneeID, WorkspaceID: wsUUID,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "assignee agent not found")
			return
		}
		// Same visibility gate as cancelling the agent's task directly: a
		// private agent's work is not something every member may seize.
		actorType, actorID := h.resolveActor(r, userID, workspaceID)
		if !h.canAccessPrivateAgent(r.Context(), agent, actorType, actorID, workspaceID) {
			writeError(w, http.StatusForbidden, "you do not have access to this agent")
			return
		}
		fromAgent = agent
	}

	alreadyMine := issue.AssigneeType.Valid && issue.AssigneeType.String == "member" &&
		issue.AssigneeID == userUUID

	// Cancel before reassigning: the daemon interrupts on cancel, and doing it
	// first means the agent can never observe itself unassigned mid-write.
	if err := h.TaskService.CancelTasksForIssue(r.Context(), issue.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel in-flight tasks")
		return
	}

	if !alreadyMine {
		updated, err := h.Queries.UpdateIssueAssignee(r.Context(), db.UpdateIssueAssigneeParams{
			ID:           issue.ID,
			AssigneeType: pgtype.Text{String: "member", Valid: true},
			AssigneeID:   userUUID,
			WorkspaceID:  wsUUID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to reassign issue")
			return
		}
		h.writeTakeoverNote(r.Context(), updated, userUUID, fromAgent)
		actorType, actorID := h.resolveActor(r, userID, workspaceID)
		h.publish(protocol.EventIssueUpdated, workspaceID, actorType, actorID, map[string]any{
			"issue":            issueToResponse(updated, h.getIssuePrefix(r.Context(), wsUUID)),
			"assignee_changed": true,
		})
		issue = updated
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"issue":    issueToResponse(issue, h.getIssuePrefix(r.Context(), wsUUID)),
		"takeover": h.issueTakeoverCard(r.Context(), wsUUID, issue),
	})
}

// GetIssueTakeover serves the same card after the fact, so a person who took
// over from their phone can read the workdir once they are back at the machine.
//
// GET /api/issues/{id}/takeover
func (h *Handler) GetIssueTakeover(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	card := h.issueTakeoverCard(r.Context(), wsUUID, issue)
	if card == nil {
		writeError(w, http.StatusNotFound, "issue has no agent work to hand over")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"takeover": card})
}

// issueTakeoverCard derives the handoff from the most recent agent task on the
// issue. Derived, never stored: the card is a view over the task row, and a
// stored copy would go stale the moment the agent runs again.
func (h *Handler) issueTakeoverCard(
	ctx context.Context,
	workspaceID pgtype.UUID,
	issue db.Issue,
) *IssueTakeoverCard {
	tasks, err := h.Queries.ListTasksByIssue(ctx, issue.ID)
	if err != nil {
		return nil
	}
	for _, task := range tasks {
		if !task.AgentID.Valid {
			continue
		}
		card := &IssueTakeoverCard{TaskID: uuidToString(task.ID)}
		if agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID: task.AgentID, WorkspaceID: workspaceID,
		}); err == nil {
			card.FromAgent = &TakeoverAgentRef{ID: uuidToString(agent.ID), Name: agent.Name}
		}
		if task.RuntimeID.Valid {
			if runtime, err := h.Queries.GetAgentRuntime(ctx, task.RuntimeID); err == nil &&
				runtime.WorkspaceID == workspaceID {
				card.Runtime = &TakeoverRuntimeRef{
					ID: uuidToString(runtime.ID), Name: runtime.Name,
				}
			}
		}
		if task.WorkDir.Valid {
			card.WorkDir = relativeWorkDir(
				task.WorkDir.String, uuidToString(workspaceID), uuidToString(task.ID),
			)
		}
		if task.SessionID.Valid {
			card.SessionID = task.SessionID.String
		}
		return card
	}
	return nil
}

// writeTakeoverNote records the handover on the issue timeline. author_type
// 'system' with the zero UUID matches the other machine-written comments; the
// note is the takeover's audit record, so a failure is logged but never fails
// the takeover that already happened.
func (h *Handler) writeTakeoverNote(
	ctx context.Context,
	issue db.Issue,
	userUUID pgtype.UUID,
	fromAgent db.Agent,
) {
	takerName := "A member"
	if user, err := h.Queries.GetUser(ctx, userUUID); err == nil && user.Name != "" {
		takerName = user.Name
	}
	content := fmt.Sprintf("%s took over this issue", takerName)
	if fromAgent.ID.Valid {
		content += fmt.Sprintf(" from %s; in-flight agent tasks were cancelled", fromAgent.Name)
	}
	content += "."
	if _, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     content,
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	}); err != nil {
		slog.Warn("takeover: note comment failed",
			"issue_id", uuidToString(issue.ID), "error", err)
	}
}
