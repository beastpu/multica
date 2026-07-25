package workflow

import "testing"

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
		if definition.Acceptance.Policy != "member" {
			t.Fatalf(
				"builtin template %q must ship a member acceptance policy, got %q",
				template.Key,
				definition.Acceptance.Policy,
			)
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
