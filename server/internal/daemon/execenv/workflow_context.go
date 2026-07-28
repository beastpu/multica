package execenv

import (
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
	InstanceID      string                    `json:"instance_id"`
	NodeInstanceID  string                    `json:"node_instance_id"`
	NodeKey         string                    `json:"node_key"`
	NodeName        string                    `json:"node_name,omitempty"`
	HostIssue       string                    `json:"host_issue,omitempty"`
	HandoffRequired bool                      `json:"handoff_required,omitempty"`
	Artifacts       []WorkflowArtifactDuty    `json:"artifacts,omitempty"`
	Upstream        []WorkflowUpstreamContext `json:"upstream,omitempty"`
}

// WorkflowArtifactDuty is one artifact the node owes plus its delivery state.
type WorkflowArtifactDuty struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Kind         string `json:"kind"`
	Required     bool   `json:"required"`
	Delivered    bool   `json:"delivered"`
	ReviewStatus string `json:"review_status,omitempty"`
}

// WorkflowUpstreamContext is one direct predecessor's conclusion.
type WorkflowUpstreamContext struct {
	NodeKey   string                     `json:"node_key"`
	Name      string                     `json:"name,omitempty"`
	Status    string                     `json:"status,omitempty"`
	Summary   string                     `json:"summary,omitempty"`
	Artifacts []WorkflowUpstreamArtifact `json:"artifacts,omitempty"`
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
	b.WriteString("## Workflow Protocol\n\n")
	name := workflow.NodeName
	if name == "" {
		name = workflow.NodeKey
	}
	fmt.Fprintf(b,
		"This issue is node **%s** (`%s`) of workflow run `%s`. "+
			"The run advances only when this node's obligations below are met — "+
			"finishing the work without them leaves the whole workflow blocked.\n\n",
		name, workflow.NodeKey, workflow.InstanceID)
	if workflow.HostIssue != "" {
		fmt.Fprintf(b, "The requirement this run serves is **%s** — read it for the "+
			"original ask; this issue's description carries only the node's own "+
			"instructions.\n\n", workflow.HostIssue)
	}

	renderWorkflowUpstream(b, workflow.InstanceID, workflow.Upstream)
	renderWorkflowDuties(b, workflow)
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
		} else {
			b.WriteString("> (no handoff summary was submitted)\n\n")
		}
		for _, artifact := range entry.Artifacts {
			fmt.Fprintf(b, "- artifact `%s` — %s (%s), id `%s`\n",
				artifact.ArtifactKey, artifact.Name, artifact.Kind, artifact.ID)
		}
		if len(entry.Artifacts) > 0 {
			fmt.Fprintf(b, "\nRead a body only when you need it — the summary above is "+
				"the conclusion. `multica workflow artifact get <id>`, or on an older CLI "+
				"`multica api get /api/workflow-instances/%s/artifacts` and match the id.\n\n",
				instanceID)
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
