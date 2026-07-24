package handler

import (
	"encoding/json"
	"net/http"

	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GetWorkflowInstanceDiagnostics exposes the durable facts an administrator
// needs to explain a stalled run. It is intentionally read-only: repair
// remains behind the existing reconcile and task retry actions.
func (h *Handler) GetWorkflowInstanceDiagnostics(
	w http.ResponseWriter,
	r *http.Request,
) {
	instance, ok := h.loadWorkflowInstance(w, r)
	if !ok {
		return
	}
	if _, roleOK := h.requireWorkspaceRole(
		w,
		r,
		uuidToString(instance.WorkspaceID),
		"workspace not found",
		"owner",
		"admin",
	); !roleOK {
		return
	}
	version, err := h.Queries.GetWorkflowTemplateVersionInWorkspace(
		r.Context(),
		db.GetWorkflowTemplateVersionInWorkspaceParams{
			ID: instance.TemplateVersionID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow definition")
		return
	}
	definition, err := workflowdomain.ParseDefinition(version.Definition)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow definition")
		return
	}
	nodes, err := h.Queries.ListWorkflowNodeInstances(
		r.Context(),
		db.ListWorkflowNodeInstancesParams{
			WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow nodes")
		return
	}
	nodeDiagnostics := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		tasks, taskErr := h.Queries.ListWorkflowNodeTasks(
			r.Context(),
			db.ListWorkflowNodeTasksParams{
				WorkflowNodeInstanceID: node.ID, WorkspaceID: instance.WorkspaceID,
			},
		)
		if taskErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load workflow tasks")
			return
		}
		resolutions, resolutionErr := h.Queries.ListWorkflowExecutorResolutions(
			r.Context(),
			db.ListWorkflowExecutorResolutionsParams{
				WorkflowNodeInstanceID: node.ID, WorkspaceID: instance.WorkspaceID,
			},
		)
		if resolutionErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load executor resolutions")
			return
		}
		submissions, submissionErr := h.Queries.ListWorkflowSubmissions(
			r.Context(),
			db.ListWorkflowSubmissionsParams{
				WorkflowNodeInstanceID: node.ID, WorkspaceID: instance.WorkspaceID,
			},
		)
		if submissionErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load submissions")
			return
		}
		verdicts, verdictErr := h.Queries.ListWorkflowVerdicts(
			r.Context(),
			db.ListWorkflowVerdictsParams{
				WorkflowNodeInstanceID: node.ID, WorkspaceID: instance.WorkspaceID,
			},
		)
		if verdictErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load verdicts")
			return
		}
		confirmations, confirmationErr := h.Queries.ListWorkflowNodeConfirmations(
			r.Context(),
			db.ListWorkflowNodeConfirmationsParams{
				WorkflowNodeInstanceID: node.ID, WorkspaceID: instance.WorkspaceID,
			},
		)
		if confirmationErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load confirmations")
			return
		}
		taskResponses := make([]workflowTaskResponse, len(tasks))
		for i, task := range tasks {
			taskResponses[i] = workflowTaskToResponse(task)
		}
		resolutionResponses := make([]map[string]any, len(resolutions))
		for i, resolution := range resolutions {
			resolutionResponses[i] = workflowExecutorResolutionToResponse(resolution)
		}
		submissionResponses := make([]workflowSubmissionResponse, len(submissions))
		for i, submission := range submissions {
			submissionResponses[i] = workflowSubmissionToResponse(submission)
		}
		verdictResponses := make([]workflowVerdictResponse, len(verdicts))
		for i, verdict := range verdicts {
			verdictResponses[i] = workflowVerdictToResponse(verdict)
		}
		confirmationResponses := make(
			[]workflowConfirmationResponse,
			len(confirmations),
		)
		for i, confirmation := range confirmations {
			confirmationResponses[i] = workflowConfirmationToResponse(confirmation)
		}
		nodeDiagnostics = append(nodeDiagnostics, map[string]any{
			"node":                 workflowNodeToResponse(node),
			"tasks":                taskResponses,
			"executor_resolutions": resolutionResponses,
			"submissions":          submissionResponses,
			"verdicts":             verdictResponses,
			"confirmations":        confirmationResponses,
		})
	}
	acceptances, err := h.Queries.ListWorkflowAcceptances(
		r.Context(),
		db.ListWorkflowAcceptancesParams{
			WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load acceptances")
		return
	}
	acceptanceResponses := make([]workflowAcceptanceResponse, len(acceptances))
	for i, acceptance := range acceptances {
		acceptanceResponses[i] = workflowAcceptanceToResponse(acceptance)
	}
	events, err := h.Queries.ListWorkflowEvents(
		r.Context(),
		db.ListWorkflowEventsParams{
			WorkflowInstanceID: instance.ID,
			WorkspaceID:        instance.WorkspaceID,
			RowLimit:           100,
		},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow events")
		return
	}
	eventResponses := make([]map[string]any, 0, len(events))
	sweeperEvents := make([]map[string]any, 0)
	for _, event := range events {
		response := map[string]any{
			"id":                        uuidToString(event.ID),
			"workflow_node_instance_id": uuidToPtr(event.WorkflowNodeInstanceID),
			"event_type":                event.EventType,
			"actor_type":                event.ActorType,
			"actor_id":                  uuidToPtr(event.ActorID),
			"idempotency_key":           event.IdempotencyKey,
			"payload":                   json.RawMessage(event.Payload),
			"created_at":                timestampToString(event.CreatedAt),
		}
		eventResponses = append(eventResponses, response)
		if event.EventType == "workflow.sweeper_anomaly" {
			sweeperEvents = append(sweeperEvents, response)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instance":               h.workflowInstanceToRuntimeResponse(r.Context(), instance),
		"instance_revision":      instance.Revision,
		"last_reconciled_at":     timestampToPtr(instance.LastReconciledAt),
		"nodes":                  nodeDiagnostics,
		"acceptances":            acceptanceResponses,
		"allowed_rework_targets": definition.Acceptance.ReworkTargets,
		"recent_sweeper_events":  sweeperEvents,
		"events":                 eventResponses,
	})
}
