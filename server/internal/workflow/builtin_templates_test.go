package workflow

import (
	"slices"
	"strings"
	"testing"
)

func TestBuiltinTemplatesAreValid(t *testing.T) {
	templates := BuiltinTemplates()
	if len(templates) != 2 {
		t.Fatalf("BuiltinTemplates() returned %d templates, want 2", len(templates))
	}
	seen := map[string]struct{}{}
	for _, template := range templates {
		if template.Key == "" {
			t.Fatal("builtin template has empty key")
		}
		if _, exists := seen[template.Key]; exists {
			t.Fatalf("duplicate builtin template key %q", template.Key)
		}
		seen[template.Key] = struct{}{}
		if template.Description == "" {
			t.Fatalf("builtin template %q has empty description", template.Key)
		}
		definition, err := ParseDefinition(template.Definition)
		if err != nil {
			t.Fatalf("builtin template %q definition invalid: %v", template.Key, err)
		}
		if definition.Name != template.Name {
			t.Fatalf(
				"builtin template %q name mismatch: meta %q, definition %q",
				template.Key,
				template.Name,
				definition.Name,
			)
		}
		if definition.Acceptance != (AcceptanceDefinition{}) {
			t.Fatalf(
				"builtin template %q must not ship a hidden run acceptance, got %#v",
				template.Key,
				definition.Acceptance,
			)
		}
		for _, edge := range definition.Edges {
			if edge.To != "end" {
				continue
			}
			for _, node := range definition.Nodes {
				if node.Key == edge.From &&
					(node.Reviewer == nil || !node.Reviewer.Required) {
					t.Fatalf(
						"builtin template %q final activity %q requires a reviewer",
						template.Key, node.Key,
					)
				}
			}
		}
	}
	for _, key := range []string{"requirement_delivery", "bug_fix"} {
		if _, exists := seen[key]; !exists {
			t.Fatalf("builtin template %q is missing", key)
		}
	}
}

func TestFindBuiltinTemplate(t *testing.T) {
	template, ok := FindBuiltinTemplate("bug_fix")
	if !ok {
		t.Fatal("FindBuiltinTemplate(bug_fix) not found")
	}
	if template.Key != "bug_fix" {
		t.Fatalf("FindBuiltinTemplate(bug_fix) key = %q", template.Key)
	}
	if _, ok := FindBuiltinTemplate("unknown"); ok {
		t.Fatal("FindBuiltinTemplate(unknown) unexpectedly found")
	}
}

// The builtins are what a workspace copies to get going, so they are also what
// teaches the model. They shipped as straight lines: every node ran, no node
// reported a fact, and nothing routed on anything — a workspace starting from
// one would never meet the structured outputs or the branching the engine is
// built around. Each now declares what its deciding node concludes and turns
// that conclusion into a route.
func TestBuiltinTemplatesRouteOnDeclaredOutputs(t *testing.T) {
	for _, template := range BuiltinTemplates() {
		definition, err := ParseDefinition(template.Definition)
		if err != nil {
			t.Fatalf("builtin template %q definition invalid: %v", template.Key, err)
		}

		declared := map[string]map[string]OutputField{}
		gateways := 0
		for _, node := range definition.Nodes {
			if len(node.Outputs) > 0 {
				fields := map[string]OutputField{}
				for _, field := range node.Outputs {
					fields[field.Key] = field
				}
				declared[node.Key] = fields
			}
			if node.Kind == "gateway" {
				gateways++
				// An else case is what keeps an unmatched run from stalling on
				// the gateway; the engine generates one, but a builtin that
				// relies on that teaches the shape without the safety.
				hasElse := false
				for _, item := range node.Cases {
					if item.ID == "else" {
						hasElse = true
					}
				}
				if !hasElse {
					t.Fatalf(
						"builtin template %q gateway %q has no else case",
						template.Key, node.Key,
					)
				}
			}
		}
		if gateways == 0 {
			t.Fatalf("builtin template %q routes nothing", template.Key)
		}
		if len(declared) == 0 {
			t.Fatalf("builtin template %q declares no output fields", template.Key)
		}

		// Every condition must read a field some upstream node actually owes.
		// A condition on an undeclared field is not an error the engine
		// reports — it fails closed to else — so a typo here would ship as a
		// branch that silently never fires.
		for _, node := range definition.Nodes {
			for _, item := range node.Cases {
				if item.When == "" {
					continue
				}
				referenced := false
				for nodeKey, fields := range declared {
					for fieldKey := range fields {
						if strings.Contains(item.When, nodeKey+"."+fieldKey) {
							referenced = true
						}
					}
				}
				if !referenced {
					t.Fatalf(
						"builtin template %q case %q reads no declared output: %q",
						template.Key, item.ID, item.When,
					)
				}
			}
		}
	}
}

// A deciding node states its conclusion in the fields the flow routes on. The
// builtins also made it tick off a work item that represented no work. Triage
// is a judgement, not a task.
func TestBuiltinDecidingNodesAskForTheConclusionOnce(t *testing.T) {
	for _, template := range BuiltinTemplates() {
		definition, err := ParseDefinition(template.Definition)
		if err != nil {
			t.Fatalf("builtin template %q definition invalid: %v", template.Key, err)
		}
		for _, node := range definition.Nodes {
			if node.Kind != "activity" {
				continue
			}
			// A node that only decides has nothing to work on, so an issue it
			// must close is ceremony the decision cannot supply.
			decidesOnly := len(node.Outputs) > 0 && len(node.Artifacts) == 0
			if decidesOnly && node.IssuePolicy != "none" {
				t.Fatalf(
					"builtin template %q node %q only decides but issues work: %q",
					template.Key, node.Key, node.IssuePolicy,
				)
			}
		}
		// Every role that executes has to be able to be an agent, or the flow
		// a workspace copies is one only people can run.
		for _, node := range definition.Nodes {
			if node.Executor == nil || node.Executor.Kind != "role" {
				continue
			}
			for _, role := range definition.Roles {
				if role.Key != node.Executor.Role {
					continue
				}
				if !slices.Contains(role.AllowedActorTypes, "agent") {
					t.Fatalf(
						"builtin template %q executes %q as role %q, which no agent may hold",
						template.Key, node.Key, role.Key,
					)
				}
			}
		}
	}
}
