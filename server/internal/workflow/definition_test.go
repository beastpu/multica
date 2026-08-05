package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func validDefinition() Definition {
	return Definition{
		SchemaVersion: DefinitionSchemaVersion,
		Name:          "Delivery",
		Roles: []RoleDefinition{
			{Key: "owner", Name: "Owner", Required: true, AllowedActorTypes: []string{"member"}},
			{Key: "executor", Name: "Executor", Required: true, AllowedActorTypes: []string{"agent"}},
		},
		Nodes: []NodeDefinition{
			{Key: "start", Kind: "start", Name: "Start"},
			{
				Key:       "implementation",
				Kind:      "activity",
				Name:      "Implementation",
				OwnerRole: "owner",
				Executor: &ExecutorDefinition{
					Kind: "role", Role: "executor",
					Fallback: &ExecutorDefinition{Kind: "manual"},
				},
				IssueTemplates: []IssueTemplate{{Key: "implement", Title: "Implement {{host.title}}", Required: true}},
			},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []EdgeDefinition{
			{From: "start", To: "implementation"},
			{From: "implementation", To: "end"},
		},
		Acceptance: AcceptanceDefinition{
			Policy:       "member",
			ApproverRole: "owner",
		},
	}
}

func TestValidateDefinition(t *testing.T) {
	if err := ValidateDefinition(validDefinition()); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}
}

func TestValidateDefinitionRejectsTaskScopedSubmissionWithoutIssues(t *testing.T) {
	for _, policy := range []string{"per_required_task", "fan_in"} {
		t.Run(policy, func(t *testing.T) {
			definition := validDefinition()
			node := &definition.Nodes[1]
			node.IssuePolicy = "none"
			node.IssueTemplates = nil
			node.SubmissionSchema = &SubmissionSchema{Policy: policy}

			err := ValidateDefinition(definition)
			if err == nil || !strings.Contains(err.Error(), "requires issue-backed tasks") {
				t.Fatalf("ValidateDefinition() error = %v, want issue-backed tasks error", err)
			}
		})
	}
}

// The default policy has to carry a node that declares nothing, because that
// is exactly the node this exists for: an author who fills in no template at
// all still gets an issue for the work to happen on.
func TestNodeIssueTemplatesSynthesisesOneForAuto(t *testing.T) {
	node := NodeDefinition{
		Key: "work", Kind: "activity", Name: "代码实施", IssuePolicy: "auto",
	}
	templates := NodeIssueTemplates(node)
	if len(templates) != 1 {
		t.Fatalf("auto templates = %d, want exactly one", len(templates))
	}
	template := templates[0]
	// The title carries the node name because a run with no host issue and no
	// node description leaves the node name as the only statement of what the
	// work is.
	if !strings.Contains(template.Title, "代码实施") {
		t.Errorf("auto title = %q, want the node name in it", template.Title)
	}
	if !strings.Contains(template.Title, "{{host.title}}") {
		t.Errorf("auto title = %q, want it scoped to the run it belongs to", template.Title)
	}
	if err := validateIssueTitleTemplate(template.Title); err != nil {
		t.Errorf("synthesised title is not a valid template: %v", err)
	}
	// A node whose issue nobody has to finish would complete while the work sits
	// untouched, which is the opposite of why the issue is created.
	if !template.Required {
		t.Error("auto task is optional; the node would complete without the work")
	}
}

// A node that names its own tasks keeps them; synthesising on top would give it
// an extra issue nobody declared.
func TestNodeIssueTemplatesLeavesDeclaredTemplatesAlone(t *testing.T) {
	node := NodeDefinition{
		Key: "work", Kind: "activity", Name: "Work", IssuePolicy: "fixed",
		IssueTemplates: []IssueTemplate{{Key: "a", Title: "A", Required: true}},
	}
	if got := NodeIssueTemplates(node); len(got) != 1 || got[0].Key != "a" {
		t.Fatalf("declared templates = %+v, want the author's own", got)
	}
	none := NodeDefinition{Key: "run", Kind: "activity", Name: "Run", IssuePolicy: "none"}
	if got := NodeIssueTemplates(none); len(got) != 0 {
		t.Fatalf("run-only templates = %+v, want none", got)
	}
}

func TestValidateDefinitionAcceptsAutoIssuePolicy(t *testing.T) {
	definition := validDefinition()
	node := &definition.Nodes[1]
	node.IssuePolicy = "auto"
	node.IssueTemplates = nil
	if err := ValidateDefinition(definition); err != nil {
		t.Fatalf("ValidateDefinition() error = %v, want auto accepted", err)
	}
}

// Auto means "one issue this node did not have to describe". Declaring
// templates alongside it says two different things about the same node.
func TestValidateDefinitionRejectsAutoWithDeclaredTemplates(t *testing.T) {
	definition := validDefinition()
	node := &definition.Nodes[1]
	node.IssuePolicy = "auto"

	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "cannot declare issue templates") {
		t.Fatalf("ValidateDefinition() error = %v, want a template conflict error", err)
	}
}

