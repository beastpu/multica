package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/util"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type workflowSubmissionResponse struct {
	ID                     string          `json:"id"`
	WorkflowNodeInstanceID string          `json:"workflow_node_instance_id"`
	Revision               int32           `json:"revision"`
	Status                 string          `json:"status"`
	Payload                json.RawMessage `json:"payload"`
	Summary                string          `json:"summary"`
	Evidence               json.RawMessage `json:"evidence"`
	SubmittedByType        string          `json:"submitted_by_type"`
	SubmittedByID          *string         `json:"submitted_by_id"`
	SourceIssueID          *string         `json:"source_issue_id"`
	SourceAgentRunID       *string         `json:"source_agent_run_id"`
	CreatedAt              string          `json:"created_at"`
}

type workflowVerdictResponse struct {
	ID                     string          `json:"id"`
	WorkflowNodeInstanceID string          `json:"workflow_node_instance_id"`
	Revision               int32           `json:"revision"`
	Result                 string          `json:"result"`
	Reason                 string          `json:"reason"`
	Confidence             *float64        `json:"confidence"`
	Evidence               json.RawMessage `json:"evidence"`
	Basis                  json.RawMessage `json:"basis"`
	EvaluatorType          string          `json:"evaluator_type"`
	EvaluatorID            *string         `json:"evaluator_id"`
	CreatedAt              string          `json:"created_at"`
}

type workflowAcceptanceResponse struct {
	ID                     string          `json:"id"`
	WorkflowNodeInstanceID string          `json:"workflow_node_instance_id"`
	Revision               int32           `json:"revision"`
	Status                 string          `json:"status"`
	DecidedByType          *string         `json:"decided_by_type"`
	DecidedByID            *string         `json:"decided_by_id"`
	Reason                 string          `json:"reason"`
	ReworkTargetNodeKey    *string         `json:"rework_target_node_key"`
	Evidence               json.RawMessage `json:"evidence"`
	DecidedAt              *string         `json:"decided_at"`
	CreatedAt              string          `json:"created_at"`
}

func workflowSubmissionToResponse(row db.WorkflowNodeSubmission) workflowSubmissionResponse {
	return workflowSubmissionResponse{
		ID: uuidToString(row.ID), WorkflowNodeInstanceID: uuidToString(row.WorkflowNodeInstanceID),
		Revision: row.Revision, Status: row.Status, Payload: json.RawMessage(row.Payload),
		Summary: row.Summary, Evidence: json.RawMessage(row.Evidence),
		SubmittedByType: row.SubmittedByType, SubmittedByID: uuidToPtr(row.SubmittedByID),
		SourceIssueID: uuidToPtr(row.SourceIssueID), SourceAgentRunID: uuidToPtr(row.SourceAgentRunID),
		CreatedAt: timestampToString(row.CreatedAt),
	}
}

func workflowVerdictToResponse(row db.WorkflowNodeVerdict) workflowVerdictResponse {
	var confidence *float64
	if row.Confidence.Valid {
		value := row.Confidence.Float64
		confidence = &value
	}
	return workflowVerdictResponse{
		ID: uuidToString(row.ID), WorkflowNodeInstanceID: uuidToString(row.WorkflowNodeInstanceID),
		Revision: row.Revision, Result: row.Result, Reason: row.Reason, Confidence: confidence,
		Evidence: json.RawMessage(row.Evidence), Basis: json.RawMessage(row.Basis),
		EvaluatorType: row.EvaluatorType, EvaluatorID: uuidToPtr(row.EvaluatorID),
		CreatedAt: timestampToString(row.CreatedAt),
	}
}

func workflowAcceptanceToResponse(row db.WorkflowAcceptance) workflowAcceptanceResponse {
	return workflowAcceptanceResponse{
		ID: uuidToString(row.ID), WorkflowNodeInstanceID: uuidToString(row.WorkflowNodeInstanceID),
		Revision: row.Revision, Status: row.Status, DecidedByType: textToPtr(row.DecidedByType),
		DecidedByID: uuidToPtr(row.DecidedByID), Reason: row.Reason,
		ReworkTargetNodeKey: textToPtr(row.ReworkTargetNodeKey), Evidence: json.RawMessage(row.Evidence),
		DecidedAt: timestampToPtr(row.DecidedAt), CreatedAt: timestampToString(row.CreatedAt),
	}
}

func (h *Handler) loadWorkflowNode(w http.ResponseWriter, r *http.Request) (db.WorkflowNodeInstance, db.WorkflowInstance, bool) {
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return db.WorkflowNodeInstance{}, db.WorkflowInstance{}, false
	}
	nodeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "nodeInstanceId"), "workflow_node_instance_id")
	if !ok {
		return db.WorkflowNodeInstance{}, db.WorkflowInstance{}, false
	}
	node, err := h.Queries.GetWorkflowNodeInstanceInWorkspace(r.Context(), db.GetWorkflowNodeInstanceInWorkspaceParams{
		ID: nodeID, WorkspaceID: wsUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "workflow node instance not found")
		return db.WorkflowNodeInstance{}, db.WorkflowInstance{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow node")
		return db.WorkflowNodeInstance{}, db.WorkflowInstance{}, false
	}
	instance, err := h.Queries.GetWorkflowInstanceInWorkspace(r.Context(), db.GetWorkflowInstanceInWorkspaceParams{
		ID: node.WorkflowInstanceID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "workflow instance not found")
		return db.WorkflowNodeInstance{}, db.WorkflowInstance{}, false
	}
	return node, instance, true
}

func (h *Handler) GetWorkflowNodeInstance(w http.ResponseWriter, r *http.Request) {
	node, instance, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	tasks, err := h.Queries.ListWorkflowNodeTasks(r.Context(), db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow node tasks")
		return
	}
	executions, err := h.Queries.ListWorkflowNodeWorkerAgentTasks(
		r.Context(),
		db.ListWorkflowNodeWorkerAgentTasksParams{
			WorkspaceID:            node.WorkspaceID,
			WorkflowNodeInstanceID: node.ID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow node executions")
		return
	}
	submissions, err := h.Queries.ListWorkflowSubmissions(r.Context(), db.ListWorkflowSubmissionsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow submissions")
		return
	}
	verdicts, err := h.Queries.ListWorkflowVerdicts(r.Context(), db.ListWorkflowVerdictsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow verdicts")
		return
	}
	participants, err := h.Queries.ListWorkflowNodeParticipants(
		r.Context(),
		db.ListWorkflowNodeParticipantsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow participants")
		return
	}
	resolutions, err := h.Queries.ListWorkflowExecutorResolutions(
		r.Context(),
		db.ListWorkflowExecutorResolutionsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow executor resolutions")
		return
	}
	taskResponses := make([]workflowTaskResponse, len(tasks))
	for i, task := range tasks {
		taskResponses[i] = workflowTaskToResponse(task)
	}
	executionResponses := make([]AgentTaskResponse, len(executions))
	for i, execution := range executions {
		executionResponses[i] = taskToResponse(execution, uuidToString(node.WorkspaceID))
	}
	h.hydrateTaskAttributions(
		r.Context(),
		attributionsOf(executionResponses),
	)
	submissionResponses := make([]workflowSubmissionResponse, len(submissions))
	for i, submission := range submissions {
		submissionResponses[i] = workflowSubmissionToResponse(submission)
	}
	verdictResponses := make([]workflowVerdictResponse, len(verdicts))
	for i, verdict := range verdicts {
		verdictResponses[i] = workflowVerdictToResponse(verdict)
	}
	participantResponses := make([]map[string]any, len(participants))
	for i, participant := range participants {
		participantResponses[i] = map[string]any{
			"id": uuidToString(participant.ID), "role": participant.Role,
			"actor_type": participant.ActorType,
			"actor_id":   uuidToString(participant.ActorID),
			"created_at": timestampToString(participant.CreatedAt),
		}
	}
	resolutionResponses := make([]map[string]any, len(resolutions))
	for i, resolution := range resolutions {
		resolutionResponses[i] = workflowExecutorResolutionToResponse(resolution)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance": h.workflowInstanceToRuntimeResponse(r.Context(), instance), "node": workflowNodeToResponse(node),
		"tasks": taskResponses, "executions": executionResponses,
		"submissions": submissionResponses, "verdicts": verdictResponses,
		"participants": participantResponses, "executor_resolutions": resolutionResponses,
	})
}

