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
