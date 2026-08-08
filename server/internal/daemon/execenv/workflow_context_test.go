package execenv

import (
	"strings"
	"testing"
)

func workflowFixture() *WorkflowTaskContext {
	return &WorkflowTaskContext{
		InstanceID:      "run-1",
		NodeInstanceID:  "node-inst-1",
		NodeKey:         "implement",
		NodeName:        "代码实施",
		HostIssue:       "WTE-14773",
		HandoffRequired: true,
		Artifacts: []WorkflowArtifactDuty{
			{Key: "impl_notes", Name: "实现说明", Kind: "document", Required: true},
		},
		Upstream: []WorkflowUpstreamContext{{
			NodeKey: "plan",
			Name:    "方案设计",
			Status:  "completed",
			Summary: "页面信息架构：\n顶部产品名与筛选。",
			Artifacts: []WorkflowUpstreamArtifact{{
				ID: "art-1", ArtifactKey: "design_doc",
				Kind: "document", Name: "技术方案",
			}},
		}},
	}
}

// The whole point of pushing the protocol is that the node's obligations reach
// the agent without it running a command. If the upstream conclusion or the
// artifact keys were missing from the brief, the agent would be back to
// guessing from the issue description alone.
func TestRenderIssueContext_WorkflowProtocol(t *testing.T) {
	md := renderIssueContext("claude", TaskContextForEnv{
		IssueID:  "issue-1",
		Workflow: workflowFixture(),
	})

	for _, want := range []string{
		"## Workflow Protocol",
		"代码实施",
		"WTE-14773",
		"### What upstream handed you",
		"方案设计",
		"> 页面信息架构：",
		"design_doc",
		"### What this node owes",
		"`impl_notes`",
		"multica workflow submit --artifact <key> --file <path>",
		"--attachment-id <attachment-id>",
		"multica workflow submit --summary",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("workflow protocol is missing %q:\n%s", want, md)
		}
	}
}

// A multi-line handoff summary must stay inside the blockquote. A bare newline
// would let the second line render as body text and read as the brief's own
// instruction rather than the upstream node's conclusion.
func TestRenderIssueContext_WorkflowSummaryStaysQuoted(t *testing.T) {
	md := renderIssueContext("claude", TaskContextForEnv{
		IssueID:  "issue-1",
		Workflow: workflowFixture(),
	})
	if !strings.Contains(md, "> 顶部产品名与筛选。") {
		t.Errorf("continuation line left the blockquote:\n%s", md)
	}
}

// A predecessor whose executor never wrote a conclusion used to hand the next
// node the literal string "(no handoff summary was submitted)" and nothing
// else. Its raw output is the only record of what happened, so the brief
// carries it — labelled as evidence, because nobody concluded anything.
func TestRenderIssueContext_WorkflowUpstreamFallsBackToWorkerOutput(t *testing.T) {
	workflow := workflowFixture()
	workflow.Upstream[0].Summary = ""
	workflow.Upstream[0].WorkerOutput = "Rewrote the pricing table query and benchmarked it."

	md := renderIssueContext("claude", TaskContextForEnv{
		IssueID:  "issue-1",
		Workflow: workflow,
	})

	if !strings.Contains(md, "Rewrote the pricing table query") {
		t.Errorf("upstream worker output is missing:\n%s", md)
	}
	if !strings.Contains(md, "treat it as evidence, not as a conclusion") {
		t.Errorf("worker output was not labelled as evidence:\n%s", md)
	}
	if strings.Contains(md, "> Rewrote the pricing table query") {
		t.Errorf("raw output was quoted like an authored handoff:\n%s", md)
	}
	if strings.Contains(md, "the summary above is the conclusion") {
		t.Errorf("brief claims a conclusion that was never written:\n%s", md)
	}
}