// An auto node is issue-backed, so the task-scoped submission policies that
// need issues behind them are legal on it.
func TestValidateDefinitionAcceptsTaskScopedSubmissionOnAuto(t *testing.T) {
	for _, policy := range []string{"per_required_task", "fan_in"} {
		t.Run(policy, func(t *testing.T) {
			definition := validDefinition()
			node := &definition.Nodes[1]
			node.IssuePolicy = "auto"
			node.IssueTemplates = nil
			node.SubmissionSchema = &SubmissionSchema{Policy: policy}
			if err := ValidateDefinition(definition); err != nil {
				t.Fatalf("ValidateDefinition() error = %v, want accepted", err)
			}
		})
	}
}

func TestValidateDefinitionAcceptsImplicitParallelOutgoingEdges(t *testing.T) {
	activity := func(key string) NodeDefinition {
		return NodeDefinition{Key: key, Kind: "activity", Name: key, OwnerRole: "owner"}
	}
	for _, test := range []struct {
		name  string
		nodes []NodeDefinition
		edges []EdgeDefinition
	}{
		{
			name: "start",
			nodes: []NodeDefinition{
				{Key: "start", Kind: "start", Name: "Start"},
				activity("left"),
				activity("right"),
				activity("target"),
				{Key: "end", Kind: "end", Name: "End"},
			},
			edges: []EdgeDefinition{
				{From: "start", To: "left"},
				{From: "start", To: "right"},
				{From: "left", To: "target"},
				{From: "right", To: "target"},
				{From: "target", To: "end"},
			},
		},
		{
			name: "activity",
			nodes: []NodeDefinition{
				{Key: "start", Kind: "start", Name: "Start"},
				activity("source"),
				activity("left"),
				activity("right"),
				{Key: "end", Kind: "end", Name: "End"},
			},
			edges: []EdgeDefinition{
				{From: "start", To: "source"},
				{From: "source", To: "left"},
				{From: "source", To: "right"},
				{From: "left", To: "end"},
				{From: "right", To: "end"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition := validDefinition()
			definition.Nodes = test.nodes
			definition.Edges = test.edges
			definition.Acceptance = AcceptanceDefinition{}
			if err := ValidateDefinition(definition); err != nil {
				t.Fatalf("ValidateDefinition() error = %v", err)
			}
		})
	}
}

func TestValidateDefinitionRejectsCycle(t *testing.T) {
	definition := validDefinition()
	definition.Edges = append(definition.Edges, EdgeDefinition{From: "end", To: "implementation"})
	if err := ValidateDefinition(definition); err == nil || !strings.Contains(err.Error(), "acyclic") {
		t.Fatalf("ValidateDefinition() error = %v, want acyclic error", err)
	}
}

func TestValidateDefinitionRejectsUnreachableNode(t *testing.T) {
	definition := validDefinition()
	definition.Nodes = append(
		definition.Nodes,
		NodeDefinition{Key: "orphan", Kind: "activity", Name: "Orphan"},
		NodeDefinition{Key: "orphan_end", Kind: "end", Name: "Orphan end"},
	)
	definition.Edges = append(
		definition.Edges,
		EdgeDefinition{From: "orphan", To: "orphan_end"},
	)
	err := ValidateDefinition(definition)
	if err == nil ||
		(!strings.Contains(err.Error(), "incoming edge") &&
			!strings.Contains(err.Error(), "reachable")) {
		t.Fatalf("ValidateDefinition() error = %v, want unreachable node error", err)
	}
}

func TestValidateDefinitionRejectsDuplicateNodeKey(t *testing.T) {
	definition := validDefinition()
	definition.Nodes = append(
		definition.Nodes,
		NodeDefinition{Key: "implementation", Kind: "activity", Name: "Duplicate"},
	)
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "duplicate node key") {
		t.Fatalf("ValidateDefinition() error = %v, want duplicate node error", err)
	}
}

func TestValidateDefinitionRejectsInvalidRole(t *testing.T) {
	definition := validDefinition()
	definition.Roles[0].AllowedActorTypes = []string{"robot"}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "invalid actor type") {
		t.Fatalf("ValidateDefinition() error = %v, want invalid role error", err)
	}
}

func TestValidateDefinitionRejectsNestedExecutorFallback(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].Executor.Fallback = &ExecutorDefinition{
		Kind: "role", Role: "executor",
		Fallback: &ExecutorDefinition{Kind: "manual"},
	}
	if err := ValidateDefinition(definition); err == nil ||
		!strings.Contains(err.Error(), "own fallback") {
		t.Fatalf("ValidateDefinition() error = %v, want nested fallback error", err)
	}
}

func TestValidateDefinitionAcceptsExecutorWithoutFallback(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].Executor = &ExecutorDefinition{Kind: "role", Role: "executor"}
	if err := ValidateDefinition(definition); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}
}

