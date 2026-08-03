package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// WorkflowTaskContext is the workflow protocol handed to an agent working a
// node issue. It is pushed with the claim rather than left for the agent to
// discover, because discovery costs a CLI round-trip the agent has to remember
// to make — and an agent running an older CLI cannot make it at all. What the
// node owes and what its predecessors concluded decide whether the run
// advances, so neither may depend on the agent's initiative.
//
// Artifact bodies are deliberately absent: the index says what exists and what
// it is called, and `multica workflow artifact get <id>` fetches a body when
// one is actually needed. Pushing every upstream document would make each node
// pay for prose it may never read.
type WorkflowTaskContext struct {
	InstanceID string `json:"instance_id"`
	Phase      string `json:"phase,omitempty"`
	// NodeInstanceID addresses the live attempt. Submissions are scoped to it,
	// so an agent that cached an earlier attempt's id would write to work that
	// has since been redone.
	NodeInstanceID   string                    `json:"node_instance_id"`
	NodeKey          string                    `json:"node_key"`
	NodeName         string                    `json:"node_name,omitempty"`
	RunTitle         string                    `json:"run_title,omitempty"`
	Instructions     string                    `json:"instructions,omitempty"`
	DirectExecution  bool                      `json:"direct_execution,omitempty"`
	HostIssue        string                    `json:"host_issue,omitempty"`
	NodeIssues       []string                  `json:"node_issues,omitempty"`
	HandoffRequired  bool                      `json:"handoff_required,omitempty"`
	Artifacts        []WorkflowArtifactDuty    `json:"artifacts,omitempty"`
	Upstream         []WorkflowUpstreamContext `json:"upstream,omitempty"`
	Rework           *WorkflowReworkContext    `json:"rework,omitempty"`
	Choice           *WorkflowChoiceDuty       `json:"choice,omitempty"`
	ReviewSubmission *WorkflowReviewSubmission `json:"review_submission,omitempty"`
}

// WorkflowReviewSubmission is the exact worker handoff the Critic judges.
// The server still points the Critic at issue and artifact bodies for detail;
// this compact record identifies the revision and preserves its conclusion.
type WorkflowReviewSubmission struct {
	ID           string          `json:"id"`
	Summary      string          `json:"summary,omitempty"`
	WorkerOutput string          `json:"worker_output,omitempty"`
	Evidence     json.RawMessage `json:"evidence,omitempty"`
}

// WorkflowChoiceDuty is the routing decision this node owes a downstream
// gateway, derived from the graph rather than declared on the node. Nil when
// nothing branches on this node.
type WorkflowChoiceDuty struct {
	GatewayName   string                 `json:"gateway_name,omitempty"`
	DefaultTarget string                 `json:"default_target,omitempty"`
	Options       []WorkflowChoiceOption `json:"options,omitempty"`
}

type WorkflowChoiceOption struct {
	Value  string `json:"value"`
	Target string `json:"target,omitempty"`
}

// WorkflowReworkContext explains why a node is running again. Nil on a first
// attempt. Rework continues on the previous attempt's issue, so the prior work
// is already in front of the executor; what the issue cannot say is that this
// is a retry and which judgement sent it back.
type WorkflowReworkContext struct {
	Attempt int    `json:"attempt"`
	Source  string `json:"source,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// WorkflowArtifactDuty is one artifact the node owes, paired with whether it
// has been delivered. The key is what `multica workflow submit --artifact`
// accepts; the server rejects any key the node did not declare, so it has to
// travel with the task instead of being guessed.
type WorkflowArtifactDuty struct {
	ID           string `json:"id,omitempty"`
	Key          string `json:"key"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Kind         string `json:"kind"`
	Required     bool   `json:"required"`
	Delivered    bool   `json:"delivered"`
	ReviewStatus string `json:"review_status,omitempty"`
}

// WorkflowUpstreamContext carries one direct predecessor's conclusion.
type WorkflowUpstreamContext struct {
	NodeKey   string                     `json:"node_key"`
	Name      string                     `json:"name,omitempty"`
	Status    string                     `json:"status,omitempty"`
	Summary   string                     `json:"summary,omitempty"`
	Artifacts []WorkflowUpstreamArtifact `json:"artifacts,omitempty"`
}