// The fallback exists for an absent conclusion. When the executor did write
// one, that is what the next node acts on — appending the transcript as well
// would bury it.
func TestRenderIssueContext_WorkflowSummaryWinsOverWorkerOutput(t *testing.T) {
	workflow := workflowFixture()
	workflow.Upstream[0].WorkerOutput = "ran the benchmark suite twice"

	md := renderIssueContext("claude", TaskContextForEnv{
		IssueID:  "issue-1",
		Workflow: workflow,
	})

	if !strings.Contains(md, "> 页面信息架构：") {
		t.Errorf("authored summary is missing:\n%s", md)
	}
	if strings.Contains(md, "ran the benchmark suite twice") {
		t.Errorf("transcript rendered alongside an authored conclusion:\n%s", md)
	}
}

// A handoff summary is capped server-side, so the detail behind it lives on the
// predecessor's issue. Without its identifier the agent cannot reach any of it.
func TestRenderIssueContext_WorkflowUpstreamNamesItsIssues(t *testing.T) {
	workflow := workflowFixture()
	workflow.Upstream[0].Issues = []string{"WTE-14775"}

	md := renderIssueContext("claude", TaskContextForEnv{
		IssueID:  "issue-1",
		Workflow: workflow,
	})

	for _, want := range []string{
		"WTE-14775",
		"multica issue get WTE-14775 --output json",
		"multica issue comment list WTE-14775 --output json",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("upstream issue entry point %q is missing:\n%s", want, md)
		}
	}
}

// An older runtime CLI has no `workflow` subcommand at all, so the protocol has
// to carry a route that works without it — addressed by node instance id,
// which is exactly why the server pushes that id.
func TestRenderIssueContext_WorkflowOldCLIFallback(t *testing.T) {
	md := renderIssueContext("claude", TaskContextForEnv{
		IssueID:  "issue-1",
		Workflow: workflowFixture(),
	})
	want := "multica api post /api/workflow-node-instances/node-inst-1/artifacts"
	if !strings.Contains(md, want) {
		t.Errorf("missing old-CLI fallback %q:\n%s", want, md)
	}
}

// Ordinary issues must not grow a workflow section.
func TestRenderIssueContext_NoWorkflow(t *testing.T) {
	md := renderIssueContext("claude", TaskContextForEnv{IssueID: "issue-1"})
	if strings.Contains(md, "Workflow Protocol") {
		t.Errorf("non-workflow issue got a protocol section:\n%s", md)
	}
}

func TestRenderIssueContext_WorkflowCriticProtocol(t *testing.T) {
	workflow := workflowFixture()
	workflow.Phase = "critic"
	workflow.NodeIssues = []string{"WTE-14774"}
	workflow.ReviewSubmission = &WorkflowReviewSubmission{
		ID: "submission-1", Summary: "Implemented the requested behavior",
		WorkerOutput: "Added the endpoint and tests",
	}
	workflow.Artifacts[0].ID = "artifact-1"
	workflow.Artifacts[0].Delivered = true
	md := renderIssueContext("claude", TaskContextForEnv{
		Workflow: workflow,
	})

	for _, want := range []string{
		"Workflow Critic Protocol (v1)",
		"WTE-14773",
		"WTE-14774",
		"submission-1",
		"Added the endpoint and tests",
		"artifact-1",
		"own agent Instructions",
		`{"result":"pass","reason":"short review opinion"}`,
	} {
		if !strings.Contains(md, want) {
			t.Errorf("critic protocol is missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "What this node owes") {
		t.Errorf("critic received the Worker delivery protocol:\n%s", md)
	}
}

// The retry has to name the objection and quote the Critic's own words. Told
// only "your verdict was invalid", a reviewer re-reads the work and may land
// somewhere else; shown what it wrote, it restates the judgement it already
// reached.
func TestRenderIssueContext_WorkflowCriticVerdictRetry(t *testing.T) {
	workflow := workflowFixture()
	workflow.Phase = "critic"
	workflow.VerdictRetry = &WorkflowVerdictRetry{
		Problem: `decode critic verdict: json: unknown field "verdict"`,
		Wrote:   `{"verdict":"reject","reason":"deleted the button","confidence":0.93}`,
	}
	md := renderIssueContext("claude", TaskContextForEnv{Workflow: workflow})

	for _, want := range []string{
		"previous verdict could not be read",
		`unknown field "verdict"`,
		`> {"verdict":"reject","reason":"deleted the button","confidence":0.93}`,
		"Keep the same judgement",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("verdict retry is missing %q:\n%s", want, md)
		}
	}

	// An ordinary Critic task must carry none of it.
	workflow.VerdictRetry = nil
	if md := renderIssueContext("claude", TaskContextForEnv{Workflow: workflow}); strings.Contains(
		md, "previous verdict could not be read",
	) {
		t.Errorf("a first-attempt Critic was told its verdict failed:\n%s", md)
	}
}

// A rejected artifact blocks completion exactly like a missing one, so it has
// to read as outstanding rather than as delivered.
func TestPendingRequiredArtifacts(t *testing.T) {
	workflow := &WorkflowTaskContext{Artifacts: []WorkflowArtifactDuty{
		{Key: "a", Required: true, Delivered: true, ReviewStatus: "approved"},
		{Key: "b", Required: true, Delivered: true, ReviewStatus: "rejected"},
		{Key: "c", Required: true},
		{Key: "d", Required: false},
	}}
	pending := workflow.PendingRequiredArtifacts()
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending, got %d: %+v", len(pending), pending)
	}
	if pending[0].Key != "b" || pending[1].Key != "c" {
		t.Errorf("unexpected pending set: %+v", pending)
	}
	if (*WorkflowTaskContext)(nil).PendingRequiredArtifacts() != nil {
		t.Error("nil context must yield no pending artifacts")
	}
}