func TestValidateDefinitionOwnerReviewerWithPinnedMember(t *testing.T) {
	// A node that pins a concrete member owner instead of using a role can
	// still carry an owner reviewer: the runtime resolves owners from node
	// participants, which the pinned actor creates.
	pinned := validDefinition()
	pinned.Nodes[1].OwnerRole = ""
	pinned.Nodes[1].Executor = &ExecutorDefinition{
		Kind: "actor", ActorType: "member",
		ActorID:  "33333333-3333-3333-3333-333333333333",
		Fallback: &ExecutorDefinition{Kind: "manual"},
	}
	pinned.Nodes[1].Reviewer = &ReviewerDefinition{Kind: "owner", Required: true}
	if err := ValidateDefinition(pinned); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}

	pinnedAgent := validDefinition()
	pinnedAgent.Nodes[1].OwnerRole = ""
	pinnedAgent.Nodes[1].Executor = &ExecutorDefinition{
		Kind: "actor", ActorType: "agent",
		ActorID:  "33333333-3333-3333-3333-333333333333",
		Fallback: &ExecutorDefinition{Kind: "manual"},
	}
	pinnedAgent.Nodes[1].Reviewer = &ReviewerDefinition{Kind: "owner", Required: true}
	if err := ValidateDefinition(pinnedAgent); err == nil {
		t.Fatal("ValidateDefinition() accepted an owner reviewer pinned to an agent")
	}

	noOwner := validDefinition()
	noOwner.Nodes[1].OwnerRole = ""
	noOwner.Nodes[1].Reviewer = &ReviewerDefinition{Kind: "owner", Required: true}
	if err := ValidateDefinition(noOwner); err == nil {
		t.Fatal("ValidateDefinition() accepted an owner reviewer without any owner")
	}
}

func TestValidateDefinitionCompletionAuthorizedRoles(t *testing.T) {
	valid := validDefinition()
	valid.Nodes[1].Completion.AuthorizedRoles = []string{"owner"}
	if err := ValidateDefinition(valid); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}

	unknown := validDefinition()
	unknown.Nodes[1].Completion.AuthorizedRoles = []string{"pm"}
	if err := ValidateDefinition(unknown); err == nil ||
		!strings.Contains(err.Error(), "authorized role") {
		t.Fatalf("ValidateDefinition() error = %v, want authorized role error", err)
	}
}

func TestValidateDefinitionNodeActions(t *testing.T) {
	valid := validDefinition()
	valid.Nodes[1].OnEnter = []NodeActionDefinition{{
		Kind: "set_host_status", Status: "in_progress",
	}}
	valid.Nodes[1].OnComplete = []NodeActionDefinition{{
		Kind: "set_host_status", Status: "in_review",
	}}
	if err := ValidateDefinition(valid); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}

	badKind := validDefinition()
	badKind.Nodes[1].OnComplete = []NodeActionDefinition{{Kind: "launch_missiles"}}
	if err := ValidateDefinition(badKind); err == nil ||
		!strings.Contains(err.Error(), "action") {
		t.Fatalf("ValidateDefinition() error = %v, want action kind error", err)
	}

	badStatus := validDefinition()
	badStatus.Nodes[1].OnComplete = []NodeActionDefinition{{
		Kind: "set_host_status", Status: "shipped",
	}}
	if err := ValidateDefinition(badStatus); err == nil ||
		!strings.Contains(err.Error(), "status") {
		t.Fatalf("ValidateDefinition() error = %v, want status error", err)
	}

	onControl := validDefinition()
	onControl.Nodes[0].OnEnter = []NodeActionDefinition{{
		Kind: "set_host_status", Status: "todo",
	}}
	if err := ValidateDefinition(onControl); err == nil {
		t.Fatal("ValidateDefinition() accepted actions on a control node")
	}
}

func TestValidateDefinitionRejectsInvalidCompletionMode(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].Completion.Mode = "on_demand"
	if err := ValidateDefinition(definition); err == nil ||
		!strings.Contains(err.Error(), "completion mode") {
		t.Fatalf("ValidateDefinition() error = %v, want completion mode error", err)
	}
}

func TestRequiresManualCompletionSupportsExplicitModeWithConditions(t *testing.T) {
	node := validDefinition().Nodes[1]
	node.Completion.Mode = "manual"
	node.SubmissionSchema = &SubmissionSchema{Policy: "single"}
	node.Reviewer = &ReviewerDefinition{Kind: "role", Role: "owner", Required: true}
	if !RequiresManualCompletion(node) {
		t.Fatal("explicit manual mode must remain manual with completion conditions")
	}

	node.Completion.Mode = "automatic"
	node.SubmissionSchema = nil
	node.Reviewer = nil
	node.IssueTemplates = nil
	if RequiresManualCompletion(node) {
		t.Fatal("explicit automatic mode must override the legacy manual heuristic")
	}
}