// WorkflowUpstreamArtifact is the index form: enough to decide whether to read
// the body, without carrying it.
type WorkflowUpstreamArtifact struct {
	ID          string `json:"id"`
	ArtifactKey string `json:"artifact_key"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
}

// workflowIssueCoordinates is the shape stamped onto a node child issue's
// metadata when the task was materialized.
type workflowIssueCoordinates struct {
	InstanceID string `json:"instance_id"`
	NodeKey    string `json:"node_key"`
	HostIssue  string `json:"host_issue"`
}

// readWorkflowIssueCoordinates pulls the workflow coordinates off an issue.
// A missing or malformed block means "not a workflow node issue", which is the
// common case — every ordinary issue takes this path — so it returns cleanly
// rather than erroring.
func readWorkflowIssueCoordinates(metadata []byte) (workflowIssueCoordinates, bool) {
	if len(metadata) == 0 {
		return workflowIssueCoordinates{}, false
	}
	var envelope struct {
		Workflow workflowIssueCoordinates `json:"workflow"`
	}
	if err := json.Unmarshal(metadata, &envelope); err != nil {
		return workflowIssueCoordinates{}, false
	}
	coordinates := envelope.Workflow
	if strings.TrimSpace(coordinates.InstanceID) == "" ||
		strings.TrimSpace(coordinates.NodeKey) == "" {
		return workflowIssueCoordinates{}, false
	}
	return coordinates, true
}

// workflowTaskContext builds the protocol block for a claimed task. It returns
// nil for any issue that is not a live workflow node issue, and for every
// failure along the way: a claim must not fail because workflow context could
// not be assembled, since the agent can still do the work and the run is only
// blocked at completion time, where the waiting reasons say exactly what is
// missing.
func (h *Handler) workflowTaskContext(
	ctx context.Context,
	issue db.Issue,
) *WorkflowTaskContext {
	coordinates, ok := readWorkflowIssueCoordinates(issue.Metadata)
	if !ok {
		return nil
	}
	instanceID, err := util.ParseUUID(coordinates.InstanceID)
	if err != nil {
		return nil
	}
	instance, err := h.Queries.GetWorkflowInstanceInWorkspace(
		ctx,
		db.GetWorkflowInstanceInWorkspaceParams{
			ID: instanceID, WorkspaceID: issue.WorkspaceID,
		},
	)
	if err != nil {
		return nil
	}
	nodes, err := h.Queries.ListWorkflowNodeInstances(
		ctx,
		db.ListWorkflowNodeInstancesParams{
			WorkflowInstanceID: instance.ID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil || len(nodes) == 0 {
		return nil
	}
	// Several attempts of the same node can exist after a rework. The live one
	// is the highest attempt; writing to an earlier one would attach work to an
	// attempt the run has already moved past.
	live := map[string]db.WorkflowNodeInstance{}
	for _, candidate := range nodes {
		if existing, seen := live[candidate.NodeKey]; !seen ||
			candidate.Attempt > existing.Attempt {
			live[candidate.NodeKey] = candidate
		}
	}
	node, exists := live[coordinates.NodeKey]
	if !exists {
		return nil
	}
	var nodeDefinition workflowdomain.NodeDefinition
	if err := json.Unmarshal(node.DefinitionSnapshot, &nodeDefinition); err != nil {
		return nil
	}
	hostIssue := coordinates.HostIssue
	if strings.TrimSpace(hostIssue) == "" && instance.HostIssueID.Valid {
		if host, hostErr := h.Queries.GetIssueInWorkspace(
			ctx,
			db.GetIssueInWorkspaceParams{
				ID: instance.HostIssueID, WorkspaceID: instance.WorkspaceID,
			},
		); hostErr == nil {
			hostIssue = h.getIssuePrefix(ctx, instance.WorkspaceID) + "-" +
				strconv.Itoa(int(host.Number))
		}
	}
	result := &WorkflowTaskContext{
		InstanceID:      uuidToString(instance.ID),
		Phase:           service.WorkflowNodeTaskPhaseWorker,
		NodeInstanceID:  uuidToString(node.ID),
		NodeKey:         node.NodeKey,
		NodeName:        node.NameSnapshot,
		RunTitle:        instance.Title,
		Instructions:    strings.TrimSpace(nodeDefinition.Description),
		HostIssue:       hostIssue,
		HandoffRequired: nodeDefinition.Completion.HandoffRequired,
	}
	result.Artifacts = h.workflowArtifactDuties(ctx, instance.WorkspaceID, node, nodeDefinition)
	result.NodeIssues = h.workflowNodeIssueIdentifiers(ctx, instance.WorkspaceID, node)
	result.Upstream = h.workflowUpstreamContext(ctx, instance, node, live)
	result.Rework = h.workflowReworkContext(ctx, instance, node)
	result.Choice = h.workflowChoiceDuty(ctx, instance, node)
	result.ReviewSubmission = h.workflowReviewSubmission(ctx, instance.WorkspaceID, node)
	return result
}

// workflowTaskContextForDirectTask adapts the same live workflow protocol used
// by Issue-backed tasks to an issue-less agent execution.
func (h *Handler) workflowTaskContextForDirectTask(
	ctx context.Context,
	task db.AgentTaskQueue,
) *WorkflowTaskContext {
	direct, ok := service.ParseWorkflowNodeTaskContext(task)
	if !ok {
		return nil
	}
	workspaceID, err := util.ParseUUID(direct.WorkspaceID)
	if err != nil {
		return nil
	}
	metadata, err := json.Marshal(map[string]any{
		"workflow": workflowIssueCoordinates{
			InstanceID: direct.InstanceID, NodeKey: "",
		},
	})
	if err != nil {
		return nil
	}
	nodeID, err := util.ParseUUID(direct.NodeInstanceID)
	if err != nil {
		return nil
	}
	node, err := h.Queries.GetWorkflowNodeInstanceInWorkspace(
		ctx, db.GetWorkflowNodeInstanceInWorkspaceParams{ID: nodeID, WorkspaceID: workspaceID},
	)
	if err != nil {
		return nil
	}
	var envelope struct {
		Workflow workflowIssueCoordinates `json:"workflow"`
	}
	if json.Unmarshal(metadata, &envelope) != nil {
		return nil
	}
	envelope.Workflow.NodeKey = node.NodeKey
	metadata, _ = json.Marshal(envelope)
	result := h.workflowTaskContext(ctx, db.Issue{WorkspaceID: workspaceID, Metadata: metadata})
	if result == nil {
		return nil
	}
	result.DirectExecution = true
	result.Phase = direct.Phase
	if strings.TrimSpace(direct.Prompt) != "" {
		result.Instructions = strings.TrimSpace(direct.Prompt)
	}
	if strings.TrimSpace(direct.RunTitle) != "" {
		result.RunTitle = strings.TrimSpace(direct.RunTitle)
	}
	return result
}

func (h *Handler) workflowNodeIssueIdentifiers(
	ctx context.Context,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
) []string {
	tasks, err := h.Queries.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return nil
	}
	prefix := h.getIssuePrefix(ctx, workspaceID)
	issues := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if !task.IssueID.Valid {
			continue
		}
		issue, issueErr := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
			ID: task.IssueID, WorkspaceID: workspaceID,
		})
		if issueErr == nil {
			issues = append(issues, prefix+"-"+strconv.Itoa(int(issue.Number)))
		}
	}
	return issues
}

func (h *Handler) workflowReviewSubmission(
	ctx context.Context,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
) *WorkflowReviewSubmission {
	if !node.LatestSubmissionID.Valid {
		return nil
	}
	submission, err := h.Queries.GetWorkflowSubmissionInWorkspace(
		ctx,
		db.GetWorkflowSubmissionInWorkspaceParams{
			ID: node.LatestSubmissionID, WorkspaceID: workspaceID,
		},
	)
	if err != nil || submission.Status != "valid" {
		return nil
	}
	return &WorkflowReviewSubmission{
		ID: uuidToString(submission.ID), Summary: submission.Summary,
		WorkerOutput: h.workflowDirectWorkerOutput(ctx, workspaceID, node),
		Evidence:     json.RawMessage(submission.Evidence),
	}
}

func (h *Handler) workflowDirectWorkerOutput(
	ctx context.Context,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
) string {
	tasks, err := h.Queries.ListWorkflowNodeTasks(ctx, db.ListWorkflowNodeTasksParams{
		WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return ""
	}
	for _, task := range tasks {
		if task.Source != "execution" {
			continue
		}
		agentTask, taskErr := h.Queries.GetLatestAgentTaskForWorkflowNodeTask(ctx, task.ID)
		if taskErr != nil || agentTask.Status != "completed" {
			continue
		}
		var result struct {
			Output string `json:"output"`
		}
		if json.Unmarshal(agentTask.Result, &result) != nil {
			continue
		}
		output := []rune(strings.TrimSpace(result.Output))
		const maxOutputRunes = 12000
		if len(output) > maxOutputRunes {
			output = append(output[:maxOutputRunes], []rune("\n\n[truncated]")...)
		}
		return string(output)
	}
	return ""
}

// workflowChoiceDuty reports the branch decision this node owes, derived from
// the published graph. Nil when no gateway reads this node's choice, so a node
// is never told to make a decision nothing consumes.
func (h *Handler) workflowChoiceDuty(
	ctx context.Context,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
) *WorkflowChoiceDuty {
	version, err := h.Queries.GetWorkflowVersionInWorkspace(
		ctx,
		db.GetWorkflowVersionInWorkspaceParams{
			ID: instance.WorkflowVersionID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		return nil
	}
	definition, err := workflowdomain.ParseDefinition(version.Definition)
	if err != nil {
		return nil
	}
	duty, decides := workflowdomain.ChoiceBranchesForNode(definition, node.NodeKey)
	if !decides || len(duty.Options) == 0 {
		return nil
	}
	options := make([]WorkflowChoiceOption, 0, len(duty.Options))
	for _, option := range duty.Options {
		options = append(options, WorkflowChoiceOption{
			Value: option.Value, Target: option.Target,
		})
	}
	return &WorkflowChoiceDuty{
		GatewayName:   duty.GatewayName,
		DefaultTarget: duty.DefaultTarget,
		Options:       options,
	}
}

// workflowReworkContext explains a re-attempt. It returns nil for a first
// attempt, and for a later attempt whose originating judgement can no longer be
// found — a missing reason is worth rendering as "unstated", but a fabricated
// one is not.
func (h *Handler) workflowReworkContext(
	ctx context.Context,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
) *WorkflowReworkContext {
	if node.Attempt < 2 {
		return nil
	}
	rework := &WorkflowReworkContext{Attempt: int(node.Attempt)}
	event, err := h.Queries.GetLatestWorkflowReworkEvent(
		ctx,
		db.GetLatestWorkflowReworkEventParams{
			WorkspaceID:        instance.WorkspaceID,
			WorkflowInstanceID: instance.ID,
			NodeKey:            node.NodeKey,
		},
	)
	if err != nil {
		return rework
	}
	var payload struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(event.Payload, &payload) == nil {
		rework.Reason = payload.Reason
	}
	switch event.EventType {
	case "acceptance.rejected":
		rework.Source = "acceptance"
	case "node.rollback":
		if payload.Action == "critic_rework" {
			rework.Source = "critic"
		} else {
			rework.Source = "manual_rollback"
		}
	}
	return rework
}

// workflowArtifactDuties pairs each declared artifact with what the node has
// actually delivered, so the agent can tell "still owed" from "already there"
// without a second call — the distinction that decides whether it should write
// a document at all on a rerun.
func (h *Handler) workflowArtifactDuties(
	ctx context.Context,
	workspaceID pgtype.UUID,
	node db.WorkflowNodeInstance,
	nodeDefinition workflowdomain.NodeDefinition,
) []WorkflowArtifactDuty {
	if len(nodeDefinition.Artifacts) == 0 {
		return nil
	}
	delivered := map[string]db.WorkflowArtifact{}
	if artifacts, err := h.Queries.ListWorkflowNodeArtifacts(
		ctx,
		db.ListWorkflowNodeArtifactsParams{
			WorkflowNodeInstanceID: node.ID, WorkspaceID: workspaceID,
		},
	); err == nil {
		for _, artifact := range artifacts {
			delivered[artifact.ArtifactKey] = artifact
		}
	}
	duties := make([]WorkflowArtifactDuty, 0, len(nodeDefinition.Artifacts))
	for _, requirement := range nodeDefinition.Artifacts {
		duty := WorkflowArtifactDuty{
			Key:         requirement.Key,
			Name:        requirement.Name,
			Description: requirement.Description,
			Kind:        workflowdomain.ArtifactKind(requirement),
			Required:    requirement.Required,
		}
		if artifact, exists := delivered[requirement.Key]; exists {
			duty.ID = uuidToString(artifact.ID)
			duty.Delivered = true
			duty.ReviewStatus = artifact.ReviewStatus
		}
		duties = append(duties, duty)
	}
	return duties
}

// workflowUpstreamContext returns each direct predecessor's conclusion.
//
// Direct predecessors only, matching GET /api/workflow-node-instances/{id}/upstream:
// that is what the graph says this node depends on, and walking further back
// would pull in parallel branches this node never waited for.
func (h *Handler) workflowUpstreamContext(
	ctx context.Context,
	instance db.WorkflowInstance,
	node db.WorkflowNodeInstance,
	live map[string]db.WorkflowNodeInstance,
) []WorkflowUpstreamContext {
	version, err := h.Queries.GetWorkflowVersionInWorkspace(
		ctx,
		db.GetWorkflowVersionInWorkspaceParams{
			ID: instance.WorkflowVersionID, WorkspaceID: instance.WorkspaceID,
		},
	)
	if err != nil {
		return nil
	}
	definition, err := workflowdomain.ParseDefinition(version.Definition)
	if err != nil {
		return nil
	}
	plan, err := workflowdomain.BuildGraphPlan(definition)
	if err != nil {
		return nil
	}
	predecessors := map[string]struct{}{}
	for _, edge := range plan.Incoming[node.NodeKey] {
		predecessors[edge.From] = struct{}{}
	}
	if len(predecessors) == 0 {
		return nil
	}
	upstream := make([]WorkflowUpstreamContext, 0, len(predecessors))
	// Ordered by the graph plan so the agent reads predecessors in execution
	// order rather than map order, which would shuffle between claims.
	for _, definitionNode := range plan.Ordered {
		if _, wanted := predecessors[definitionNode.Key]; !wanted {
			continue
		}
		candidate, exists := live[definitionNode.Key]
		if !exists {
			continue
		}
		entry := WorkflowUpstreamContext{
			NodeKey: candidate.NodeKey,
			Name:    candidate.NameSnapshot,
			Status:  candidate.Status,
		}
		if candidate.LatestSubmissionID.Valid {
			if submission, err := h.Queries.GetWorkflowSubmissionInWorkspace(
				ctx,
				db.GetWorkflowSubmissionInWorkspaceParams{
					ID: candidate.LatestSubmissionID, WorkspaceID: instance.WorkspaceID,
				},
			); err == nil {
				entry.Summary = submission.Summary
			}
		}
		if artifacts, err := h.Queries.ListWorkflowNodeArtifacts(
			ctx,
			db.ListWorkflowNodeArtifactsParams{
				WorkflowNodeInstanceID: candidate.ID, WorkspaceID: instance.WorkspaceID,
			},
		); err == nil {
			for _, artifact := range artifacts {
				entry.Artifacts = append(entry.Artifacts, WorkflowUpstreamArtifact{
					ID:          uuidToString(artifact.ID),
					ArtifactKey: artifact.ArtifactKey,
					Kind:        artifact.Kind,
					Name:        artifact.Name,
				})
			}
		}
		upstream = append(upstream, entry)
	}
	return upstream
}
