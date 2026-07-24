package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func validDefinition() Definition {
	return Definition{
		SchemaVersion: DefinitionSchemaVersion,
		Name:          "Delivery",
		AppliesTo:     AppliesTo{Kind: "issue"},
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
				Executor: ExecutorDefinition{Strategies: []ExecutorStrategy{
					{Kind: "fixed_role", Role: "executor"},
					{Kind: "manual"},
				}},
				IssueTemplates: []IssueTemplate{{Key: "implement", Title: "Implement {{host.title}}", Required: true}},
			},
			{Key: "acceptance", Kind: "activity", ActivityMode: "acceptance", Name: "Acceptance", OwnerRole: "owner"},
			{Key: "end", Kind: "end", Name: "End"},
		},
		Edges: []EdgeDefinition{
			{From: "start", To: "implementation"},
			{From: "implementation", To: "acceptance"},
			{From: "acceptance", To: "end"},
		},
		Acceptance: AcceptanceDefinition{
			Policy:        "member",
			ApproverRole:  "owner",
			NodeKey:       "acceptance",
			ReworkTargets: []string{"implementation"},
		},
	}
}

func TestValidateDefinition(t *testing.T) {
	if err := ValidateDefinition(validDefinition()); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}
}

func TestValidateDefinitionRejectsCycle(t *testing.T) {
	definition := validDefinition()
	definition.Edges = append(definition.Edges, EdgeDefinition{From: "acceptance", To: "implementation"})
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

func TestValidateDefinitionRequiresVisibleAcceptanceActivity(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[2].ActivityMode = ""
	if err := ValidateDefinition(definition); err == nil || !strings.Contains(err.Error(), "visible acceptance activity") {
		t.Fatalf("ValidateDefinition() error = %v, want visible acceptance error", err)
	}
}

func TestValidateDefinitionRequiresExecutorFallback(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].Executor.Strategies = []ExecutorStrategy{{Kind: "fixed_role", Role: "executor"}}
	if err := ValidateDefinition(definition); err == nil || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("ValidateDefinition() error = %v, want fallback error", err)
	}
}

func TestValidateDefinitionRejectsInvalidCompletionPolicy(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].Completion.Confirmation = "agent_any"
	if err := ValidateDefinition(definition); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("ValidateDefinition() error = %v, want confirmation error", err)
	}
}

func TestValidateDefinitionRequiresMemberOwnerForOwnerConfirmation(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].OwnerRole = "executor"
	definition.Nodes[1].Completion.Confirmation = "owner_any"
	if err := ValidateDefinition(definition); err == nil || !strings.Contains(err.Error(), "only to member") {
		t.Fatalf("ValidateDefinition() error = %v, want member owner error", err)
	}
}

func TestValidateDefinitionRequiresOwnerConfirmationRoleToBeMemberOnly(t *testing.T) {
	definition := validDefinition()
	definition.Roles[0].AllowedActorTypes = []string{"member", "agent"}
	definition.Nodes[1].Completion.Confirmation = "owner_any"
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "only to member") {
		t.Fatalf("ValidateDefinition() error = %v, want member-only error", err)
	}
}

func TestValidateDefinitionRejectsDuplicateTaskKeyAcrossActivities(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[2].IssuePolicy = "fixed"
	definition.Nodes[2].IssueTemplates = []IssueTemplate{{
		Key: "implement", Title: "Accept {{host.title}}", Required: true,
	}}
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
	definition.Nodes[1].SubmissionSchema = &SubmissionSchema{
		Policy: "arbitrary",
		Fields: []SubmissionField{{
			Key: "summary", Name: "Summary", Type: "text", Required: true,
		}},
	}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "submission policy") {
		t.Fatalf("ValidateDefinition() error = %v, want submission policy error", err)
	}
}

func TestValidateDefinitionRejectsDuplicateSubmissionField(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].SubmissionSchema = &SubmissionSchema{
		Fields: []SubmissionField{
			{Key: "summary", Name: "Summary", Type: "text"},
			{Key: "summary", Name: "Summary again", Type: "text"},
		},
	}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "duplicate submission field") {
		t.Fatalf("ValidateDefinition() error = %v, want duplicate field error", err)
	}
}