func (h *Handler) ListWorkflowNodeIssues(w http.ResponseWriter, r *http.Request) {
	node, _, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	issues, err := h.Queries.ListWorkflowNodeIssues(r.Context(), db.ListWorkflowNodeIssuesParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow node issues")
		return
	}
	prefix := h.getIssuePrefix(r.Context(), node.WorkspaceID)
	items := make([]IssueResponse, len(issues))
	issueIDs := make([]pgtype.UUID, len(issues))
	for i, issue := range issues {
		issueIDs[i] = issue.ID
	}
	workflowContexts := h.issueWorkflowContextsByIssue(
		r.Context(),
		node.WorkspaceID,
		issueIDs,
		prefix,
	)
	for i, issue := range issues {
		items[i] = issueToResponse(issue, prefix)
		items[i].WorkflowContext = workflowContexts[items[i].ID]
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": items, "total": len(items)})
}

func (h *Handler) ListWorkflowNodeSubmissions(w http.ResponseWriter, r *http.Request) {
	node, _, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListWorkflowSubmissions(r.Context(), db.ListWorkflowSubmissionsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow submissions")
		return
	}
	items := make([]workflowSubmissionResponse, len(rows))
	for i, row := range rows {
		items[i] = workflowSubmissionToResponse(row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"submissions": items})
}

type createWorkflowSubmissionRequest struct {
	Payload          map[string]any  `json:"payload"`
	Summary          string          `json:"summary"`
	Evidence         json.RawMessage `json:"evidence,omitempty"`
	SourceIssueID    string          `json:"source_issue_id,omitempty"`
	SourceAgentRunID string          `json:"source_agent_run_id,omitempty"`
	IdempotencyKey   string          `json:"idempotency_key"`
}

func (h *Handler) CreateWorkflowNodeSubmission(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	var req createWorkflowSubmissionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "payload must be a JSON object")
		return
	}
	// A node can owe a conclusion without owing a structured result — that is
	// the common shape now that nodes declare artifacts instead of fields. An
	// omitted payload therefore means "empty", not "malformed"; the schema, if
	// there is one, still decides whether empty is acceptable.
	if req.Payload == nil {
		req.Payload = map[string]any{}
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return
	}
	node, instance, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	var nodeDefinition workflowdomain.NodeDefinition
	if err := json.Unmarshal(node.DefinitionSnapshot, &nodeDefinition); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow node snapshot")
		return
	}
	submissionPolicy := workflowdomain.SubmissionPolicy(nodeDefinition)
	// The policy governs structured results, not the handoff conclusion. A node
	// that declares no schema still owes the next node a summary — refusing it
	// here would leave the default node shape, which produces no issues and no
	// fields, with nowhere to hand anything off from.
	// Declared outputs are their own grant: a node that names structured
	// fields accepts them regardless of its submission policy, which predates
	// outputs and defaults to none.
	if submissionPolicy == "none" && len(nodeDefinition.Outputs) == 0 && len(req.Payload) > 0 {
		writeError(w, http.StatusConflict, "workflow node does not accept member submissions")
		return
	}
	// A summary longer than the cap defeats its purpose: downstream is meant to
	// read it whole without deciding whether to. Reject at the boundary so the
	// author can trim it, rather than truncating and silently losing meaning.
	if len([]rune(strings.TrimSpace(req.Summary))) > workflowdomain.MaxHandoffSummaryChars {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"handoff summary exceeds %d characters", workflowdomain.MaxHandoffSummaryChars,
		))
		return
	}
	// The node's declared outputs are the contract: values are validated and
	// normalized before anything is stored, and a failure names every field so
	// the submitter — human or agent — can fix its own submission and retry.
	// Rejecting outright, rather than storing an "invalid" row, is deliberate:
	// a submission missing a required field would otherwise silently route the
	// gateway to else, which is exactly the failure mode outputs replace.
	if len(nodeDefinition.Outputs) > 0 {
		normalized, fieldErrors := workflowdomain.ValidateOutputValues(
			nodeDefinition.Outputs, req.Payload,
		)
		if len(fieldErrors) > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":  "output_validation_failed",
				"fields": fieldErrors,
			})
			return
		}
		req.Payload = normalized
	}
	var reasons []workflowdomain.WaitingReason
	status := "valid"
	payload, _ := json.Marshal(req.Payload)
	evidence, ok := normalizeWorkflowJSONArray(w, req.Evidence, "evidence")
	if !ok {
		return
	}
	sourceIssueID, ok := parseOptionalWorkflowUUID(w, req.SourceIssueID, "source_issue_id")
	if !ok {
		return
	}
	sourceAgentRunID, ok := parseOptionalWorkflowUUID(w, req.SourceAgentRunID, "source_agent_run_id")
	if !ok {
		return
	}
	if sourceIssueID.Valid {
		sourceTask, sourceErr := h.Queries.GetWorkflowNodeTaskByIssue(
			r.Context(),
			db.GetWorkflowNodeTaskByIssueParams{
				IssueID: sourceIssueID, WorkspaceID: node.WorkspaceID,
			},
		)
		if errors.Is(sourceErr, pgx.ErrNoRows) ||
			sourceTask.WorkflowNodeInstanceID != node.ID {
			writeError(w, http.StatusBadRequest, "source_issue_id must belong to this workflow node")
			return
		}
		if sourceErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate source issue")
			return
		}
	}
	if (submissionPolicy == "per_required_task" || submissionPolicy == "fan_in") &&
		!sourceIssueID.Valid {
		writeError(
			w,
			http.StatusBadRequest,
			"source_issue_id is required by the node submission policy",
		)
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorIDText := h.resolveActor(
		r, userID, uuidToString(node.WorkspaceID),
	)
	actorID, ok := parseUUIDOrBadRequest(w, actorIDText, "actor_id")
	if !ok {
		return
	}
	allowed := false
	var permissionErr error
	if actorType == "agent" {
		allowed, permissionErr = h.canAgentSubmitWorkflowNode(
			r.Context(), node, actorID,
		)
	} else {
		allowed, permissionErr = h.canSubmitWorkflowNode(
			r.Context(), instance, node, nodeDefinition, actorID,
		)
	}
	if permissionErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify workflow node permission")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "only an assigned node actor can submit a result")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.LockWorkflowInstance(r.Context(), db.LockWorkflowInstanceParams{ID: instance.ID, WorkspaceID: instance.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	if event, err := qtx.GetWorkflowEventByIdempotencyKey(r.Context(), db.GetWorkflowEventByIdempotencyKeyParams{
		WorkflowInstanceID: locked.ID, WorkspaceID: locked.WorkspaceID, IdempotencyKey: idempotencyKey,
	}); err == nil {
		var eventPayload struct {
			SubmissionID string `json:"submission_id"`
		}
		if json.Unmarshal(event.Payload, &eventPayload) == nil {
			submissionID, parseOK := parseUUIDOrBadRequest(w, eventPayload.SubmissionID, "submission_id")
			if !parseOK {
				return
			}
			submission, getErr := qtx.GetWorkflowSubmissionInWorkspace(r.Context(), db.GetWorkflowSubmissionInWorkspaceParams{
				ID: submissionID, WorkspaceID: locked.WorkspaceID,
			})
			if getErr == nil {
				tx.Rollback(r.Context())
				h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
				writeJSON(w, http.StatusOK, map[string]any{
					"submission": workflowSubmissionToResponse(submission), "validation_errors": reasons,
				})
				return
			}
		}
	}
	currentNode, err := qtx.GetWorkflowNodeInstanceInWorkspace(
		r.Context(),
		db.GetWorkflowNodeInstanceInWorkspaceParams{
			ID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil ||
		locked.Status != "running" ||
		!workflowNodeIsOpen(currentNode) {
		writeError(
			w,
			http.StatusConflict,
			"workflow node is not accepting submissions",
		)
		return
	}
	revision, err := qtx.GetNextWorkflowSubmissionRevision(r.Context(), db.GetNextWorkflowSubmissionRevisionParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate submission revision")
		return
	}
	submission, err := qtx.CreateWorkflowSubmission(r.Context(), db.CreateWorkflowSubmissionParams{
		WorkspaceID: node.WorkspaceID, WorkflowInstanceID: instance.ID, WorkflowNodeInstanceID: node.ID,
		Revision: revision, Status: status, Payload: payload, Summary: strings.TrimSpace(req.Summary),
		Evidence: evidence, SubmittedByType: actorType, SubmittedByID: actorID,
		SourceIssueID: sourceIssueID, SourceAgentRunID: sourceAgentRunID, SchemaVersion: 1,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "submission revision changed; retry the request")
		return
	}
	if status == "valid" {
		if err := qtx.SetWorkflowNodeLatestSubmission(r.Context(), db.SetWorkflowNodeLatestSubmissionParams{
			LatestSubmissionID: submission.ID, ID: node.ID, WorkspaceID: node.WorkspaceID,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update current submission")
			return
		}
	}
	eventPayload, _ := json.Marshal(map[string]any{
		"submission_id": uuidToString(submission.ID), "revision": submission.Revision, "status": submission.Status,
	})
	if _, err := qtx.CreateWorkflowEvent(r.Context(), db.CreateWorkflowEventParams{
		WorkspaceID: node.WorkspaceID, WorkflowInstanceID: instance.ID, WorkflowNodeInstanceID: node.ID,
		EventType: "node.submission_created", ActorType: actorType, ActorID: actorID,
		IdempotencyKey: idempotencyKey, Payload: eventPayload,
	}); err != nil {
		writeError(w, http.StatusConflict, "submission has already been recorded")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit submission")
		return
	}
	h.Metrics.RecordWorkflowSubmission(status)
	if status == "valid" {
		_, _ = h.reconcileWorkflowInstance(
			r.Context(), node.WorkspaceID, instance.ID, actorType, actorID,
			"submission:"+uuidToString(submission.ID),
		)
	}
	h.publishWorkflowRealtime(
		protocol.EventWorkflowSubmissionCreated,
		uuidToString(node.WorkspaceID), actorType, actorIDText,
		map[string]any{
			"workflow_instance_id":      uuidToString(instance.ID),
			"workflow_node_instance_id": uuidToString(node.ID),
			"workflow_submission_id":    uuidToString(submission.ID),
		},
	)
	h.publishWorkflowNodeUpdated(
		uuidToString(node.WorkspaceID), actorType, actorIDText,
		uuidToString(instance.ID), uuidToString(node.ID),
	)
	responseStatus := http.StatusCreated
	if status == "invalid" {
		responseStatus = http.StatusUnprocessableEntity
	}
	writeJSON(w, responseStatus, map[string]any{
		"submission": workflowSubmissionToResponse(submission), "validation_errors": reasons,
	})
}

func (h *Handler) canSubmitWorkflowNode(
	ctx context.Context,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	userID pgtype.UUID,
) (bool, error) {
	if nodeDefinition.OwnerRole == "" {
		participants, err := h.Queries.ListWorkflowNodeParticipants(
			ctx,
			db.ListWorkflowNodeParticipantsParams{
				WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
			},
		)
		if err != nil {
			return false, err
		}
		hasOwner := false
		for _, participant := range participants {
			if participant.Role != "owner" {
				continue
			}
			hasOwner = true
			if participant.ActorType == "member" && participant.ActorID == userID {
				return true, nil
			}
		}
		if hasOwner {
			return false, nil
		}
		return instance.StartedByType == "member" && instance.StartedByID == userID, nil
	}
	assignments, err := h.Queries.ListWorkflowRoleAssignments(ctx, db.ListWorkflowRoleAssignmentsParams{
		WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		if assignment.RoleKey == nodeDefinition.OwnerRole &&
			assignment.ActorType == "member" && assignment.ActorID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (h *Handler) canAgentSubmitWorkflowNode(
	ctx context.Context,
	node db.WorkflowNodeInstance,
	agentID pgtype.UUID,
) (bool, error) {
	participants, err := h.Queries.ListWorkflowNodeParticipants(
		ctx,
		db.ListWorkflowNodeParticipantsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil {
		return false, err
	}
	for _, participant := range participants {
		switch participant.ActorType {
		case "agent":
			if participant.ActorID == agentID {
				return true, nil
			}
		case "squad":
			member, memberErr := h.Queries.IsSquadMember(
				ctx,
				db.IsSquadMemberParams{
					SquadID: participant.ActorID, MemberType: "agent",
					MemberID: agentID,
				},
			)
			if memberErr != nil {
				return false, memberErr
			}
			if member {
				return true, nil
			}
		}
	}
	resolutions, err := h.Queries.ListWorkflowExecutorResolutions(
		ctx,
		db.ListWorkflowExecutorResolutionsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil {
		return false, err
	}
	for _, resolution := range resolutions {
		if resolution.Status != "resolved" || !resolution.ActorID.Valid ||
			!resolution.ActorType.Valid {
			continue
		}
		switch resolution.ActorType.String {
		case "agent":
			if resolution.ActorID == agentID {
				return true, nil
			}
		case "squad":
			member, memberErr := h.Queries.IsSquadMember(
				ctx,
				db.IsSquadMemberParams{
					SquadID: resolution.ActorID, MemberType: "agent",
					MemberID: agentID,
				},
			)
			if memberErr != nil {
				return false, memberErr
			}
			if member {
				return true, nil
			}
		}
	}
	return false, nil
}

func workflowReviewerAssignment(
	ctx context.Context,
	q *db.Queries,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
) (string, pgtype.UUID, bool, error) {
	reviewer := nodeDefinition.Reviewer
	if reviewer == nil {
		return "", pgtype.UUID{}, false, nil
	}
	role := "reviewer"
	if reviewer.Kind == "owner" {
		role = "owner"
	}
	if reviewer.Kind == "actor" {
		actorID, err := util.ParseUUID(reviewer.ActorID)
		if err != nil {
			return "", pgtype.UUID{}, false, err
		}
		return reviewer.ActorType, actorID, true, nil
	}
	if reviewer.Kind != "role" && reviewer.Kind != "owner" {
		return "", pgtype.UUID{}, false, nil
	}
	participants, err := q.ListWorkflowNodeParticipants(
		ctx,
		db.ListWorkflowNodeParticipantsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil {
		return "", pgtype.UUID{}, false, err
	}
	for _, participant := range participants {
		if participant.Role == role {
			return participant.ActorType, participant.ActorID, true, nil
		}
	}
	return "", pgtype.UUID{}, false, nil
}

func workflowActorMatchesReviewer(
	ctx context.Context,
	q *db.Queries,
	reviewerType string,
	reviewerID pgtype.UUID,
	actorType string,
	actorID pgtype.UUID,
) (bool, error) {
	switch reviewerType {
	case "member", "agent":
		return reviewerType == actorType && reviewerID == actorID, nil
	case "squad":
		if actorType != "agent" {
			return false, nil
		}
		return q.IsSquadMember(ctx, db.IsSquadMemberParams{
			SquadID: reviewerID, MemberType: "agent", MemberID: actorID,
		})
	default:
		return false, nil
	}
}

func normalizeWorkflowJSONArray(w http.ResponseWriter, raw json.RawMessage, field string) ([]byte, bool) {
	if len(raw) == 0 {
		return []byte("[]"), true
	}
	var values []any
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		writeError(w, http.StatusBadRequest, field+" must be a JSON array")
		return nil, false
	}
	normalized, _ := json.Marshal(values)
	return normalized, true
}

func parseOptionalWorkflowUUID(w http.ResponseWriter, value, field string) (pgtype.UUID, bool) {
	if strings.TrimSpace(value) == "" {
		return pgtype.UUID{}, true
	}
	return parseUUIDOrBadRequest(w, value, field)
}

func (h *Handler) ListWorkflowNodeVerdicts(w http.ResponseWriter, r *http.Request) {
	node, _, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListWorkflowVerdicts(r.Context(), db.ListWorkflowVerdictsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workflow verdicts")
		return
	}
	items := make([]workflowVerdictResponse, len(rows))
	for i, row := range rows {
		items[i] = workflowVerdictToResponse(row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"verdicts": items})
}

type createWorkflowVerdictRequest struct {
	Result         string          `json:"result"`
	Reason         string          `json:"reason"`
	Confidence     *float64        `json:"confidence,omitempty"`
	Evidence       json.RawMessage `json:"evidence,omitempty"`
	IdempotencyKey string          `json:"idempotency_key"`
}

func (h *Handler) CreateWorkflowNodeVerdict(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	// Who is calling is settled before what they sent. An agent is refused
	// here whatever its body says, and it has to learn that from its first
	// attempt: WTE-14841's Critic met "invalid request body" fourteen times,
	// each rejection an invitation to guess again, and never reached the one
	// sentence that would have stopped it.
	node, instance, ok := h.loadWorkflowNode(w, r)
	if !ok {
		return
	}
	var nodeDefinition workflowdomain.NodeDefinition
	if err := json.Unmarshal(node.DefinitionSnapshot, &nodeDefinition); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow node snapshot")
		return
	}
	if nodeDefinition.Reviewer == nil {
		writeError(w, http.StatusConflict, "workflow node does not accept verdicts")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorType, actorIDText := h.resolveActor(
		r, userID, uuidToString(node.WorkspaceID),
	)
	actorID, ok := parseUUIDOrBadRequest(w, actorIDText, "actor_id")
	if !ok {
		return
	}
	if actorType == "agent" {
		writeError(
			w, http.StatusConflict,
			"agent reviewer verdicts are recorded from Critic task completion, "+
				"not from this endpoint: run `multica workflow review --decision "+
				strings.Join(workflowdomain.CriticResults, "|")+
				" --reason \"...\"` and finish the task instead",
		)
		return
	}

	var req createWorkflowVerdictRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch req.Result {
	case "pass":
	case "fail", "blocked":
		if strings.TrimSpace(req.Reason) == "" {
			writeError(w, http.StatusBadRequest, "reason is required for fail or blocked verdicts")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "result must be pass, fail, or blocked")
		return
	}
	if req.Confidence != nil && (*req.Confidence < 0 || *req.Confidence > 1) {
		writeError(w, http.StatusBadRequest, "confidence must be between 0 and 1")
		return
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return
	}
	evidence, ok := normalizeWorkflowJSONArray(w, req.Evidence, "evidence")
	if !ok {
		return
	}
	reviewerType, reviewerID, resolved, err := workflowReviewerAssignment(
		r.Context(), h.Queries, node, nodeDefinition,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve workflow reviewer")
		return
	}
	if !resolved {
		writeError(w, http.StatusConflict, "workflow reviewer is not assigned")
		return
	}
	eventAction := "member_verdict"
	allowed, err := workflowActorMatchesReviewer(
		r.Context(), h.Queries, reviewerType, reviewerID, actorType, actorID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify workflow verdict permission")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "only an assigned node actor can record a verdict")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start workflow transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.LockWorkflowInstance(r.Context(), db.LockWorkflowInstanceParams{
		ID: instance.ID, WorkspaceID: instance.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "workflow instance changed; refresh and try again")
		return
	}
	if event, eventErr := qtx.GetWorkflowEventByIdempotencyKey(
		r.Context(),
		db.GetWorkflowEventByIdempotencyKeyParams{
			WorkflowInstanceID: locked.ID,
			WorkspaceID:        locked.WorkspaceID,
			IdempotencyKey:     req.IdempotencyKey,
		},
	); eventErr == nil {
		var payload struct {
			Action    string `json:"action"`
			VerdictID string `json:"verdict_id"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil && payload.Action == eventAction {
			verdictID, parseOK := parseUUIDOrBadRequest(w, payload.VerdictID, "verdict_id")
			if !parseOK {
				return
			}
			existing, getErr := qtx.GetWorkflowVerdictInWorkspace(
				r.Context(),
				db.GetWorkflowVerdictInWorkspaceParams{
					ID: verdictID, WorkspaceID: node.WorkspaceID,
				},
			)
			if getErr == nil && existing.WorkflowNodeInstanceID == node.ID {
				h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
				writeJSON(w, http.StatusOK, map[string]any{
					"verdict": workflowVerdictToResponse(existing),
				})
				return
			}
		}
		writeError(w, http.StatusConflict, "idempotency_key was already used for another workflow action")
		return
	}
	currentNode, err := qtx.GetWorkflowNodeInstanceInWorkspace(
		r.Context(),
		db.GetWorkflowNodeInstanceInWorkspaceParams{
			ID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil ||
		locked.Status != "running" ||
		!workflowNodeIsOpen(currentNode) {
		writeError(w, http.StatusConflict, "workflow node is not accepting verdicts")
		return
	}
	submissions, err := qtx.ListWorkflowSubmissions(
		r.Context(),
		db.ListWorkflowSubmissionsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow submissions")
		return
	}
	var submission db.WorkflowNodeSubmission
	for _, candidate := range submissions {
		if candidate.Status == "valid" {
			submission = candidate
			break
		}
	}
	if !submission.ID.Valid {
		writeError(w, http.StatusConflict, "a valid submission is required before recording a verdict")
		return
	}
	artifactIDs, err := reviewWorkflowArtifactsForVerdict(
		r.Context(), qtx, node.WorkspaceID, currentNode, nodeDefinition,
		req.Result, strings.TrimSpace(req.Reason), actorID,
	)
	if err != nil {
		writeError(w, http.StatusConflict, "workflow artifacts are not ready for review: "+err.Error())
		return
	}
	revision, err := qtx.GetNextWorkflowVerdictRevision(
		r.Context(),
		db.GetNextWorkflowVerdictRevisionParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to allocate verdict revision")
		return
	}
	var confidence pgtype.Float8
	if req.Confidence != nil {
		confidence = pgtype.Float8{Float64: *req.Confidence, Valid: true}
	}
	basis, _ := json.Marshal(map[string]any{
		"submission_id": uuidToString(submission.ID),
		"kind":          eventAction,
		"artifact_ids":  artifactIDs,
	})
	definitionSnapshot, _ := json.Marshal(nodeDefinition.Reviewer)
	verdict, err := qtx.CreateWorkflowVerdict(
		r.Context(),
		db.CreateWorkflowVerdictParams{
			WorkspaceID: node.WorkspaceID, WorkflowInstanceID: instance.ID,
			WorkflowNodeInstanceID: node.ID, Revision: revision,
			Result: req.Result, Reason: strings.TrimSpace(req.Reason),
			Confidence: confidence, Evidence: evidence, Basis: basis,
			EvaluatorType: actorType, EvaluatorID: actorID,
			DefinitionSnapshot: definitionSnapshot,
		},
	)
	if err != nil {
		writeError(w, http.StatusConflict, "verdict revision changed; retry the request")
		return
	}
	if err := qtx.SetWorkflowNodeLatestVerdict(
		r.Context(),
		db.SetWorkflowNodeLatestVerdictParams{
			LatestVerdictID: verdict.ID, ID: node.ID, WorkspaceID: node.WorkspaceID,
		},
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update current verdict")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"action":     eventAction,
		"verdict_id": uuidToString(verdict.ID),
		"revision":   verdict.Revision,
		"result":     verdict.Result,
	})
	if _, err := qtx.CreateWorkflowEvent(r.Context(), db.CreateWorkflowEventParams{
		WorkspaceID: locked.WorkspaceID, WorkflowInstanceID: locked.ID,
		WorkflowNodeInstanceID: node.ID, EventType: "node.verdict_recorded",
		ActorType: actorType, ActorID: actorID,
		IdempotencyKey: req.IdempotencyKey, Payload: payload,
	}); err != nil {
		writeError(w, http.StatusConflict, "verdict has already been recorded")
		return
	}
	updatedInstance := locked
	var reworkNode db.WorkflowNodeInstance
	if req.Result == "fail" {
		updatedInstance, reworkNode, err = createWorkflowVerdictRework(
			r.Context(), qtx, locked, currentNode, nodeDefinition,
			strings.TrimSpace(req.Reason), actorType, actorID,
			"verdict-rework:"+req.IdempotencyKey,
		)
		if err != nil {
			writeError(w, http.StatusConflict, "failed to create workflow rework")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow verdict")
		return
	}
	reviewStatus := ""
	if req.Result == "pass" {
		reviewStatus = "approved"
	} else if req.Result == "fail" {
		reviewStatus = "rejected"
	}
	h.publishWorkflowArtifactsReviewed(
		node.WorkspaceID, instance.ID, node.ID, actorType, actorIDText,
		artifactIDs, reviewStatus,
	)
	h.Metrics.RecordWorkflowVerdict(actorType, verdict.Result)
	if reworkNode.ID.Valid {
		h.activateWorkflowVerdictRework(
			r.Context(), locked, updatedInstance, reworkNode,
		)
	} else {
		_, _ = h.reconcileWorkflowInstance(
			r.Context(), node.WorkspaceID, instance.ID, actorType, actorID,
			"verdict:"+uuidToString(verdict.ID),
		)
	}
	h.publishWorkflowRealtime(
		protocol.EventWorkflowVerdictCreated,
		uuidToString(node.WorkspaceID), actorType, actorIDText,
		map[string]any{
			"workflow_instance_id":      uuidToString(instance.ID),
			"workflow_node_instance_id": uuidToString(node.ID),
			"workflow_verdict_id":       uuidToString(verdict.ID),
		},
	)
	h.publishWorkflowNodeUpdated(
		uuidToString(node.WorkspaceID), actorType, actorIDText,
		uuidToString(instance.ID), uuidToString(node.ID),
	)
	if reworkNode.ID.Valid {
		h.publishWorkflowInstanceUpdated(
			uuidToString(node.WorkspaceID), actorType, actorIDText,
			uuidToString(instance.ID), uuidToString(reworkNode.ID),
		)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"verdict": workflowVerdictToResponse(verdict),
	})
}

type confirmWorkflowNodeRequest struct {
	Decision       string `json:"decision"`
	Comment        string `json:"comment"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (h *Handler) ReconcileWorkflowInstance(w http.ResponseWriter, r *http.Request) {
	if !h.workflowWriteEnabled(w, r) {
		return
	}
	if _, roleOK := h.requireWorkspaceRole(
		w,
		r,
		h.resolveWorkspaceID(r),
		"workspace not found",
		"owner",
		"admin",
	); !roleOK {
		return
	}
	req, ok := decodeWorkflowActionRequest(w, r)
	if !ok {
		return
	}
	instance, ok := h.loadWorkflowInstance(w, r)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user_id")
	if !ok {
		return
	}
	updated, err := h.reconcileWorkflowInstance(r.Context(), instance.WorkspaceID, instance.ID, "member", userUUID, req.IdempotencyKey)
	h.Metrics.RecordWorkflowHumanIntervention("reconcile")
	if err != nil {
		if errors.Is(err, errWorkflowNoop) {
			h.writeWorkflowInstanceDetail(w, r, instance, http.StatusOK)
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	h.publishWorkflowInstanceUpdated(
		uuidToString(updated.WorkspaceID), "member", userID,
		uuidToString(updated.ID), "",
	)
	h.publishWorkflowRealtime(
		protocol.EventWorkflowVerdictCreated,
		uuidToString(updated.WorkspaceID), "member", userID,
		workflowRealtimePayload(uuidToString(updated.ID), ""),
	)
	h.writeWorkflowInstanceDetail(w, r, updated, http.StatusOK)
}

var errWorkflowNoop = errors.New("workflow reconciliation made no transition")

// errWorkflowTransitionLimit reports that a run kept transitioning until the
// safety limit and never settled. It is named so the reconciler can back the
// instance off rather than re-claim it and spend the same work again.
var errWorkflowTransitionLimit = errors.New("workflow exceeded transition safety limit")

type workflowCriticDispatch struct {
	node       db.WorkflowNodeInstance
	definition workflowdomain.NodeDefinition
}

func (h *Handler) reconcileWorkflowInstance(
	ctx context.Context,
	workspaceID, instanceID pgtype.UUID,
	actorType string,
	actorID pgtype.UUID,
	idempotencyKey string,
) (result db.WorkflowInstance, resultErr error) {
	transitioned := false
	defer func() {
		switch {
		case resultErr == nil && transitioned:
			h.Metrics.RecordWorkflowOperation("reconcile", "repaired")
		case errors.Is(resultErr, errWorkflowNoop):
			h.Metrics.RecordWorkflowOperation("reconcile", "noop")
		case resultErr != nil:
			h.Metrics.RecordWorkflowOperation("reconcile", "failed")
		}
	}()
	if featureflags.WorkflowProgressionPaused(
		ctx,
		h.FeatureFlags,
		uuidToString(workspaceID),
	) {
		return db.WorkflowInstance{}, errWorkflowProgressionPaused
	}
	current, err := h.Queries.GetWorkflowInstanceInWorkspace(ctx, db.GetWorkflowInstanceInWorkspaceParams{
		ID: instanceID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return db.WorkflowInstance{}, err
	}
	maxTransitions := 128
	// Which activities completed, and how many times each. A run that reaches
	// the limit is cycling through some subset of its graph, and the counts name
	// that subset — the one thing a reader needs to start looking.
	nodeCompletions := map[string]int{}
	for step := 0; step < maxTransitions; step++ {
		tx, err := h.TxStarter.Begin(ctx)
		if err != nil {
			return current, err
		}
		qtx := h.Queries.WithTx(tx)
		locked, err := qtx.LockWorkflowInstance(ctx, db.LockWorkflowInstanceParams{ID: instanceID, WorkspaceID: workspaceID})
		if err != nil {
			tx.Rollback(ctx)
			return current, err
		}
		if locked.Status != "running" {
			tx.Rollback(ctx)
			if transitioned {
				return locked, nil
			}
			return locked, errWorkflowNoop
		}
		version, err := qtx.GetWorkflowVersionInWorkspace(ctx, db.GetWorkflowVersionInWorkspaceParams{
			ID: locked.WorkflowVersionID, WorkspaceID: workspaceID,
		})
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		definition, err := workflowdomain.ParseDefinition(version.Definition)
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		plan, err := workflowdomain.BuildGraphPlan(definition)
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		nodes, err := qtx.ListWorkflowNodeInstances(ctx, db.ListWorkflowNodeInstancesParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		roleRows, err := qtx.ListWorkflowRoleAssignments(ctx, db.ListWorkflowRoleAssignmentsParams{
			WorkflowInstanceID: locked.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		roleMap := workflowRoleAssignmentsMap(roleRows)
		propagated, err := h.propagateWorkflowGraph(
			ctx, qtx, workspaceID, locked, definition, plan, roleMap,
			nodes, actorType, actorID,
		)
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		repairedTasks := false
		needsSetup := propagated.NeedsSetup
		var completedNode db.WorkflowNodeInstance
		var completionSubmission db.WorkflowNodeSubmission
		var completionVerdict db.WorkflowNodeVerdict
		var blockedNodes []db.WorkflowNodeInstance
		var carrierSyncs []workflowCarrierSync
		var createdSubmissions []db.WorkflowNodeSubmission
		var createdVerdicts []db.WorkflowNodeVerdict
		var criticDispatches []workflowCriticDispatch
		for _, active := range workflowActiveNodes(propagated.Nodes, plan) {
			nodeDefinition, exists := plan.Node(active.NodeKey)
			if !exists || nodeDefinition.Kind != "activity" {
				continue
			}
			repaired, err := ensureWorkflowNodeTasks(
				ctx, qtx, workspaceID, locked, active, nodeDefinition,
			)
			if err != nil {
				tx.Rollback(ctx)
				return locked, err
			}
			repairedTasks = repairedTasks || repaired
			nodeNeedsSetup, err := workflowNodeNeedsExecutorSetup(
				ctx, qtx, workspaceID, active.ID,
			)
			if err != nil {
				tx.Rollback(ctx)
				return locked, err
			}
			if nodeNeedsSetup {
				needsSetup = true
				if !workflowNodeBlockedForExecutor(active) {
					reasons := workflowdomain.EncodeWaitingReasons([]workflowdomain.WaitingReason{{
						Code:    "executor_needs_setup",
						Message: "One or more workflow tasks require an executor",
					}})
					blocked, updateErr := qtx.UpdateWorkflowNodeState(
						ctx,
						db.UpdateWorkflowNodeStateParams{
							Status: "blocked", WaitingReasons: reasons, MarkReconciled: true,
							ID: active.ID, WorkspaceID: workspaceID,
							ExpectedStatus: active.Status,
						},
					)
					if updateErr != nil {
						tx.Rollback(ctx)
						return locked, updateErr
					}
					propagated.Nodes[active.NodeKey] = blocked
					blockedNodes = append(blockedNodes, blocked)
				}
				continue
			}
			ready, reasons, submission, verdict, err := h.evaluateWorkflowNode(
				ctx, qtx, workspaceID, locked, active, nodeDefinition, definition,
				true,
			)
			if err != nil {
				tx.Rollback(ctx)
				return locked, err
			}
			if submission.ID.Valid &&
				(!active.LatestSubmissionID.Valid ||
					active.LatestSubmissionID != submission.ID) {
				createdSubmissions = append(createdSubmissions, submission)
			}
			if verdict.ID.Valid &&
				(!active.LatestVerdictID.Valid ||
					active.LatestVerdictID != verdict.ID) {
				createdVerdicts = append(createdVerdicts, verdict)
			}
			if ready && !completedNode.ID.Valid {
				completedNode, err = qtx.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
					Status: "completed", WaitingReasons: []byte("[]"), MarkReconciled: true,
					ID: active.ID, WorkspaceID: workspaceID, ExpectedStatus: active.Status,
				})
				if err != nil {
					tx.Rollback(ctx)
					return locked, err
				}
				propagated.Nodes[active.NodeKey] = completedNode
				nodeCompletions[completedNode.NodeKey]++
				completionSubmission = submission
				completionVerdict = verdict
				continue
			}
			if !ready {
				nextStatus := "waiting"
				switch {
				case workflowWaitingReasonsBlockNode(reasons):
					nextStatus = "blocked"
				case workflowdomain.ReviewerAcceptsActor(nodeDefinition) &&
					workflowWaitingReasonsAwaitReview(reasons):
					nextStatus = "in_review"
				}
				updatedNode, err := qtx.UpdateWorkflowNodeState(ctx, db.UpdateWorkflowNodeStateParams{
					Status: nextStatus, WaitingReasons: workflowdomain.EncodeWaitingReasons(reasons),
					MarkReconciled: true, ID: active.ID, WorkspaceID: workspaceID,
					ExpectedStatus: active.Status,
				})
				if err != nil {
					tx.Rollback(ctx)
					return locked, err
				}
				propagated.Nodes[active.NodeKey] = updatedNode
				if workflowWaitingReasonsNeedReviewer(reasons) {
					criticDispatches = append(criticDispatches, workflowCriticDispatch{
						node: updatedNode, definition: nodeDefinition,
					})
				}
				if nextStatus == "blocked" && active.Status != "blocked" {
					blockedNodes = append(blockedNodes, updatedNode)
				}
				// Review and back again. Both directions have to reach the
				// carrier or the board stops tracking the node partway through
				// its own round trip: delivered work reads as still in
				// progress, and work sent back for more reads as still under
				// review.
				if nextStatus != active.Status &&
					(nextStatus == "in_review" || nextStatus == "waiting") {
					carrierSyncs = append(carrierSyncs, workflowCarrierSync{
						node: updatedNode, event: nextStatus,
					})
				}
			}
		}

		activated := append([]db.WorkflowNodeInstance(nil), propagated.Activated...)
		graphChanged := propagated.Changed
		canComplete := propagated.CanComplete
		if completedNode.ID.Valid {
			eventPayload := map[string]any{"node_key": completedNode.NodeKey}
			if completionSubmission.ID.Valid {
				eventPayload["submission_id"] = uuidToString(completionSubmission.ID)
			}
			if completionVerdict.ID.Valid {
				eventPayload["verdict_id"] = uuidToString(completionVerdict.ID)
			}
			payload, _ := json.Marshal(eventPayload)
			transitionKey := fmt.Sprintf("advance:%s:%d", uuidToString(completedNode.ID), completedNode.Attempt)
			if strings.TrimSpace(idempotencyKey) != "" && step == 0 {
				transitionKey = idempotencyKey + ":" + transitionKey
			}
			if _, err := qtx.CreateWorkflowEvent(ctx, db.CreateWorkflowEventParams{
				WorkspaceID: workspaceID, WorkflowInstanceID: locked.ID,
				WorkflowNodeInstanceID: completedNode.ID, EventType: "node.completed",
				ActorType: actorType, ActorID: actorID,
				IdempotencyKey: transitionKey, Payload: payload,
			}); err != nil {
				tx.Rollback(ctx)
				return locked, err
			}
			nodeValues := make([]db.WorkflowNodeInstance, 0, len(propagated.Nodes))
			for _, node := range propagated.Nodes {
				nodeValues = append(nodeValues, node)
			}
			afterCompletion, err := h.propagateWorkflowGraph(
				ctx, qtx, workspaceID, locked, definition, plan, roleMap,
				nodeValues, actorType, actorID,
			)
			if err != nil {
				tx.Rollback(ctx)
				return locked, err
			}
			activated = append(activated, afterCompletion.Activated...)
			graphChanged = graphChanged || afterCompletion.Changed
			needsSetup = needsSetup || afterCompletion.NeedsSetup
			canComplete = afterCompletion.CanComplete
		}

		targetStatus := locked.Status
		var result []byte
		if needsSetup {
			targetStatus = "needs_setup"
		} else if canComplete {
			targetStatus = "completed"
			result, _ = json.Marshal(map[string]any{
				"completed_node_key": completedNode.NodeKey,
			})
		}
		updated, err := qtx.UpdateWorkflowInstanceState(ctx, db.UpdateWorkflowInstanceStateParams{
			Status: targetStatus, Result: result, MarkReconciled: true,
			ID: locked.ID, WorkspaceID: workspaceID, ExpectedRevision: locked.Revision,
		})
		if err != nil {
			tx.Rollback(ctx)
			return locked, err
		}
		if err := tx.Commit(ctx); err != nil {
			return locked, err
		}
		h.recordWorkflowInstanceStatusTransition(locked.Status, updated.Status)
		if completedNode.ID.Valid {
			h.recordWorkflowNodeTransition(completedNode, "completed")
			h.syncWorkflowNodeIssueStatus(ctx, workspaceID, completedNode, "completed")
			if nodeDefinition, ok := plan.Node(completedNode.NodeKey); ok {
				h.applyWorkflowNodeActions(ctx, updated, nodeDefinition.OnComplete)
			}
		}
		for _, blockedNode := range blockedNodes {
			h.recordWorkflowNodeTransition(blockedNode, "blocked")
			h.syncWorkflowNodeIssueStatus(ctx, workspaceID, blockedNode, "blocked")
			// Blocking is only useful if somebody hears about it. The list
			// holds nodes that just crossed into blocked, so this notifies on
			// the transition rather than on every reconcile that finds them
			// still stuck.
			h.notifyWorkflowIntervention(
				ctx, updated, blockedNode,
				workflowWaitingReasonsForNotification(
					decodeWorkflowWaitingReasons(blockedNode.WaitingReasons),
				),
			)
		}
		for _, carrier := range carrierSyncs {
			h.syncWorkflowNodeIssueStatus(ctx, workspaceID, carrier.node, carrier.event)
		}
		h.recordWorkflowNodesActivated(ctx, activated)
		for range createdSubmissions {
			h.Metrics.RecordWorkflowSubmission("valid")
		}
		for _, verdict := range createdVerdicts {
			h.Metrics.RecordWorkflowVerdict(
				verdict.EvaluatorType,
				verdict.Result,
			)
		}
		if repairedTasks {
			h.Metrics.RecordWorkflowOperation("task_repair", "repaired")
		}
		if updated.Status == "completed" {
			if err := h.updateManagedWorkflowHostStatus(ctx, updated, "done"); err != nil {
				return updated, err
			}
		}
		stepTransitioned := completedNode.ID.Valid || graphChanged ||
			repairedTasks || updated.Status != locked.Status
		transitioned = transitioned || stepTransitioned
		current = updated
		h.applyWorkflowNodeEnterActions(ctx, updated, definition, activated)
		for _, node := range activated {
			h.materializeWorkflowNodeTasks(ctx, workspaceID, updated, node)
		}
		for _, dispatch := range criticDispatches {
			if err := h.ensureWorkflowAgentCriticTask(
				ctx, updated, dispatch.node, dispatch.definition,
			); err != nil {
				return updated, err
			}
		}
		if repairedTasks && h.WorkflowMaterializer != nil {
			h.WorkflowMaterializer.Notify()
		}
		if updated.Status == "completed" {
			return updated, nil
		}
		if !completedNode.ID.Valid {
			if transitioned {
				return updated, nil
			}
			return updated, errWorkflowNoop
		}
	}
	h.recordWorkflowTransitionLimit(ctx, current, maxTransitions, nodeCompletions)
	return current, errWorkflowTransitionLimit
}

// recordWorkflowTransitionLimit makes an exhausted reconcile findable.
//
// Every other way a run stops making progress writes a waiting reason and
// raises an intervention. This one returned a bare error that reached a log
// line and nothing else: the run stayed 'running' with no sign anything was
// wrong, and because the transitions it had just written made it due again,
// the reconciler re-claimed it every cycle to spend the same work.
//
// The idempotency key digests the completion counts, following the sweeper —
// the same cycle raises one finding, a different one is a new finding.
func (h *Handler) recordWorkflowTransitionLimit(
	ctx context.Context,
	instance db.WorkflowInstance,
	transitions int,
	nodeCompletions map[string]int,
) {
	cycling := make([]string, 0, len(nodeCompletions))
	for nodeKey := range nodeCompletions {
		cycling = append(cycling, nodeKey)
	}
	sort.Strings(cycling)
	var signature strings.Builder
	for _, nodeKey := range cycling {
		fmt.Fprintf(&signature, "%s=%d;", nodeKey, nodeCompletions[nodeKey])
	}
	digest := sha256.Sum256([]byte(signature.String()))
	payload, _ := json.Marshal(map[string]any{
		"transitions":      transitions,
		"node_completions": nodeCompletions,
	})
	event, err := h.Queries.CreateWorkflowEvent(ctx, db.CreateWorkflowEventParams{
		WorkspaceID:        instance.WorkspaceID,
		WorkflowInstanceID: instance.ID,
		EventType:          "workflow.transition_limit_exceeded",
		ActorType:          "system",
		IdempotencyKey:     fmt.Sprintf("transition-limit:%x", digest[:8]),
		Payload:            payload,
	})
	if err != nil || !event.ID.Valid {
		return
	}
	// The run is the subject, not any one activity. The limit says the graph as
	// a whole stopped settling, and the activity that happened to complete last
	// is a symptom — naming it would point the reader at the wrong thing.
	h.notifyWorkflowActionRequired(
		ctx, instance, nil, "transition_limit",
		"Workflow run stopped settling",
		fmt.Sprintf(
			"The run reached its %d-transition safety limit without settling and needs a person. Activities that kept completing: %s",
			transitions, strings.Join(cycling, ", "),
		),
		map[string]any{
			"transitions":      transitions,
			"node_completions": nodeCompletions,
		},
	)
}

func currentWorkflowNode(nodes []db.WorkflowNodeInstance) (db.WorkflowNodeInstance, bool) {
	var selected db.WorkflowNodeInstance
	found := false
	for _, node := range nodes {
		switch node.Status {
		case "active", "in_review", "waiting", "blocked":
			if !found || node.DisplayOrder < selected.DisplayOrder || (node.NodeKey == selected.NodeKey && node.Attempt > selected.Attempt) {
				selected = node
				found = true
			}
		}
	}
	return selected, found
}

func latestNodeByKey(nodes []db.WorkflowNodeInstance, key string) (db.WorkflowNodeInstance, error) {
	var selected db.WorkflowNodeInstance
	found := false
	for _, node := range nodes {
		if node.NodeKey == key && (!found || node.Attempt > selected.Attempt) {
			selected = node
			found = true
		}
	}
	if !found {
		return db.WorkflowNodeInstance{}, fmt.Errorf("workflow node %q not found", key)
	}
	return selected, nil
}

// workflowNodeTimeoutReason reports the timeout waiting reason for a node that
// has been active past its configured timeout.
//
// Both the reconciler and the sweeper persist a node's waiting reasons, and
// each rebuilds the list from scratch. Deriving the timeout here, from state
// both of them already hold, keeps the later writer from erasing what the
// earlier one recorded: an instance reads `node_timeout` to surface
// `blocked_or_timeout`, so dropping it makes a stalled activity look exactly
// like a healthy one.
func workflowNodeTimeoutReason(
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
) (workflowdomain.WaitingReason, bool) {
	if nodeDefinition.TimeoutMinutes <= 0 || !node.ActivatedAt.Valid {
		return workflowdomain.WaitingReason{}, false
	}
	deadline := node.ActivatedAt.Time.Add(
		time.Duration(nodeDefinition.TimeoutMinutes) * time.Minute,
	)
	if !time.Now().After(deadline) {
		return workflowdomain.WaitingReason{}, false
	}
	return workflowdomain.WaitingReason{
		Code:    "node_timeout",
		Message: "The activity exceeded its configured timeout",
	}, true
}

// evaluateWorkflowNode reports whether a node may complete, plus the reasons it
// cannot. The timeout is appended here rather than inside the readiness rules
// because it is not a readiness input — a node past its deadline still
// completes the moment its real obligations are met.
func (h *Handler) evaluateWorkflowNode(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	definition workflowdomain.Definition,
	includeManualCompletion bool,
) (bool, []workflowdomain.WaitingReason, db.WorkflowNodeSubmission, db.WorkflowNodeVerdict, error) {
	ready, reasons, submission, verdict, err := h.evaluateWorkflowNodeReadiness(
		ctx, q, workspaceID, instance, node, nodeDefinition, definition,
		includeManualCompletion,
	)
	if err != nil || ready {
		return ready, reasons, submission, verdict, err
	}
	if reason, timedOut := workflowNodeTimeoutReason(node, nodeDefinition); timedOut {
		reasons = appendWorkflowWaitingReason(reasons, reason)
	}
	return ready, reasons, submission, verdict, nil
}

func (h *Handler) evaluateWorkflowNodeReadiness(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	definition workflowdomain.Definition,
	includeManualCompletion bool,
) (bool, []workflowdomain.WaitingReason, db.WorkflowNodeSubmission, db.WorkflowNodeVerdict, error) {
	tasks, err := q.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, err
	}
	issues, err := q.ListWorkflowNodeIssues(ctx, db.ListWorkflowNodeIssuesParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, err
	}
	issueStatuses := make(map[pgtype.UUID]string, len(issues))
	for _, issue := range issues {
		issueStatuses[issue.ID] = issue.Status
	}
	reasons := make([]workflowdomain.WaitingReason, 0)
	// An activity that defers its issues to runtime has nothing to gate on
	// until someone decomposes it: with no tasks, "all required issues are
	// done" is true of the empty set, so the node activated and completed in
	// the same breath and the work it stood for never happened. That is the
	// one reading of "decide at runtime" nobody means.
	//
	// Checked against materialized tasks rather than the declaration, so a
	// fixed_and_dynamic node that did declare one is unaffected, and the block
	// clears the moment the first task exists.
	if workflowdomain.AllowsDynamicIssues(nodeDefinition) && len(tasks) == 0 {
		reasons = append(reasons, workflowdomain.WaitingReason{
			Code:    "awaiting_decomposition",
			Message: "Waiting for this activity to be broken into issues",
		})
	}
	requiredIssueOutcome := nodeDefinition.Completion.RequiredIssueOutcome
	if requiredIssueOutcome == "" {
		requiredIssueOutcome = "done"
	}
	for _, task := range tasks {
		if !task.Required {
			continue
		}
		if task.Source == "execution" {
			if task.MaterializationStatus != "materialized" {
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "direct_execution_not_dispatched", Field: task.TaskKey,
					Message: "Direct agent execution has not been dispatched",
				})
				continue
			}
			agentTask, taskErr := q.GetLatestAgentTaskForWorkflowNodeTask(ctx, task.ID)
			if taskErr != nil {
				if errors.Is(taskErr, pgx.ErrNoRows) {
					reasons = append(reasons, workflowdomain.WaitingReason{
						Code: "direct_execution_not_dispatched", Field: task.TaskKey,
						Message: "Direct agent execution has not been dispatched",
					})
					continue
				}
				return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, taskErr
			}
			switch agentTask.Status {
			case "completed":
			case "failed", "cancelled":
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "direct_execution_failed", Field: task.TaskKey,
					Message: "Direct agent execution did not complete successfully",
				})
			default:
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "direct_execution_running", Field: task.TaskKey,
					Message: "Direct agent execution is still running",
				})
			}
			continue
		}
		if requiredIssueOutcome == "none" {
			continue
		}
		if task.MaterializationStatus != "materialized" || !task.IssueID.Valid {
			reasons = append(reasons, workflowdomain.WaitingReason{
				Code: "required_task_not_materialized", Field: task.TaskKey,
				Message: "Required task has not been materialized",
			})
			continue
		}
		// Whoever did the work decides. When a run owns this task the run's
		// outcome is the answer and the issue is its mirror: an agent that
		// delivered has nothing left to do, and holding the node until someone
		// also drags the issue to done strands finished work behind a status
		// change nobody owes. Only a task no run ever claimed — a person
		// working in the issue — is answered by the issue's own status.
		owner, ownerErr := q.GetOwningAgentTaskForWorkflowNodeTask(
			ctx,
			db.GetOwningAgentTaskForWorkflowNodeTaskParams{
				WorkflowNodeTaskID: task.ID, IssueID: task.IssueID,
			},
		)
		if ownerErr != nil && !errors.Is(ownerErr, pgx.ErrNoRows) {
			return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, ownerErr
		}
		if ownerErr == nil {
			switch owner.Status {
			case "completed":
			case "failed", "cancelled":
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "direct_execution_failed", Field: task.TaskKey,
					Message: "Direct agent execution did not complete successfully",
				})
			default:
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "direct_execution_running", Field: task.TaskKey,
					Message: "Direct agent execution is still running",
				})
			}
			continue
		}
		status := issueStatuses[task.IssueID]
		outcomeSatisfied := status == "done"
		if requiredIssueOutcome == "terminal" {
			outcomeSatisfied = status == "done" || status == "cancelled"
		}
		if !outcomeSatisfied {
			code := "required_issue_not_done"
			message := "Required issue has not reached the configured outcome"
			if status == "cancelled" && requiredIssueOutcome == "done" {
				code = "required_issue_cancelled"
				message = "Required issue was cancelled under a done-only policy"
			}
			reasons = append(reasons, workflowdomain.WaitingReason{
				Code: code, Field: task.TaskKey, Message: message,
			})
		}
	}
	if required := workflowdomain.RequiredArtifacts(nodeDefinition); len(required) > 0 {
		artifacts, err := q.ListWorkflowNodeArtifacts(ctx, db.ListWorkflowNodeArtifactsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, err
		}
		delivered := make(map[string]db.WorkflowArtifact, len(artifacts))
		for _, artifact := range artifacts {
			delivered[artifact.ArtifactKey] = artifact
		}
		for _, requirement := range required {
			artifact, exists := delivered[requirement.Key]
			if !exists {
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "required_artifact_missing", Field: requirement.Key,
					Message: "Required artifact has not been submitted",
				})
				continue
			}
			// A rejected artifact is an explicit "not acceptable", so it blocks
			// exactly like a missing one. Submitted and approved both let the
			// delivery enter review; actor-reviewed nodes have a stricter
			// post-verdict gate below and cannot leave until required artifacts
			// are approved.
			if artifact.ReviewStatus == "rejected" {
				reasons = append(reasons, workflowdomain.WaitingReason{
					Code: "required_artifact_rejected", Field: requirement.Key,
					Message: "Required artifact was rejected in review",
				})
			}
		}
	}
	if len(reasons) > 0 {
		return false, reasons, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, nil
	}
	submissions, err := q.ListWorkflowSubmissions(ctx, db.ListWorkflowSubmissionsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, nil, db.WorkflowNodeSubmission{}, db.WorkflowNodeVerdict{}, err
	}
	var submission db.WorkflowNodeSubmission
	validSubmissionsByIssue := map[pgtype.UUID]struct{}{}
	for _, candidate := range submissions {
		if candidate.Status != "valid" {
			continue
		}
		if !submission.ID.Valid {
			submission = candidate
		}
		if candidate.SourceIssueID.Valid {
			validSubmissionsByIssue[candidate.SourceIssueID] = struct{}{}
		}
	}
	submissionPolicy := workflowdomain.SubmissionPolicy(nodeDefinition)
	if submissionPolicy == "per_required_task" || submissionPolicy == "fan_in" {
		missingTaskSubmissions := make([]workflowdomain.WaitingReason, 0)
		requiredTaskCount := 0
		for _, task := range tasks {
			if !task.Required {
				continue
			}
			requiredTaskCount++
			if !task.IssueID.Valid {
				missingTaskSubmissions = append(
					missingTaskSubmissions,
					workflowdomain.WaitingReason{
						Code: "task_submission_required", Field: task.TaskKey,
						Message: "Required task needs a valid structured submission",
					},
				)
				continue
			}
			if _, exists := validSubmissionsByIssue[task.IssueID]; !exists {
				missingTaskSubmissions = append(
					missingTaskSubmissions,
					workflowdomain.WaitingReason{
						Code: "task_submission_required", Field: task.TaskKey,
						Message: "Required task needs a valid structured submission",
					},
				)
			}
		}
		if len(missingTaskSubmissions) > 0 {
			return false, missingTaskSubmissions, submission, db.WorkflowNodeVerdict{}, nil
		}
		if requiredTaskCount == 0 && !submission.ID.Valid {
			return false, []workflowdomain.WaitingReason{{
				Code:    "valid_submission_required",
				Message: "At least one valid structured submission is required",
			}}, submission, db.WorkflowNodeVerdict{}, nil
		}
	}
	reviewRequired := workflowdomain.RequiresReview(nodeDefinition)
	submissionRequired := nodeDefinition.Completion.SubmissionRequired ||
		submissionPolicy != "none"
	if !submission.ID.Valid && nodeDefinition.SubmissionSchema == nil &&
		(submissionRequired || reviewRequired) {
		revision, err := q.GetNextWorkflowSubmissionRevision(ctx, db.GetNextWorkflowSubmissionRevisionParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return false, nil, submission, db.WorkflowNodeVerdict{}, err
		}
		basis, _ := json.Marshal(map[string]any{"kind": "all_required_issues_done"})
		submission, err = q.CreateWorkflowSubmission(ctx, db.CreateWorkflowSubmissionParams{
			WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID, WorkflowNodeInstanceID: node.ID,
			Revision: revision, Status: "valid", Payload: basis, Summary: "All required issues are done",
			Evidence: []byte("[]"), SubmittedByType: "system", SchemaVersion: 1,
		})
		if err != nil {
			return false, nil, submission, db.WorkflowNodeVerdict{}, err
		}
		if err := q.SetWorkflowNodeLatestSubmission(ctx, db.SetWorkflowNodeLatestSubmissionParams{
			LatestSubmissionID: submission.ID, ID: node.ID, WorkspaceID: workspaceID,
		}); err != nil {
			return false, nil, submission, db.WorkflowNodeVerdict{}, err
		}
	}
	if !submission.ID.Valid && submissionRequired {
		return false, []workflowdomain.WaitingReason{{
			Code: "valid_submission_required", Message: "A valid structured submission is required",
		}}, submission, db.WorkflowNodeVerdict{}, nil
	}
	// Declared output fields are a delivery the node owes, and only a real
	// submission carries them: the synthesised one above records that the
	// issues are done and nothing else. Completing on it would leave the
	// variable pool empty, and a downstream gateway would fail closed to its
	// else branch with nobody having made the decision — the silent routing
	// that declared outputs exist to prevent. Held here rather than refused
	// at submit time so the work itself is never blocked, only its handover.
	if missing := workflowdomain.MissingRequiredOutputs(
		nodeDefinition.Outputs, workflowSubmissionOutputs(submission),
	); len(missing) > 0 {
		reasons := make([]workflowdomain.WaitingReason, 0, len(missing))
		for _, key := range missing {
			reasons = append(reasons, workflowdomain.WaitingReason{
				Code:    "output_field_required",
				Field:   key,
				Message: "The activity owes the output field " + key,
			})
		}
		return false, reasons, submission, db.WorkflowNodeVerdict{}, nil
	}
	if !reviewRequired {
		return h.evaluateWorkflowManualCompletion(
			ctx, q, workspaceID, node, nodeDefinition, submission,
			db.WorkflowNodeVerdict{}, includeManualCompletion,
		)
	}
	if workflowdomain.ReviewerAcceptsActor(nodeDefinition) {
		reviewerType, reviewerID, resolved, err := workflowReviewerAssignment(
			ctx, q, node, nodeDefinition,
		)
		if err != nil {
			return false, nil, submission, db.WorkflowNodeVerdict{}, err
		}
		if !resolved {
			return false, []workflowdomain.WaitingReason{{
				Code: "review_required", Message: "Waiting for a reviewer assignment",
			}}, submission, db.WorkflowNodeVerdict{}, nil
		}
		verdicts, err := q.ListWorkflowVerdicts(ctx, db.ListWorkflowVerdictsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return false, nil, submission, db.WorkflowNodeVerdict{}, err
		}
		var verdict db.WorkflowNodeVerdict
		for _, candidate := range verdicts {
			matches, matchErr := workflowActorMatchesReviewer(
				ctx, q, reviewerType, reviewerID,
				candidate.EvaluatorType, candidate.EvaluatorID,
			)
			if matchErr != nil {
				return false, nil, submission, db.WorkflowNodeVerdict{}, matchErr
			}
			if matches && !workflowVerdictIsManualCompletion(candidate) {
				verdict = candidate
				break
			}
		}
		if !verdict.ID.Valid {
			return false, []workflowdomain.WaitingReason{{
				Code: "review_required", Message: "Waiting for the reviewer",
			}}, submission, db.WorkflowNodeVerdict{}, nil
		}
		if !workflowVerdictSatisfies(verdict.Result) {
			return false, []workflowdomain.WaitingReason{
				workflowVerdictWaitingReason(verdict),
			}, submission, verdict, nil
		}
		artifactReasons, err := workflowArtifactApprovalReasons(
			ctx, q, workspaceID, node, nodeDefinition,
		)
		if err != nil {
			return false, nil, submission, verdict, err
		}
		if len(artifactReasons) > 0 {
			return false, artifactReasons, submission, verdict, nil
		}
		return h.evaluateWorkflowManualCompletion(
			ctx, q, workspaceID, node, nodeDefinition, submission, verdict,
			includeManualCompletion,
		)
	}
	if nodeDefinition.Reviewer.Kind == "api" {
		result, reason := h.evaluateAPIWorkflowVerdict(ctx, nodeDefinition.Reviewer.APIURL)
		if !workflowVerdictSatisfies(result) {
			return false, []workflowdomain.WaitingReason{{
				Code: "api_verdict_not_passed", Message: reason,
			}}, submission, db.WorkflowNodeVerdict{}, nil
		}
		return h.evaluateWorkflowManualCompletion(
			ctx, q, workspaceID, node, nodeDefinition, submission,
			db.WorkflowNodeVerdict{}, includeManualCompletion,
		)
	}
	verdictResult, verdictReason, verdictBasis, err :=
		h.evaluateDeterministicWorkflowVerdict(
			ctx,
			q,
			workspaceID,
			instance,
			nodeDefinition.Reviewer.Condition,
		)
	if err != nil {
		return false, nil, submission, db.WorkflowNodeVerdict{}, err
	}
	verdicts, err := q.ListWorkflowVerdicts(ctx, db.ListWorkflowVerdictsParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, nil, submission, db.WorkflowNodeVerdict{}, err
	}
	for _, candidate := range verdicts {
		if candidate.EvaluatorType != "deterministic" {
			continue
		}
		var basis map[string]any
		_ = json.Unmarshal(candidate.Basis, &basis)
		sameInput := !submission.ID.Valid ||
			basis["submission_id"] == uuidToString(submission.ID)
		sameEvaluation := true
		if matched, exists := verdictBasis["condition_matched"]; exists {
			sameEvaluation = basis["condition_matched"] == matched
		}
		if sameInput && sameEvaluation {
			if !workflowVerdictSatisfies(candidate.Result) {
				return false, []workflowdomain.WaitingReason{
					workflowVerdictWaitingReason(candidate),
				}, submission, candidate, nil
			}
			return h.evaluateWorkflowManualCompletion(
				ctx, q, workspaceID, node, nodeDefinition, submission, candidate,
				includeManualCompletion,
			)
		}
	}
	revision, err := q.GetNextWorkflowVerdictRevision(ctx, db.GetNextWorkflowVerdictRevisionParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, nil, submission, db.WorkflowNodeVerdict{}, err
	}
	basisMap := map[string]any{"rule": "configured_completion_predicates"}
	if submission.ID.Valid {
		basisMap["submission_id"] = uuidToString(submission.ID)
	}
	for key, value := range verdictBasis {
		basisMap[key] = value
	}
	basis, _ := json.Marshal(basisMap)
	definitionSnapshot, _ := json.Marshal(nodeDefinition.Reviewer)
	verdict, err := q.CreateWorkflowVerdict(ctx, db.CreateWorkflowVerdictParams{
		WorkspaceID: workspaceID, WorkflowInstanceID: instance.ID, WorkflowNodeInstanceID: node.ID,
		Revision: revision, Result: verdictResult, Reason: verdictReason,
		Evidence: []byte("[]"), Basis: basis, EvaluatorType: "deterministic",
		DefinitionSnapshot: definitionSnapshot,
	})
	if err != nil {
		return false, nil, submission, verdict, err
	}
	if err := q.SetWorkflowNodeLatestVerdict(ctx, db.SetWorkflowNodeLatestVerdictParams{
		LatestVerdictID: verdict.ID, ID: node.ID, WorkspaceID: workspaceID,
	}); err != nil {
		return false, nil, submission, verdict, err
	}
	if !workflowVerdictSatisfies(verdict.Result) {
		return false, []workflowdomain.WaitingReason{
			workflowVerdictWaitingReason(verdict),
		}, submission, verdict, nil
	}
	return h.evaluateWorkflowManualCompletion(
		ctx, q, workspaceID, node, nodeDefinition, submission, verdict,
		includeManualCompletion,
	)
}

