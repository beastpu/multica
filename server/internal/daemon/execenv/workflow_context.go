package execenv

import (
	"encoding/json"
	"fmt"
	"strings"

	workflowdomain "github.com/multica-ai/multica/server/internal/workflow"
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
	Outputs          []WorkflowOutputDuty      `json:"outputs,omitempty"`
	ReviewSubmission *WorkflowReviewSubmission `json:"review_submission,omitempty"`
	VerdictRetry     *WorkflowVerdictRetry     `json:"verdict_retry,omitempty"`
}

// WorkflowVerdictRetry is set when this Critic task replaces one whose verdict
// the server could not read. It carries the objection and the earlier output so
// the same judgement can be restated in the required shape.
type WorkflowVerdictRetry struct {
	Problem string `json:"problem"`
	Wrote   string `json:"wrote"`
}

type WorkflowReviewSubmission struct {
	ID           string          `json:"id"`
	Summary      string          `json:"summary,omitempty"`
	WorkerOutput string          `json:"worker_output,omitempty"`
	Evidence     json.RawMessage `json:"evidence,omitempty"`
}

// WorkflowOutputDuty is one structured field this node owes on delivery. The
// declaration never names a downstream node or a branch: the executor reports
// domain facts, and the graph decides where they route.
type WorkflowOutputDuty struct {
	Key      string   `json:"key"`
	Type     string   `json:"type"`
	Values   []string `json:"values,omitempty"`
	Required bool     `json:"required,omitempty"`
	Desc     string   `json:"desc,omitempty"`
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

	// When the node owes both kinds, the sections below describe what is owed
	// and the delivery contract states the one command that discharges it. A
	// section that also printed its own runnable command would read as a
	// complete instruction while covering half the obligation.
	deferDelivery := workflowOwesBothKinds(workflow)

	renderWorkflowRework(b, workflow.Rework)
	renderWorkflowOutputs(b, workflow.Outputs, deferDelivery)
	renderWorkflowUpstream(b, workflow.InstanceID, workflow.Upstream)
	renderWorkflowDuties(b, workflow, deferDelivery)
	renderWorkflowDeliveryContract(b, workflow)
}

// workflowOwesBothKinds reports whether the node still owes required
// structured fields and required artifacts at the same time — the case where
// any single command is necessarily partial.
func workflowOwesBothKinds(workflow *WorkflowTaskContext) bool {
	requiredFields := 0
	for _, output := range workflow.Outputs {
		if output.Required {
			requiredFields++
		}
	}
	return requiredFields > 0 && len(workflow.PendingRequiredArtifacts()) > 0
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
	// A reviewer looks for a command, because that is what an agent does with
	// a decision. It used to have none, and the consequences were both kinds of
	// wrong: one run spent itself guessing request bodies against an API that
	// refuses agents, and three reviews stated their verdict in prose the
	// server then had to guess at. Give it the command.
	b.WriteString("State your verdict by running:\n\n")
	fmt.Fprintf(
		b,
		"```bash\nmultica workflow review --decision %s --reason \"what must change\"\n```\n\n",
		strings.Join(workflowdomain.CriticResults, "|"),
	)
	b.WriteString("Run it once, then finish the task. The verdict is applied when the task ends, so nothing is recorded twice and nothing is recorded early.\n\n")
	fmt.Fprintf(
		b,
		"Use `fail` to send the node back for rework, and `blocked` when you cannot judge it at all; both need a `--reason` the worker can act on. `pass` advances the workflow.\n\n",
	)
	b.WriteString("If the command is unavailable, fall back to ending with exactly one JSON object and no prose or fence:\n\n")
	b.WriteString("```json\n{\"result\":\"pass\",\"reason\":\"short review opinion\"}\n```\n")
	renderWorkflowVerdictRetry(b, workflow.VerdictRetry)
}

