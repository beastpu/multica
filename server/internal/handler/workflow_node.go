package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	// The idempotency key above catches a caller repeating itself verbatim. It
	// cannot catch a caller repeating itself in different words, because the key
	// is derived from what was sent and the row is written from what the server
	// made of it: `true`, `"true"` and `"1"` are three keys and one stored value.
	// A node then carried the same conclusion twice, both marked valid, with
	// nothing on screen to say which one a reviewer was judging.
	//
	// Asked against the normalised form, this is the question a reader asks
	// looking at the two cards. A conclusion that actually changed still lands:
	// only a row that would be written identically is folded back.
	trimmedSummary := strings.TrimSpace(req.Summary)
	if existing, findErr := qtx.FindEquivalentWorkflowSubmission(
		r.Context(),
		db.FindEquivalentWorkflowSubmissionParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: node.WorkspaceID,
			Status: status, Summary: trimmedSummary, Payload: payload,
			SubmittedByType: actorType, SubmittedByID: actorID,
			SourceIssueID: sourceIssueID,
		},
	); findErr == nil && existing.ID.Valid {
		tx.Rollback(r.Context())
		h.Metrics.RecordWorkflowOperation("duplicate", "prevented")
		writeJSON(w, http.StatusOK, map[string]any{
			"submission": workflowSubmissionToResponse(existing), "validation_errors": reasons,
		})
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
		Revision: revision, Status: status, Payload: payload, Summary: trimmedSummary,
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

// workflowAPIVerdictTimeout bounds the external call. A check that has not
// answered by now is indistinguishable from one that never will, and a node
// waiting on it blocks the run.
const workflowAPIVerdictTimeout = 10 * time.Second