func TestNormalizeAuthoringDefinitionUnifiesLegacySignOffGates(t *testing.T) {
	definition := validDefinition()
	node := &definition.Nodes[1]
	node.Completion.Mode = "manual"
	node.Reviewer = &ReviewerDefinition{Kind: "owner", Required: true}

	normalized, err := NormalizeAuthoringDefinition(definition)
	if err != nil {
		t.Fatalf("NormalizeAuthoringDefinition() error = %v", err)
	}
	if normalized.Acceptance != (AcceptanceDefinition{}) {
		t.Fatalf("acceptance = %#v, want removed", normalized.Acceptance)
	}
	got := normalized.Nodes[1]
	if got.Completion.Mode != "automatic" {
		t.Fatalf("completion mode = %q, want automatic", got.Completion.Mode)
	}
	if got.Reviewer == nil || got.Reviewer.Kind != "role" ||
		got.Reviewer.Role != "owner" || !got.Reviewer.Required {
		t.Fatalf("reviewer = %#v, want required owner role reviewer", got.Reviewer)
	}
}

func TestNormalizeAuthoringDefinitionMovesAcceptanceToTerminalActivities(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].Reviewer = nil
	definition.Nodes[1].Completion.Mode = "automatic"

	normalized, err := NormalizeAuthoringDefinition(definition)
	if err != nil {
		t.Fatalf("NormalizeAuthoringDefinition() error = %v", err)
	}
	got := normalized.Nodes[1].Reviewer
	if got == nil || got.Kind != "role" || got.Role != "owner" || !got.Required {
		t.Fatalf("terminal reviewer = %#v, want required acceptance role", got)
	}
}

func TestNormalizeAuthoringDefinitionPrefersVisibleReviewerOverLegacyAcceptance(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].Reviewer = &ReviewerDefinition{
		Kind: "role", Role: "executor",
	}

	normalized, err := NormalizeAuthoringDefinition(definition)
	if err != nil {
		t.Fatalf("NormalizeAuthoringDefinition() error = %v", err)
	}
	if normalized.Acceptance != (AcceptanceDefinition{}) {
		t.Fatalf("acceptance = %#v, want removed", normalized.Acceptance)
	}
	got := normalized.Nodes[1].Reviewer
	if got == nil || got.Kind != "role" || got.Role != "executor" || !got.Required {
		t.Fatalf("reviewer = %#v, want required visible reviewer", got)
	}
}

func TestNormalizeAuthoringDefinitionMakesEveryReviewerRequired(t *testing.T) {
	definition := validDefinition()
	definition.Acceptance = AcceptanceDefinition{}
	definition.Nodes[1].Completion.Mode = "automatic"
	definition.Nodes[1].Reviewer = &ReviewerDefinition{
		Kind: "role", Role: "owner", Required: false,
	}

	normalized, err := NormalizeAuthoringDefinition(definition)
	if err != nil {
		t.Fatalf("NormalizeAuthoringDefinition() error = %v", err)
	}
	if normalized.Nodes[1].Reviewer == nil || !normalized.Nodes[1].Reviewer.Required {
		t.Fatalf("reviewer = %#v, want required reviewer", normalized.Nodes[1].Reviewer)
	}
}

func TestNormalizeAuthoringDefinitionRejectsUnassignedManualCompletion(t *testing.T) {
	definition := validDefinition()
	node := &definition.Nodes[1]
	node.OwnerRole = ""
	node.Completion.Mode = "manual"
	node.Reviewer = nil

	_, err := NormalizeAuthoringDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "manual completion requires a reviewer") {
		t.Fatalf("NormalizeAuthoringDefinition() error = %v", err)
	}
}

func TestNormalizeAuthoringDefinitionPreservesImplicitLegacyConfirmation(t *testing.T) {
	definition := validDefinition()
	node := &definition.Nodes[1]
	node.Completion = CompletionDefinition{}
	node.IssueTemplates = nil
	node.Reviewer = nil

	normalized, err := NormalizeAuthoringDefinition(definition)
	if err != nil {
		t.Fatalf("NormalizeAuthoringDefinition() error = %v", err)
	}
	got := normalized.Nodes[1]
	if got.Completion.Mode != "automatic" || got.Reviewer == nil ||
		got.Reviewer.Kind != "role" || got.Reviewer.Role != "owner" {
		t.Fatalf("normalized node = %#v, want legacy confirmation as role approval", got)
	}
}

func TestValidateDefinitionRequiresMemberOwnerForOwnerReviewer(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].OwnerRole = "executor"
	definition.Nodes[1].Reviewer = &ReviewerDefinition{Kind: "owner", Required: true}
	if err := ValidateDefinition(definition); err == nil || !strings.Contains(err.Error(), "only to member") {
		t.Fatalf("ValidateDefinition() error = %v, want member owner error", err)
	}
}