func (h *Handler) evaluateWorkflowManualCompletion(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
	submission db.WorkflowNodeSubmission,
	currentVerdict db.WorkflowNodeVerdict,
	includeManualCompletion bool,
) (bool, []workflowdomain.WaitingReason, db.WorkflowNodeSubmission, db.WorkflowNodeVerdict, error) {
	if !includeManualCompletion ||
		!workflowdomain.RequiresManualCompletion(nodeDefinition) {
		return true, nil, submission, currentVerdict, nil
	}
	verdicts, err := q.ListWorkflowVerdicts(
		ctx,
		db.ListWorkflowVerdictsParams{
			WorkflowNodeInstanceID: node.ID,
			WorkspaceID:            workspaceID,
		},
	)
	if err != nil {
		return false, nil, submission, currentVerdict, err
	}
	for _, verdict := range verdicts {
		if verdict.EvaluatorType == "member" && verdict.Result == "pass" &&
			workflowVerdictIsManualCompletion(verdict) {
			return true, nil, submission, verdict, nil
		}
	}
	return false, []workflowdomain.WaitingReason{{
		Code:    "manual_completion_required",
		Message: "Waiting for the node owner to complete this activity",
	}}, submission, currentVerdict, nil
}

func workflowVerdictIsManualCompletion(verdict db.WorkflowNodeVerdict) bool {
	var basis map[string]any
	_ = json.Unmarshal(verdict.Basis, &basis)
	return basis["kind"] == "manual_completion"
}