// renderWorkflowVerdictRetry comes last so it is the final instruction read.
//
// The judgement is not in question here — only its shape. Restating the
// objection without the original output would invite the Critic to review the
// work a second time, which wastes the first review and can reach a different
// conclusion on the same evidence.
func renderWorkflowVerdictRetry(b *strings.Builder, retry *WorkflowVerdictRetry) {
	if retry == nil {
		return
	}
	b.WriteString("\n### Your previous verdict could not be read\n\n")
	fmt.Fprintf(b, "The server rejected it: %s\n\n", strings.TrimSpace(retry.Problem))
	if wrote := strings.TrimSpace(retry.Wrote); wrote != "" {
		b.WriteString("What you wrote:\n\n")
		for line := range strings.SplitSeq(wrote, "\n") {
			fmt.Fprintf(b, "> %s\n", line)
		}
		b.WriteString("\n")
	}
	b.WriteString("Keep the same judgement. Re-express it as the exact object above: only `result` and `reason`, no other keys, no prose, no fence. Put your reasoning — including any findings you listed — into `reason`.\n")
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
	case "manual_review":
		b.WriteString("退回来源：**人工驳回**\n\n")
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

// renderWorkflowOutputs writes the structured fields this node owes. The table
// carries the machine values and the descriptions together, and the section
// never names a downstream node — the executor reports facts, not routes.
func renderWorkflowOutputs(b *strings.Builder, outputs []WorkflowOutputDuty, deferDelivery bool) {
	if len(outputs) == 0 {
		return
	}
	b.WriteString("### 这个节点要交付的结构化字段\n\n")
	b.WriteString("| 字段 | 类型 | 必填 | 说明 |\n| --- | --- | --- | --- |\n")
	example := ""
	for _, output := range outputs {
		kind := output.Type
		if output.Type == "enum" {
			kind = "枚举: " + strings.Join(output.Values, " / ")
		}
		required := "否"
		if output.Required {
			required = "是"
			if example == "" && len(output.Values) > 0 {
				example = fmt.Sprintf(" --set %s=%s", output.Key, output.Values[0])
			} else if example == "" {
				example = fmt.Sprintf(" --set %s=<值>", output.Key)
			}
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", output.Key, kind, required, output.Desc)
	}
	b.WriteString("\n枚举字段只认上表中的英文值。" +
		"字段校验不通过时命令会返回每个字段的具体问题，按提示修正后重交。" +
		"缺了必填字段流程会拒绝这次交付。\n\n")
	if deferDelivery {
		b.WriteString("交付命令见下方「这个节点欠的是一份交付，不是两份」。\n\n")
		return
	}
	b.WriteString("提交（--set 可重复）：\n\n")
	fmt.Fprintf(b, "```\nmultica workflow submit --summary \"<结论>\"%s\n```\n\n", example)
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
func renderWorkflowDuties(b *strings.Builder, workflow *WorkflowTaskContext, deferDelivery bool) {
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
		b.WriteString("Write your document to a file, and submit it with the command " +
			"below — or, if this node also owes structured fields, with the single " +
			"command in the delivery section that carries both.\n\n")
		if !deferDelivery {
			b.WriteString("```\nmultica workflow submit --artifact <key> --file <path>\n```\n\n")
		}
		b.WriteString("If the file is already on the issue as a comment attachment, " +
			"register that attachment instead of uploading a second copy — swap " +
			"`--file <path>` for:\n\n")
		b.WriteString("```\n--attachment-id <attachment-id>\n```\n\n")
		// A run without a host issue has no comment to carry the file, which
		// used to leave an attachment artifact unsatisfiable: the only uploader
		// demanded a chat task this node does not have. Upload direct and pass
		// the id along.
		b.WriteString("If there is no issue to attach to, upload the file first " +
			"and submit the id it prints:\n\n")
		b.WriteString("```\nmultica attachment upload <path>\n```\n\n")
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

// renderWorkflowDeliveryContract states, once, everything the node still owes.
//
// The fields and the artifacts each arrived with their own `multica workflow
// submit` line, in their own section, neither saying the other existed. An
// agent that read to the end ran the last command it saw and stopped: three
// runs in a row delivered the artifact, never the fields, and sat waiting for
// a field the brief had already asked for — in a section it had scrolled past.
//
// One obligation, said in one place, with one command that satisfies it. The
// CLI has always accepted the combination; only the brief split it.
func renderWorkflowDeliveryContract(b *strings.Builder, workflow *WorkflowTaskContext) {
	pendingFields := make([]WorkflowOutputDuty, 0, len(workflow.Outputs))
	for _, output := range workflow.Outputs {
		if output.Required {
			pendingFields = append(pendingFields, output)
		}
	}
	pendingArtifacts := workflow.PendingRequiredArtifacts()
	if len(pendingFields) == 0 || len(pendingArtifacts) == 0 {
		// With only one kind outstanding, that kind's own section already says
		// the whole truth; repeating it would be noise.
		return
	}

	b.WriteString("### 这个节点欠的是一份交付，不是两份\n\n")
	b.WriteString("结构化字段和制品都要交齐，节点才会完成。" +
		"只交其中一样会一直停在这里，等待原因写着缺的那一样。\n\n")

	command := "multica workflow submit --summary \"<结论>\""
	for _, field := range pendingFields {
		value := "<值>"
		if len(field.Values) > 0 {
			value = field.Values[0]
		}
		command += fmt.Sprintf(" --set %s=%s", field.Key, value)
	}
	first := pendingArtifacts[0]
	switch first.Kind {
	case "link":
		command += fmt.Sprintf(" --artifact %s --url <地址>", first.Key)
	case "attachment":
		command += fmt.Sprintf(" --artifact %s --attachment-id <id>", first.Key)
	default:
		command += fmt.Sprintf(" --artifact %s --file <路径>", first.Key)
	}
	fmt.Fprintf(b, "```\n%s\n```\n\n", command)
	if len(pendingArtifacts) > 1 {
		b.WriteString("还欠的制品逐个补交：`multica workflow submit --artifact <key> ...`\n\n")
	}
}