func TestValidateDefinitionRequiresOwnerReviewerRoleToBeMemberOnly(t *testing.T) {
	definition := validDefinition()
	definition.Roles[0].AllowedActorTypes = []string{"member", "agent"}
	definition.Nodes[1].Reviewer = &ReviewerDefinition{Kind: "owner", Required: true}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "only to member") {
		t.Fatalf("ValidateDefinition() error = %v, want member-only error", err)
	}
}

func TestValidateDefinitionRejectsDuplicateTaskKeyAcrossActivities(t *testing.T) {
	definition := validDefinition()
	definition.Nodes = append(definition.Nodes[:2], NodeDefinition{
		Key: "review", Kind: "activity", Name: "Review", OwnerRole: "owner",
		IssuePolicy: "fixed",
		IssueTemplates: []IssueTemplate{{
			Key: "implement", Title: "Review {{host.title}}", Required: true,
		}},
	}, definition.Nodes[2])
	definition.Edges = []EdgeDefinition{
		{From: "start", To: "implementation"},
		{From: "implementation", To: "review"},
		{From: "review", To: "end"},
	}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("ValidateDefinition() error = %v, want duplicate task error", err)
	}
}

func TestValidateDefinitionRejectsUnknownTitleTemplateVariable(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].IssueTemplates[0].Title = "Implement {{host.secret}}"
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "only supports") {
		t.Fatalf("ValidateDefinition() error = %v, want title template error", err)
	}
}

func TestValidateDefinitionRejectsInvalidSubmissionPolicy(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].SubmissionSchema = &SubmissionSchema{Policy: "arbitrary"}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "submission policy") {
		t.Fatalf("ValidateDefinition() error = %v, want submission policy error", err)
	}
}

func TestValidateDefinitionAcceptsStructuredAutoReviewer(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].Reviewer = &ReviewerDefinition{
		Kind: "auto",
		Condition: json.RawMessage(
			`{"source":"host_issue","key":"status","op":"eq","value":"done"}`,
		),
	}
	if err := ValidateDefinition(definition); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}
}

func TestValidateDefinitionRejectsDownstreamConditionReference(t *testing.T) {
	definition := validDefinition()
	definition.Nodes = append(definition.Nodes, NodeDefinition{
		Key: "verify", Kind: "activity", Name: "Verify", OwnerRole: "owner",
		Reviewer: &ReviewerDefinition{Kind: "role", Role: "owner"},
	})
	definition.Edges = []EdgeDefinition{
		{From: "start", To: "implementation"},
		{From: "implementation", To: "verify"},
		{From: "verify", To: "end"},
	}
	definition.Nodes[1].Reviewer = &ReviewerDefinition{
		Kind: "auto",
		Condition: json.RawMessage(
			`{"source":"node_verdict","node":"verify","key":"result","op":"eq","value":"pass"}`,
		),
	}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "must be upstream") {
		t.Fatalf("ValidateDefinition() error = %v, want upstream reference error", err)
	}
}

func TestValidateDefinitionRejectsInvalidIssueDefaults(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*IssueTemplate)
		want   string
	}{
		{
			name: "status",
			mutate: func(task *IssueTemplate) {
				task.InitialStatus = "ready_to_ship"
			},
			want: "invalid initial_status",
		},
		{
			name: "priority",
			mutate: func(task *IssueTemplate) {
				task.Priority = "critical"
			},
			want: "invalid priority",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := validDefinition()
			test.mutate(&definition.Nodes[1].IssueTemplates[0])
			err := ValidateDefinition(definition)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateDefinition() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateDefinitionRejectsNodeCountLimit(t *testing.T) {
	definition := validDefinition()
	definition.Nodes = make([]NodeDefinition, maxNodes+1)
	for i := range definition.Nodes {
		definition.Nodes[i] = NodeDefinition{
			Key:  fmt.Sprintf("node_%d", i),
			Kind: "activity",
			Name: "Node",
		}
	}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "nodes must contain") {
		t.Fatalf("ValidateDefinition() error = %v, want node limit error", err)
	}
}

func TestValidateDefinitionRejectsOversizeDefinition(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].Description = strings.Repeat("x", maxDefinitionBytes)
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("ValidateDefinition() error = %v, want size error", err)
	}
}

func TestParseDefinitionRejectsUnknownField(t *testing.T) {
	raw, err := json.Marshal(validDefinition())
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw[:len(raw)-1], []byte(`,"surprise":true}`)...)
	if _, err := ParseDefinition(raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ParseDefinition() error = %v, want unknown field error", err)
	}
}