// A re-attempted node is the one place an agent is most likely to repeat work
// it already did. The brief has to say that this is a retry and why the
// previous attempt was sent back — the issue itself carries the prior work, but
// nothing on it says which part was judged wrong.
func TestRenderIssueContext_WorkflowRework(t *testing.T) {
	workflow := workflowFixture()
	workflow.Rework = &WorkflowReworkContext{
		Attempt: 2,
		Source:  "acceptance",
		Reason:  "AC-004 未通过：~> 5.3 应阻断却放行",
	}
	md := renderIssueContext("claude", TaskContextForEnv{
		IssueID:  "issue-1",
		Workflow: workflow,
	})
	for _, want := range []string{
		"第 2 次",
		"AC-004 未通过：~> 5.3 应阻断却放行",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("rework brief missing %q:\n%s", want, md)
		}
	}
}

// A first attempt must not read as a retry.
func TestRenderIssueContext_NoReworkOnFirstAttempt(t *testing.T) {
	md := renderIssueContext("claude", TaskContextForEnv{
		IssueID:  "issue-1",
		Workflow: workflowFixture(),
	})
	if strings.Contains(md, "第 2 次") {
		t.Fatalf("first attempt rendered as rework:\n%s", md)
	}
}

// A node that owes structured fields has to learn it from the brief — the
// table names the values and the submit flags, and it never names a branch or
// a downstream node.
func TestRenderIssueContext_WorkflowOutputs(t *testing.T) {
	workflow := workflowFixture()
	workflow.Outputs = []WorkflowOutputDuty{
		{Key: "is_bug", Type: "bool", Required: true, Desc: "是否为真实缺陷"},
		{Key: "category", Type: "enum", Values: []string{"bug", "duplicate"}, Required: true},
		{Key: "root_cause", Type: "string"},
	}
	md := renderIssueContext("claude", TaskContextForEnv{
		IssueID:  "issue-1",
		Workflow: workflow,
	})
	for _, want := range []string{
		"结构化字段",
		"is_bug",
		"枚举: bug / duplicate",
		"是否为真实缺陷",
		"--set",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("outputs brief missing %q:\n%s", want, md)
		}
	}
}

// A node declaring no outputs must not be told to deliver fields.
func TestRenderIssueContext_NoOutputsSection(t *testing.T) {
	md := renderIssueContext("claude", TaskContextForEnv{
		IssueID:  "issue-1",
		Workflow: workflowFixture(),
	})
	if strings.Contains(md, "--set") {
		t.Fatalf("node without outputs was told to deliver fields:\n%s", md)
	}
}