func TestValidateDefinitionRejectsUnknownSubmissionFieldType(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].SubmissionSchema = &SubmissionSchema{
		Fields: []SubmissionField{{
			Key: "payload", Name: "Payload", Type: "object",
		}},
	}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "invalid type") {
		t.Fatalf("ValidateDefinition() error = %v, want field type error", err)
	}
}

func TestValidateDefinitionAcceptsStructuredDeterministicVerdict(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].SubmissionSchema = &SubmissionSchema{
		Policy: "single",
		Fields: []SubmissionField{{
			Key: "approved", Name: "Approved", Type: "boolean", Required: true,
		}},
	}
	definition.Nodes[1].Verdict = &VerdictDefinition{
		Evaluator: "deterministic", RequiredResult: "pass",
		Condition: json.RawMessage(
			`{"source":"node_submission","node":"implementation","key":"approved","op":"eq","value":true}`,
		),
	}
	if err := ValidateDefinition(definition); err != nil {
		t.Fatalf("ValidateDefinition() error = %v", err)
	}
}

func TestValidateDefinitionRejectsDownstreamConditionReference(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[2].SubmissionSchema = &SubmissionSchema{
		Fields: []SubmissionField{{
			Key: "approved", Name: "Approved", Type: "boolean", Required: true,
		}},
	}
	definition.Nodes[1].Verdict = &VerdictDefinition{
		Evaluator: "deterministic",
		Condition: json.RawMessage(
			`{"source":"node_submission","node":"acceptance","key":"approved","op":"eq","value":true}`,
		),
	}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "must be upstream") {
		t.Fatalf("ValidateDefinition() error = %v, want upstream reference error", err)
	}
}

func TestValidateDefinitionRejectsUnknownVerdictSubmissionField(t *testing.T) {
	definition := validDefinition()
	definition.Nodes[1].SubmissionSchema = &SubmissionSchema{
		Fields: []SubmissionField{{
			Key: "approved", Name: "Approved", Type: "boolean", Required: true,
		}},
	}
	definition.Nodes[1].Verdict = &VerdictDefinition{
		Evaluator: "deterministic",
		Condition: json.RawMessage(
			`{"source":"node_submission","node":"implementation","key":"missing","op":"eq","value":true}`,
		),
	}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ValidateDefinition() error = %v, want unknown submission field error", err)
	}
}