// workflowWaitingReasonsAwaitReview reports whether the node is stopped on its
// reviewer rather than on its own work. The distinction is what the canvas
// renders: the executor has delivered, and the flow is waiting on someone else.
//
// Callers pair this with ReviewerAcceptsActor, because "someone else" has to
// be a member or agent. An api or auto reviewer already answered — a rule that
// evaluated to no is a node that is waiting, not one under review.
func workflowWaitingReasonsAwaitReview(
	reasons []workflowdomain.WaitingReason,
) bool {
	for _, reason := range reasons {
		if reason.Code == "review_required" || reason.Code == "verdict_not_passed" {
			return true
		}
	}
	return false
}

func workflowWaitingReasonsNeedReviewer(
	reasons []workflowdomain.WaitingReason,
) bool {
	for _, reason := range reasons {
		if reason.Code == "review_required" {
			return true
		}
	}
	return false
}

func workflowWaitingReasonsBlockNode(
	reasons []workflowdomain.WaitingReason,
) bool {
	for _, reason := range reasons {
		if reason.Code == "required_issue_cancelled" ||
			reason.Code == "verdict_blocked" ||
			// An agent that failed is not a step still in progress. Left as
			// "waiting" the node reads like every other node making its way,
			// and the run sits on work that already stopped until somebody
			// happens to look.
			reason.Code == "direct_execution_failed" {
			return true
		}
	}
	return false
}