// Three consecutive runs delivered the artifact and never the fields, then sat
// waiting on a field the brief had already listed. The brief was not missing
// the contract — it was stating it twice, in two sections, each ending in its
// own `multica workflow submit` line with nothing saying the other was also
// owed. An agent reading to the end ran the last command it saw.
func TestWorkflowBriefStatesOneDeliveryForFieldsAndArtifacts(t *testing.T) {
	workflow := &WorkflowTaskContext{
		InstanceID: "instance-1",
		NodeKey:    "root_cause",
		NodeName:   "根因分析",
		Outputs: []WorkflowOutputDuty{
			{Key: "root_cause_found", Type: "bool", Required: true},
			{Key: "component", Type: "string"},
		},
		Artifacts: []WorkflowArtifactDuty{
			{Key: "root_cause_report", Name: "根因分析", Kind: "document", Required: true},
		},
	}
	var b strings.Builder
	renderWorkflowProtocol(&b, workflow)
	brief := b.String()

	// One command that satisfies the whole obligation, so following the brief
	// literally cannot leave half of it undone.
	if !strings.Contains(brief, "--set root_cause_found=") ||
		!strings.Contains(brief, "--artifact root_cause_report") {
		t.Fatalf("brief has no combined delivery command:\n%s", brief)
	}
	combined := false
	for _, line := range strings.Split(brief, "\n") {
		if strings.Contains(line, "--set root_cause_found=") &&
			strings.Contains(line, "--artifact root_cause_report") {
			combined = true
		}
	}
	if !combined {
		t.Fatalf("fields and artifact are still handed over separately:\n%s", brief)
	}

	// An optional field is not part of what blocks the node, so it does not
	// belong in the command that says how to unblock it.
	if strings.Contains(brief, "--set component=") {
		t.Fatalf("optional field appears in the required delivery command:\n%s", brief)
	}
}

// A node owing only one of the two already says everything in that kind's own
// section; repeating it under a second heading is noise.
func TestWorkflowBriefSaysItOnceWhenOnlyOneKindIsOwed(t *testing.T) {
	var b strings.Builder
	renderWorkflowProtocol(&b, &WorkflowTaskContext{
		InstanceID: "instance-1",
		NodeKey:    "triage",
		Outputs: []WorkflowOutputDuty{
			{Key: "is_bug", Type: "bool", Required: true},
		},
	})
	if strings.Contains(b.String(), "不是两份") {
		t.Fatalf("fields-only node got the combined contract:\n%s", b.String())
	}
}

// A node owing both kinds used to get three submit commands: one under the
// field table, one under the artifact list, and the combined one. Each read as
// a complete instruction on its own, and a real agent ran the artifact one,
// wrote "delivered" on the issue, and left the node waiting on a field nobody
// had asked it for in a command it could run.
func TestWorkflowBriefOffersNoPartialDeliveryCommand(t *testing.T) {
	var b strings.Builder
	renderWorkflowProtocol(&b, &WorkflowTaskContext{
		InstanceID: "instance-1",
		NodeKey:    "root_cause",
		NodeName:   "根因分析",
		Outputs: []WorkflowOutputDuty{
			{Key: "root_cause_found", Type: "bool", Required: true},
		},
		Artifacts: []WorkflowArtifactDuty{
			{Key: "root_cause_report", Name: "根因分析", Kind: "document", Required: true},
		},
	})
	for _, line := range strings.Split(b.String(), "\n") {
		if !strings.Contains(line, "workflow submit") {
			continue
		}
		// Every runnable submit line must carry the whole obligation. A line
		// with one half and not the other is the trap.
		hasField := strings.Contains(line, "--set root_cause_found=")
		hasArtifact := strings.Contains(line, "--artifact root_cause_report")
		if hasField != hasArtifact {
			t.Fatalf("brief offers a command that delivers only half:\n%s\n\nfull brief:\n%s",
				line, b.String())
		}
	}
}