func TestValidateDefinitionRejectsUnreachableAcceptanceReworkTarget(t *testing.T) {
	definition := validDefinition()
	definition.Nodes = append(
		definition.Nodes,
		NodeDefinition{Key: "post_acceptance", Kind: "activity", Name: "Post acceptance"},
	)
	definition.Edges[2] = EdgeDefinition{From: "acceptance", To: "post_acceptance"}
	definition.Edges = append(
		definition.Edges,
		EdgeDefinition{From: "post_acceptance", To: "end"},
	)
	definition.Acceptance.ReworkTargets = []string{"post_acceptance"}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "must reach acceptance") {
		t.Fatalf("ValidateDefinition() error = %v, want rework reachability error", err)
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
			name: "assignee",
			mutate: func(task *IssueTemplate) {
				task.AssigneeRole = "unknown"
			},
			want: "unknown assignee role",
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

func TestValidateDefinitionAcceptsParallelGatewayDAG(t *testing.T) {
	definition := validDefinition()
	definition.Nodes = []NodeDefinition{
		{Key: "start", Kind: "start", Name: "Start"},
		{Key: "split", Kind: "parallel_split", Name: "Split"},
		{
			Key: "analysis", Kind: "activity", Name: "Analysis", OwnerRole: "owner",
			SubmissionSchema: &SubmissionSchema{Fields: []SubmissionField{{
				Key: "needs_review", Name: "Needs review", Type: "boolean", Required: true,
			}}},
		},
		{Key: "implementation", Kind: "activity", Name: "Implementation", OwnerRole: "owner"},
		{Key: "join", Kind: "parallel_join", JoinMode: "all", Name: "Join"},
		{Key: "route", Kind: "gateway", Name: "Route"},
		{Key: "review", Kind: "activity", Name: "Review", OwnerRole: "owner"},
		{Key: "direct", Kind: "activity", Name: "Direct", OwnerRole: "owner"},
		{Key: "review_end", Kind: "end", Name: "Review end"},
		{Key: "direct_end", Kind: "end", Name: "Direct end"},
	}
	definition.Edges = []EdgeDefinition{
		{From: "start", To: "split"},
		{From: "split", To: "analysis"},
		{From: "split", To: "implementation"},
		{From: "analysis", To: "join"},
		{From: "implementation", To: "join"},
		{From: "join", To: "route"},
		{
			From: "route", To: "review",
			Condition: json.RawMessage(`{
				"source":"node_submission",
				"node":"analysis",
				"key":"needs_review",
				"op":"eq",
				"value":true
			}`),
		},
		{From: "route", To: "direct", Default: true},
		{From: "review", To: "review_end"},
		{From: "direct", To: "direct_end"},
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

func TestNormalizeProposedTasks(t *testing.T) {
	tasks, err := NormalizeProposedTasks([]IssueTemplate{{
		Key:      "investigate_logs",
		Title:    "Investigate {{host.title}}",
		Required: true,
	}})
	if err != nil {
		t.Fatalf("NormalizeProposedTasks() error = %v", err)
	}
	if tasks[0].InitialStatus != "todo" || tasks[0].Priority != "none" {
		t.Fatalf("normalized task = %#v", tasks[0])
	}
}

func TestNormalizeProposedTasksRejectsDuplicateStableKey(t *testing.T) {
	_, err := NormalizeProposedTasks([]IssueTemplate{
		{Key: "same", Title: "First"},
		{Key: "same", Title: "Second"},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("NormalizeProposedTasks() error = %v, want duplicate key", err)
	}
}

func TestValidateDefinitionRejectsGatewayWithoutDefault(t *testing.T) {
	definition := validDefinition()
	definition.Nodes = []NodeDefinition{
		{Key: "start", Kind: "start", Name: "Start"},
		{Key: "route", Kind: "gateway", Name: "Route"},
		{Key: "left", Kind: "end", Name: "Left"},
		{Key: "right", Kind: "end", Name: "Right"},
	}
	definition.Edges = []EdgeDefinition{
		{From: "start", To: "route"},
		{
			From: "route", To: "left",
			Condition: json.RawMessage(`{"source":"host_issue","key":"priority","op":"eq","value":"high"}`),
		},
		{
			From: "route", To: "right",
			Condition: json.RawMessage(`{"source":"host_issue","key":"priority","op":"neq","value":"high"}`),
		},
	}
	definition.Acceptance = AcceptanceDefinition{}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "exactly one default") {
		t.Fatalf("ValidateDefinition() error = %v, want default error", err)
	}
}

func TestValidateDefinitionRejectsUnknownConditionOperator(t *testing.T) {
	definition := validDefinition()
	definition.Nodes = []NodeDefinition{
		{Key: "start", Kind: "start", Name: "Start"},
		{Key: "route", Kind: "gateway", Name: "Route"},
		{Key: "left", Kind: "end", Name: "Left"},
		{Key: "right", Kind: "end", Name: "Right"},
	}
	definition.Edges = []EdgeDefinition{
		{From: "start", To: "route"},
		{
			From: "route", To: "left",
			Condition: json.RawMessage(`{"source":"host_issue","key":"priority","op":"matches","value":"high"}`),
		},
		{From: "route", To: "right", Default: true},
	}
	definition.Acceptance = AcceptanceDefinition{}
	err := ValidateDefinition(definition)
	if err == nil || !strings.Contains(err.Error(), "unknown condition operator") {
		t.Fatalf("ValidateDefinition() error = %v, want operator error", err)
	}
}

func TestEvaluateConditionCompositionsAndMissingFailClosed(t *testing.T) {
	resolver := func(source, node, key string) (any, bool) {
		values := map[string]any{
			"host_issue.priority":           "high",
			"node_submission.analysis.risk": float64(8),
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
			{"not":{"source":"node_submission","node":"analysis","key":"risk","op":"lt","value":5}}
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