func workflowVerdictWaitingReason(
	verdict db.WorkflowNodeVerdict,
) workflowdomain.WaitingReason {
	code := "verdict_not_passed"
	if verdict.Result == "blocked" {
		code = "verdict_blocked"
	}
	return workflowdomain.WaitingReason{Code: code, Message: verdict.Reason}
}

func hasWorkflowJSONValue(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

func (h *Handler) evaluateDeterministicWorkflowVerdict(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	instance db.WorkflowInstance,
	condition json.RawMessage,
) (string, string, map[string]any, error) {
	result := "pass"
	reason := "All deterministic completion conditions passed"
	basis := map[string]any{}
	if !hasWorkflowJSONValue(condition) {
		return result, reason, basis, nil
	}
	nodeRows, err := q.ListWorkflowNodeInstances(
		ctx,
		db.ListWorkflowNodeInstancesParams{
			WorkflowInstanceID: instance.ID,
			WorkspaceID:        workspaceID,
		},
	)
	if err != nil {
		return "", "", nil, err
	}
	resolver, err := workflowConditionResolver(
		ctx,
		q,
		workspaceID,
		instance,
		latestWorkflowNodesByKey(nodeRows),
	)
	if err != nil {
		return "", "", nil, err
	}
	matches, err := workflowdomain.EvaluateCondition(condition, resolver)
	if err != nil {
		return "", "", nil, err
	}
	basis["condition"] = json.RawMessage(condition)
	basis["condition_matched"] = matches
	if !matches {
		result = "fail"
		reason = "The deterministic verdict condition did not match"
	}
	return result, reason, basis, nil
}

// workflowVerdictSatisfies reports whether a verdict releases the node. A
// reviewer either passed the output or did not; the old required_result knob
// let a template accept a failing verdict, which no template ever did and
// which made "reviewed" mean two different things.
func workflowVerdictSatisfies(result string) bool {
	return result == "pass"
}

// workflowSubmissionOutputs reads the delivered field values off a submission.
// An absent submission yields an empty map, so a node with nothing delivered
// reads as "owes everything" rather than panicking.
func workflowSubmissionOutputs(submission db.WorkflowNodeSubmission) map[string]any {
	if !submission.ID.Valid {
		return map[string]any{}
	}
	return decodeWorkflowObject(submission.Payload)
}

func decodeWorkflowObject(raw []byte) map[string]any {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return map[string]any{}
	}
	return value
}