func TestParseAuthoringDefinitionDropsRetiredAppliesTo(t *testing.T) {
	raw, err := json.Marshal(validDefinition())
	if err != nil {
		t.Fatal(err)
	}
	raw = append(
		raw[:len(raw)-1],
		[]byte(`,"applies_to":{"kind":"issue","type_key":"requirement"}}`)...,
	)

	definition, err := ParseAuthoringDefinition(raw)
	if err != nil {
		t.Fatalf("ParseAuthoringDefinition() error = %v", err)
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"applies_to"`)) {
		t.Fatalf("ParseAuthoringDefinition() retained applies_to: %s", encoded)
	}
}

func TestParseAuthoringDefinitionStillRejectsUnknownFields(t *testing.T) {
	raw, err := json.Marshal(validDefinition())
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw[:len(raw)-1], []byte(`,"surprise":true}`)...)

	if _, err := ParseAuthoringDefinition(raw); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ParseAuthoringDefinition() error = %v, want unknown field error", err)
	}
}

func TestValidateDefinitionAcceptsParallelGatewayDAG(t *testing.T) {
	definition := validDefinition()
	definition.Nodes = []NodeDefinition{
		{Key: "start", Kind: "start", Name: "Start"},
		{Key: "split", Kind: "parallel_split", Name: "Split"},
		{
			Key: "analysis", Kind: "activity", Name: "Analysis", OwnerRole: "owner",
		},
		{Key: "implementation", Kind: "activity", Name: "Implementation", OwnerRole: "owner"},
		{Key: "join", Kind: "parallel_join", JoinMode: "all", Name: "Join"},
		{Key: "route", Kind: "gateway", Name: "Route"},
		{Key: "review", Kind: "activity", Name: "Review", OwnerRole: "owner"},
		{Key: "direct", Kind: "activity", Name: "Direct", OwnerRole: "owner"},
		{Key: "review_end", Kind: "end", Name: "Review end"},
		{Key: "direct_end", Kind: "end", Name: "Direct end"},
	}
	definition.Nodes[2].Outputs = []OutputField{
		{Key: "path", Type: "enum", Values: []string{"review", "direct"}, Required: true},
	}
	definition.Edges = []EdgeDefinition{
		{From: "start", To: "split"},
		{From: "split", To: "analysis"},
		{From: "split", To: "implementation"},
		{From: "analysis", To: "join"},
		{From: "implementation", To: "join"},
		{From: "join", To: "route"},
		{From: "route", To: "review", FromCase: "c1"},
		{From: "route", To: "direct", FromCase: "else"},
		{From: "review", To: "review_end"},
		{From: "direct", To: "direct_end"},
	}
	for i, node := range definition.Nodes {
		if node.Key == "route" {
			definition.Nodes[i].Cases = []GatewayCase{
				{ID: "c1", Label: "评审", When: `path == "review"`},
				{ID: "else", Label: "直接结束"},
			}
		}
	}
	definition.Acceptance = AcceptanceDefinition{}
	if err := ValidateDefinition(definition); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}
	plan, err := BuildGraphPlan(definition)
	if err != nil {
		t.Fatalf("BuildGraphPlan() error = %v", err)
	}
	if len(plan.Successors("split")) != 2 || plan.Index["join"] <= plan.Index["analysis"] {
		t.Fatalf("unexpected graph plan: %#v", plan)
	}
}

func gatewayTestDefinition(cases []GatewayCase, edges []EdgeDefinition) Definition {
	definition := validDefinition()
	definition.Nodes = []NodeDefinition{
		{Key: "start", Kind: "start", Name: "Start"},
		{
			Key: "triage", Kind: "activity", Name: "Triage", OwnerRole: "owner",
			Outputs: []OutputField{
				{Key: "is_bug", Type: "bool", Required: true},
				{Key: "severity", Type: "enum", Values: []string{"low", "high"}},
			},
		},
		{Key: "route", Kind: "gateway", Name: "Route", Cases: cases},
		{Key: "left", Kind: "end", Name: "Left"},
		{Key: "right", Kind: "end", Name: "Right"},
	}
	base := []EdgeDefinition{
		{From: "start", To: "triage"},
		{From: "triage", To: "route"},
	}
	definition.Edges = append(base, edges...)
	definition.Acceptance = AcceptanceDefinition{}
	return definition
}

func TestValidateDefinitionGatewayCases(t *testing.T) {
	valid := gatewayTestDefinition(
		[]GatewayCase{
			{ID: "c1", Label: "非缺陷", When: `is_bug == false`},
			{ID: "else", Label: "继续"},
		},
		[]EdgeDefinition{
			{From: "route", To: "left", FromCase: "c1"},
			{From: "route", To: "right", FromCase: "else"},
		},
	)
	if err := ValidateDefinition(valid); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}

	tests := []struct {
		name  string
		cases []GatewayCase
		edges []EdgeDefinition
		want  string
	}{
		{
			name:  "missing else",
			cases: []GatewayCase{{ID: "c1", When: `is_bug == false`}, {ID: "c2", When: `is_bug == true`}},
			edges: []EdgeDefinition{
				{From: "route", To: "left", FromCase: "c1"},
				{From: "route", To: "right", FromCase: "c2"},
			},
			want: "fallback branch last",
		},
		{
			name:  "else not last",
			cases: []GatewayCase{{ID: "else"}, {ID: "c1", When: `is_bug == false`}},
			edges: []EdgeDefinition{
				{From: "route", To: "left", FromCase: "c1"},
				{From: "route", To: "right", FromCase: "else"},
			},
			want: "fallback branch last",
		},
		{
			name:  "else with condition",
			cases: []GatewayCase{{ID: "c1", When: `is_bug == false`}, {ID: "else", When: `is_bug == true`}},
			edges: []EdgeDefinition{
				{From: "route", To: "left", FromCase: "c1"},
				{From: "route", To: "right", FromCase: "else"},
			},
			want: "cannot carry a condition",
		},
		{
			name:  "unknown operator",
			cases: []GatewayCase{{ID: "c1", When: `is_bug matches "x"`}, {ID: "else"}},
			edges: []EdgeDefinition{
				{From: "route", To: "left", FromCase: "c1"},
				{From: "route", To: "right", FromCase: "else"},
			},
			want: "branch \"Left\"",
		},
		{
			name:  "enum literal outside declaration",
			cases: []GatewayCase{{ID: "c1", When: `severity == "urgent"`}, {ID: "else"}},
			edges: []EdgeDefinition{
				{From: "route", To: "left", FromCase: "c1"},
				{From: "route", To: "right", FromCase: "else"},
			},
			want: "not among enum values",
		},
		{
			name:  "case without edge",
			cases: []GatewayCase{{ID: "c1", When: `is_bug == false`}, {ID: "else"}},
			edges: []EdgeDefinition{
				{From: "route", To: "left", FromCase: "else"},
				{From: "route", To: "right", FromCase: "else"},
			},
			want: "exactly one outgoing edge",
		},
		{
			name:  "edge bound to unknown case",
			cases: []GatewayCase{{ID: "c1", When: `is_bug == false`}, {ID: "else"}},
			edges: []EdgeDefinition{
				{From: "route", To: "left", FromCase: "ghost"},
				{From: "route", To: "right", FromCase: "else"},
			},
			want: "unknown branch",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDefinition(gatewayTestDefinition(tc.cases, tc.edges))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateDefinition() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSelectGatewayCase(t *testing.T) {
	definition := gatewayTestDefinition(
		[]GatewayCase{
			{ID: "c1", Label: "非缺陷", When: `is_bug == false`},
			{ID: "c2", Label: "高危", When: `severity == "high"`},
			{ID: "else", Label: "继续"},
		},
		[]EdgeDefinition{
			{From: "route", To: "left", FromCase: "c1"},
			{From: "route", To: "right", FromCase: "c2"},
		},
	)
	// c2 and else share targets with c1/right to keep the graph small: give
	// else its own edge by reusing right for c2 above and left for else here
	// is not allowed (duplicate edge), so extend with a third end node.
	definition.Nodes = append(definition.Nodes, NodeDefinition{Key: "fallthrough", Kind: "end", Name: "Fallthrough"})
	definition.Edges = append(definition.Edges, EdgeDefinition{From: "route", To: "fallthrough", FromCase: "else"})
	plan, err := BuildGraphPlan(definition)
	if err != nil {
		t.Fatalf("BuildGraphPlan() error = %v", err)
	}
	gateway := plan.Nodes["route"]

	t.Run("first match wins in declared order", func(t *testing.T) {
		selected, target, err := SelectGatewayCase(gateway, plan, ExprPool{
			"triage": {"is_bug": false, "severity": "high"},
		})
		if err != nil || selected.ID != "c1" || target != "left" {
			t.Fatalf("selected %q -> %q, err %v", selected.ID, target, err)
		}
	})
	t.Run("later case fires when earlier misses", func(t *testing.T) {
		selected, target, err := SelectGatewayCase(gateway, plan, ExprPool{
			"triage": {"is_bug": true, "severity": "high"},
		})
		if err != nil || selected.ID != "c2" || target != "right" {
			t.Fatalf("selected %q -> %q, err %v", selected.ID, target, err)
		}
	})
	t.Run("no match falls to else", func(t *testing.T) {
		selected, target, err := SelectGatewayCase(gateway, plan, ExprPool{
			"triage": {"is_bug": true, "severity": "low"},
		})
		if err != nil || selected.ID != "else" || target != "fallthrough" {
			t.Fatalf("selected %q -> %q, err %v", selected.ID, target, err)
		}
	})
	t.Run("absent upstream fails closed to else", func(t *testing.T) {
		selected, target, err := SelectGatewayCase(gateway, plan, ExprPool{})
		if err != nil || selected.ID != "else" || target != "fallthrough" {
			t.Fatalf("selected %q -> %q, err %v", selected.ID, target, err)
		}
	})
}

func TestEvaluateConditionCompositionsAndMissingFailClosed(t *testing.T) {
	resolver := func(source, node, key string) (any, bool) {
		values := map[string]any{
			"host_issue.priority":          "high",
			"node_verdict.analysis.result": "pass",
		}
		lookup := source + "." + key
		if node != "" {
			lookup = source + "." + node + "." + key
		}
		value, ok := values[lookup]
		return value, ok
	}
	raw := json.RawMessage(`{
		"all":[
			{"source":"host_issue","key":"priority","op":"in","value":["high","urgent"]},
			{"not":{"source":"node_verdict","node":"analysis","key":"result","op":"eq","value":"fail"}}
		]
	}`)
	matches, err := EvaluateCondition(raw, resolver)
	if err != nil || !matches {
		t.Fatalf("EvaluateCondition() = %v, %v; want true", matches, err)
	}
	missing, err := EvaluateCondition(
		json.RawMessage(`{"source":"host_property","key":"missing","op":"neq","value":"x"}`),
		resolver,
	)
	if err != nil || missing {
		t.Fatalf("missing neq = %v, %v; want fail-closed false", missing, err)
	}
}

func TestValidateDefinitionAcceptsDirectActorExecutor(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].Executor = &ExecutorDefinition{
		Kind:      "actor",
		ActorType: "agent",
		ActorID:   "550e8400-e29b-41d4-a716-446655440000",
		Fallback:  &ExecutorDefinition{Kind: "manual"},
	}
	if err := ValidateDefinition(definition); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}
}

func TestValidateDefinitionRejectsInvalidExecutor(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Definition)
		want   string
	}{
		{
			name: "actor type",
			mutate: func(definition *Definition) {
				definition.Nodes[1].Executor = &ExecutorDefinition{
					Kind:      "actor",
					ActorType: "robot",
					ActorID:   "550e8400-e29b-41d4-a716-446655440000",
				}
			},
			want: "invalid actor type",
		},
		{
			name: "actor id",
			mutate: func(definition *Definition) {
				definition.Nodes[1].Executor = &ExecutorDefinition{
					Kind:      "actor",
					ActorType: "agent",
					ActorID:   "not-a-uuid",
				}
			},
			want: "invalid actor id",
		},
		{
			name: "unknown kind",
			mutate: func(definition *Definition) {
				definition.Nodes[1].Executor = &ExecutorDefinition{Kind: "whoever"}
			},
			want: "invalid executor kind",
		},
		{
			name: "unknown role",
			mutate: func(definition *Definition) {
				definition.Nodes[1].Executor = &ExecutorDefinition{
					Kind: "role", Role: "nobody",
				}
			},
			want: "unknown role",
		},
		{
			name: "fallback without executor",
			mutate: func(definition *Definition) {
				definition.Nodes[1].Executor = &ExecutorDefinition{
					Fallback: &ExecutorDefinition{Kind: "manual"},
				}
			},
			want: "fallback requires an executor",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := validDefinition()
			test.mutate(&definition)
			err := ValidateDefinition(definition)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateDefinition() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateDefinitionReviewer(t *testing.T) {
	tests := []struct {
		name     string
		reviewer ReviewerDefinition
		want     string
	}{
		{name: "role", reviewer: ReviewerDefinition{Kind: "role", Role: "owner"}},
		{
			name:     "api",
			reviewer: ReviewerDefinition{Kind: "api", APIURL: "https://review.example.com/check"},
		},
		{
			name:     "unknown role",
			reviewer: ReviewerDefinition{Kind: "role", Role: "nobody"},
			want:     "unknown role",
		},
		{
			name:     "api without url",
			reviewer: ReviewerDefinition{Kind: "api"},
			want:     "api reviewer requires api_url",
		},
		{
			name:     "api over http",
			reviewer: ReviewerDefinition{Kind: "api", APIURL: "http://review.example.com"},
			want:     "must use https",
		},
		{
			name:     "url on a non-api reviewer",
			reviewer: ReviewerDefinition{Kind: "role", Role: "owner", APIURL: "https://x.example.com"},
			want:     "only an api reviewer",
		},
		{
			name:     "auto without condition",
			reviewer: ReviewerDefinition{Kind: "auto"},
			want:     "auto reviewer requires a condition",
		},
		{
			name:     "unknown kind",
			reviewer: ReviewerDefinition{Kind: "vibes"},
			want:     "invalid reviewer kind",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := validDefinition()
			reviewer := test.reviewer
			definition.Nodes[1].Reviewer = &reviewer
			err := ValidateDefinition(definition)
			if test.want == "" {
				if err != nil {
					t.Fatalf("ValidateDefinition() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateDefinition() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAcceptanceReworkTargetsComeFromTheGraph(t *testing.T) {
	// Every activity is a legitimate destination, and it is the graph that
	// says so — nothing in the template lists them.
	plan, err := BuildGraphPlan(validDefinition())
	if err != nil {
		t.Fatalf("BuildGraphPlan() error = %v", err)
	}
	targets := plan.AcceptanceReworkTargets()
	if len(targets) != 1 || targets[0] != "implementation" {
		t.Fatalf("AcceptanceReworkTargets() = %v, want [implementation]", targets)
	}
}
