package execenv

import (
	"encoding/json"
	"fmt"
	"strings"
)

// WorkflowTaskContext mirrors the `workflow` block the server attaches to a
// claimed task. It is defined here rather than in the daemon package because
// the brief renderer is what consumes it, and the daemon already depends on
// execenv.
//
// Every field is optional on the wire: a server that predates the block sends
// nothing and the daemon renders no protocol section, which is the same
// observable result as a non-workflow issue.
type WorkflowTaskContext struct {
	InstanceID       string                    `json:"instance_id"`
	Phase            string                    `json:"phase,omitempty"`
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

type WorkflowReviewSubmission struct {
	ID           string          `json:"id"`
	Summary      string          `json:"summary,omitempty"`
	WorkerOutput string          `json:"worker_output,omitempty"`
	Evidence     json.RawMessage `json:"evidence,omitempty"`
}

// WorkflowChoiceDuty is the routing decision this node owes a downstream
// gateway. It is nil when nothing branches on this node.
//
// The options are derived from the graph, not from the template author's
// prose. A node whose choice decides the path but whose description forgets to
// say so takes the default branch on every run, and nothing anywhere reports
// that a decision was never made.
type WorkflowChoiceDuty struct {
	GatewayName   string                 `json:"gateway_name,omitempty"`
	DefaultTarget string                 `json:"default_target,omitempty"`
	Options       []WorkflowChoiceOption `json:"options,omitempty"`
}

type WorkflowChoiceOption struct {
	Value  string `json:"value"`
	Target string `json:"target,omitempty"`
}

// WorkflowReworkContext explains why a node is being executed again. It is nil
// on a first attempt.
//
// The prior attempt's work lives on the same issue — rework continues there
// rather than opening a new one — so this carries only what the issue cannot
// say for itself: that this is a retry, and which judgement sent it back.
type WorkflowReworkContext struct {
	Attempt int    `json:"attempt"`
	Source  string `json:"source,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// WorkflowArtifactDuty is one artifact the node owes plus its delivery state.
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

// WorkflowUpstreamContext is one direct predecessor's conclusion.
//
// Summary is what its author concluded. WorkerOutput is only set when nobody
// wrote one: it is raw execution output the platform extracted, and it is a
// separate field so the brief can say which it is showing. Issues names where
// the full record lives.
type WorkflowUpstreamContext struct {
	NodeKey      string                     `json:"node_key"`
	Name         string                     `json:"name,omitempty"`
	Status       string                     `json:"status,omitempty"`
	Summary      string                     `json:"summary,omitempty"`
	WorkerOutput string                     `json:"worker_output,omitempty"`
	Issues       []string                   `json:"issues,omitempty"`
	Artifacts    []WorkflowUpstreamArtifact `json:"artifacts,omitempty"`
}

// WorkflowUpstreamArtifact is the index form of an upstream artifact — enough
// to decide whether to read the body, without carrying it.
type WorkflowUpstreamArtifact struct {
	ID          string `json:"id"`
	ArtifactKey string `json:"artifact_key"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
}

// renderWorkflowProtocol writes the node protocol into the brief.
//
// The protocol is stated in the brief rather than left to the agent to
// discover, for two reasons. A node's obligations decide whether the run
// advances, so they cannot depend on the agent remembering to ask. And the
// runtime's CLI may predate `multica workflow` entirely — a file the agent
// reads works regardless of CLI version, which is why every command here is
// paired with a `multica api` fallback addressed by node instance id.
func renderWorkflowProtocol(b *strings.Builder, workflow *WorkflowTaskContext) {
	if workflow == nil || workflow.NodeKey == "" {
		return
	}
	if workflow.Phase == "critic" {
		renderWorkflowCriticProtocol(b, workflow)
		return
	}
	b.WriteString("## Workflow Protocol\n\n")
	name := workflow.NodeName
	if name == "" {
		name = workflow.NodeKey
	}
	subject := "This issue is"
	if workflow.DirectExecution {
		subject = "This direct agent task executes"
	}
	fmt.Fprintf(b,
		"%s node **%s** (`%s`) of workflow run `%s`. "+
			"The run advances only when this node's obligations below are met — "+
			"finishing the work without them leaves the whole workflow blocked.\n\n",
		subject, name, workflow.NodeKey, workflow.InstanceID)
	if workflow.RunTitle != "" {
		fmt.Fprintf(b, "Run: **%s**\n\n", workflow.RunTitle)
	}
	if workflow.Instructions != "" {
		fmt.Fprintf(b, "Node instructions:\n\n%s\n\n", workflow.Instructions)
	}
	if workflow.HostIssue != "" {
		fmt.Fprintf(b, "The requirement this run serves is **%s** — read it for the "+
			"original ask; this issue's description carries only the node's own "+
			"instructions.\n\n", workflow.HostIssue)
	}

	renderWorkflowRework(b, workflow.Rework)
	renderWorkflowChoice(b, workflow.Choice)
	renderWorkflowUpstream(b, workflow.InstanceID, workflow.Upstream)
	renderWorkflowDuties(b, workflow)
}

// renderWorkflowCriticProtocol is a built-in, versioned review contract. The
// selected agent supplies domain expertise through its own Instructions; the
// workflow supplies the evidence and keeps the verdict schema stable.
func renderWorkflowCriticProtocol(b *strings.Builder, workflow *WorkflowTaskContext) {
	b.WriteString("## Workflow Critic Protocol (v1)\n\n")
	name := workflow.NodeName
	if name == "" {
		name = workflow.NodeKey
	}
	fmt.Fprintf(b, "You are the Critic for node **%s** (`%s`) in workflow run `%s`. Review the Worker output; do not redo the Worker task.\n\n", name, workflow.NodeKey, workflow.InstanceID)
	b.WriteString("Judge the result using all applicable business context:\n\n")
	if workflow.HostIssue != "" {
		fmt.Fprintf(b, "- Requirement Issue `%s`: read its description and acceptance criteria with `multica issue get %s --output json`.\n", workflow.HostIssue, workflow.HostIssue)
	}
	if workflow.Instructions != "" {
		fmt.Fprintf(b, "- Node name and description:\n\n%s\n\n", workflow.Instructions)
	}
	for _, issue := range workflow.NodeIssues {
		fmt.Fprintf(b, "- Worker Issue `%s`: inspect its description, comments, deliverables, and linked PR/MR.\n", issue)
	}
	if submission := workflow.ReviewSubmission; submission != nil {
		fmt.Fprintf(b, "- Worker submission `%s`", submission.ID)
		if strings.TrimSpace(submission.Summary) != "" {
			fmt.Fprintf(b, ": %s", strings.TrimSpace(submission.Summary))
		}
		b.WriteString(".\n")
		if strings.TrimSpace(submission.WorkerOutput) != "" {
			b.WriteString("\nWorker final output:\n\n")
			for line := range strings.SplitSeq(submission.WorkerOutput, "\n") {
				fmt.Fprintf(b, "> %s\n", line)
			}
			b.WriteString("\n")
		}
	}
	for _, artifact := range workflow.Artifacts {
		if !artifact.Delivered {
			continue
		}
		fmt.Fprintf(b, "- Deliverable `%s` — %s (%s)", artifact.Key, artifact.Name, artifact.Kind)
		if artifact.ID != "" {
			fmt.Fprintf(b, ", id `%s`; inspect with `multica workflow artifact get %s`", artifact.ID, artifact.ID)
		}
		b.WriteString(".\n")
	}
	b.WriteString("\nAlso apply your own agent Instructions as domain-specific review guidance. Approve only when the delivered result satisfies the requirement and node obligations. On rejection, give a concise, actionable reason the Worker can use for rework.\n\n")
	b.WriteString("Your final output must be exactly one JSON object with no prose or Markdown fence:\n\n")
	b.WriteString("```json\n{\"approved\":true,\"comment\":\"short review opinion\"}\n```\n\n")
	b.WriteString("Use `approved: false` when rejecting; `comment` must explain what must change. The server records this object as the node verdict: approval advances the workflow, rejection sends the node to rework.\n")
}

// renderWorkflowRework writes why this node is being executed again. It comes
// before the upstream conclusions because it changes what the agent should do
// with them: on a retry the upstream has not moved, and the thing that has is
// the judgement below.
func renderWorkflowRework(b *strings.Builder, rework *WorkflowReworkContext) {
	if rework == nil || rework.Attempt < 2 {
		return
	}
	b.WriteString("### 你为什么回到这里\n\n")
	fmt.Fprintf(b, "这是本节点的第 %d 次执行。上一次的产出留在同一个 Issue 上——"+
		"先读它和它的评论，只改被判定有问题的部分，不要从头重做。\n\n", rework.Attempt)
	switch rework.Source {
	case "acceptance":
		b.WriteString("退回来源：**验收驳回**\n\n")
	case "critic":
		b.WriteString("退回来源：**智能体评审驳回**\n\n")
	case "manual_rollback":
		b.WriteString("退回来源：**人工回滚**\n\n")
	}
	if reason := strings.TrimSpace(rework.Reason); reason != "" {
		for line := range strings.SplitSeq(reason, "\n") {
			fmt.Fprintf(b, "> %s\n", line)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("> (没有填写退回理由——先在 Issue 上问清楚再动手，" +
			"盲目重做很可能被再次退回。)\n\n")
	}
}

// renderWorkflowChoice writes the branch decision this node owes. It states
// the consequence of not choosing, so skipping the decision is at least an
// informed choice rather than an unnoticed one.
func renderWorkflowChoice(b *strings.Builder, choice *WorkflowChoiceDuty) {
	if choice == nil || len(choice.Options) == 0 {
		return
	}
	b.WriteString("### 这个节点要选一条分支\n\n")
	gateway := choice.GatewayName
	if gateway == "" {
		gateway = "下游网关"
	}
	fmt.Fprintf(b, "下游的「%s」会读你的选择来决定流程走向。可选：\n\n", gateway)
	for _, option := range choice.Options {
		if option.Target != "" {
			fmt.Fprintf(b, "- `%s` — 走向「%s」\n", option.Value, option.Target)
		} else {
			fmt.Fprintf(b, "- `%s`\n", option.Value)
		}
	}
	b.WriteString("\n提交时带上选择：\n\n")
	b.WriteString("```\nmultica workflow submit --summary \"<结论>\" --choice <上面的某个值>\n```\n\n")
	if choice.DefaultTarget != "" {
		fmt.Fprintf(b, "不选则走默认路径「%s」。如果你的结论其实指向别的分支，"+
			"不选就等于把判断丢掉了。\n\n", choice.DefaultTarget)
	}
}

// renderWorkflowUpstream writes what the predecessors concluded. Summaries are
// inlined because they are short by construction (capped server-side) and are
// the first thing this node needs; artifact bodies stay behind an id.
func renderWorkflowUpstream(
	b *strings.Builder,
	instanceID string,
	upstream []WorkflowUpstreamContext,
) {
	if len(upstream) == 0 {
		return
	}
	b.WriteString("### What upstream handed you\n\n")
	for _, entry := range upstream {
		name := entry.Name
		if name == "" {
			name = entry.NodeKey
		}
		fmt.Fprintf(b, "**%s** (%s)\n\n", name, entry.Status)
		if summary := strings.TrimSpace(entry.Summary); summary != "" {
			for line := range strings.SplitSeq(summary, "\n") {
				fmt.Fprintf(b, "> %s\n", line)
			}
			b.WriteString("\n")
		} else if output := strings.TrimSpace(entry.WorkerOutput); output != "" {
			// Labelled, not quoted like a summary. Nobody concluded anything
			// here — this is the transcript the platform found, and reading it
			// as a handoff would credit a conclusion that was never drawn.
			b.WriteString("No handoff summary was submitted. Its executor's raw " +
				"output is below — treat it as evidence, not as a conclusion:\n\n")
			b.WriteString("```\n" + output + "\n```\n\n")
		} else {
			b.WriteString("> (no handoff summary was submitted)\n\n")
		}
		if len(entry.Issues) > 0 {
			// A summary is capped server-side, so the detail behind it is on
			// the issue. Without these ids there is no way to reach it.
			for _, issue := range entry.Issues {
				fmt.Fprintf(b, "- issue `%s` — `multica issue get %s --output json`, "+
					"`multica issue comment list %s --output json`\n", issue, issue, issue)
			}
			b.WriteString("\n")
		}
		for _, artifact := range entry.Artifacts {
			fmt.Fprintf(b, "- artifact `%s` — %s (%s), id `%s`\n",
				artifact.ArtifactKey, artifact.Name, artifact.Kind, artifact.ID)
		}
		if len(entry.Artifacts) > 0 {
			// "The summary is the conclusion" only holds when there is one.
			// Told that with an empty handoff, an agent would skip the bodies
			// believing it had already been given the conclusion.
			lead := "Read a body only when you need it — the summary above is the conclusion."
			if strings.TrimSpace(entry.Summary) == "" {
				lead = "Nothing above concludes for you, so read what you need."
			}
			fmt.Fprintf(b, "\n%s `multica workflow artifact get <id>`, or on an older CLI "+
				"`multica api get /api/workflow-instances/%s/artifacts` and match the id.\n\n",
				lead, instanceID)
		}
	}
}

// renderWorkflowDuties writes what this node owes and exactly how to deliver it.
func renderWorkflowDuties(b *strings.Builder, workflow *WorkflowTaskContext) {
	if len(workflow.Artifacts) == 0 && !workflow.HandoffRequired {
		return
	}
	b.WriteString("### What this node owes\n\n")
	for _, duty := range workflow.Artifacts {
		state := "not submitted"
		switch {
		case duty.Delivered && duty.ReviewStatus == "rejected":
			state = "rejected in review — resubmit"
		case duty.Delivered:
			state = "submitted (" + duty.ReviewStatus + ")"
		}
		necessity := "optional"
		if duty.Required {
			necessity = "required"
		}
		fmt.Fprintf(b, "- `%s` — %s (%s, %s) — **%s**\n",
			duty.Key, duty.Name, duty.Kind, necessity, state)
		if duty.Description != "" {
			fmt.Fprintf(b, "  - %s\n", duty.Description)
		}
	}
	if len(workflow.Artifacts) > 0 {
		b.WriteString("\nThe keys above are fixed by the workflow template. " +
			"The server rejects any key it did not declare, so use them verbatim.\n\n")
		b.WriteString("Write your document to a file and submit the file:\n\n")
		b.WriteString("```\nmultica workflow submit --artifact <key> --file <path>\n```\n\n")
		b.WriteString("If the file is already on the issue as a comment attachment, " +
			"register that attachment instead of uploading a second copy:\n\n")
		b.WriteString("```\nmultica workflow submit --artifact <key> --attachment-id <attachment-id>\n```\n\n")
		fmt.Fprintf(b, "If `multica workflow` is not a known command, this runtime's CLI "+
			"predates it — submit through the API instead:\n\n"+
			"```\nmultica api post /api/workflow-node-instances/%s/artifacts --content-file body.json\n```\n\n"+
			"where `body.json` is `{\"artifact_key\":\"<key>\",\"content\":\"<document text>\"}` "+
			"for a document, or `{\"artifact_key\":\"<key>\",\"attachment_id\":\"<id>\"}` for an attachment.\n\n",
			workflow.NodeInstanceID)
	}
	if workflow.HandoffRequired {
		b.WriteString("Before you finish, hand off your conclusion — the next node reads " +
			"this first, and it is not a copy of the document:\n\n")
		b.WriteString("```\nmultica workflow submit --summary \"<conclusion, decisions, " +
			"open risks, what the next node should watch for>\"\n```\n\n")
	}
	if pending := workflow.PendingRequiredArtifacts(); len(pending) > 0 {
		keys := make([]string, 0, len(pending))
		for _, duty := range pending {
			keys = append(keys, "`"+duty.Key+"`")
		}
		fmt.Fprintf(b, "Still outstanding right now: %s.\n\n", strings.Join(keys, ", "))
	}
}

// PendingRequiredArtifacts returns the required artifacts still missing. A
// rejected artifact counts as pending because it blocks completion exactly
// like a missing one.
func (w *WorkflowTaskContext) PendingRequiredArtifacts() []WorkflowArtifactDuty {
	if w == nil {
		return nil
	}
	pending := make([]WorkflowArtifactDuty, 0, len(w.Artifacts))
	for _, duty := range w.Artifacts {
		if !duty.Required {
			continue
		}
		if !duty.Delivered || duty.ReviewStatus == "rejected" {
			pending = append(pending, duty)
		}
	}
	return pending
}