// workflowAPIVerdictTimeout bounds the external call. A check that has not
// answered by now is indistinguishable from one that never will, and a node
// waiting on it blocks the run.
const workflowAPIVerdictTimeout = 10 * time.Second

// evaluateAPIWorkflowVerdict asks an external endpoint whether the node passes.
//
// Anything other than a clean "pass" is treated as blocked, never as approval:
// a check that times out, errors, or answers in a shape we do not recognise has
// not approved anything, and defaulting the other way would let an unreachable
// endpoint wave work through.
func (h *Handler) evaluateAPIWorkflowVerdict(
	ctx context.Context,
	apiURL string,
) (string, string) {
	requestCtx, cancel := context.WithTimeout(ctx, workflowAPIVerdictTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "blocked", "Check endpoint address is invalid"
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "blocked", "Check endpoint is unreachable"
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "blocked", fmt.Sprintf("Check endpoint returned %d", response.StatusCode)
	}
	var payload struct {
		Result string `json:"result"`
		Reason string `json:"reason"`
	}
	// Bound the body: an endpoint streaming megabytes is a fault, not a verdict.
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return "blocked", "Check endpoint returned an unreadable body"
	}
	switch payload.Result {
	case "pass", "fail", "blocked":
	default:
		return "blocked", "Check endpoint returned an unknown result"
	}
	reason := strings.TrimSpace(payload.Reason)
	if reason == "" {
		reason = "Check result: " + payload.Result
	}
	return payload.Result, reason
}
