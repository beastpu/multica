package workflow

import "testing"

func exprScope() ExprScope {
	return ExprScope{
		Fields: map[string][]string{
			"is_bug":     {"triage"},
			"category":   {"triage"},
			"severity":   {"triage"},
			"confidence": {"triage"},
			"done":       {"fix", "verify"},
			"areas":      {"fix"},
		},
		FieldTypes: map[string]OutputField{
			"triage.is_bug":     {Key: "is_bug", Type: "bool"},
			"triage.category":   {Key: "category", Type: "enum", Values: []string{"bug", "duplicate"}},
			"triage.severity":   {Key: "severity", Type: "enum", Values: []string{"low", "high", "critical"}},
			"triage.confidence": {Key: "confidence", Type: "number"},
			"fix.done":          {Key: "done", Type: "bool"},
			"verify.done":       {Key: "done", Type: "bool"},
			"fix.areas":         {Key: "areas", Type: "string[]"},
		},
	}
}

func mustParseExpr(t *testing.T, source string) *Expr {
	t.Helper()
	expr, err := ParseExpr(source, exprScope())
	if err != nil {
		t.Fatalf("parse %q: %v", source, err)
	}
	return expr
}

func TestParseExprErrors(t *testing.T) {
	scope := exprScope()
	cases := []struct{ name, source string }{
		{"empty", ""},
		{"unknown field", `mystery == true`},
		{"unknown node", `ghost.done == true`},
		{"undeclared field on node", `triage.done == true`},
		{"ambiguous unqualified", `done == true`},
		{"enum value outside declaration", `category == "urgent"`},
		{"number op on bool", `is_bug > 1`},
		{"in without list", `severity in "high"`},
		{"trailing garbage", `is_bug == true extra`},
		{"unterminated string", `category == "bug`},
		{"lone identifier", `is_bug`},
		{"assignment typo", `is_bug = true`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseExpr(tc.source, scope); err == nil {
				t.Fatalf("expected parse error for %q", tc.source)
			}
		})
	}
}

func TestExprEvaluate(t *testing.T) {
	pool := ExprPool{
		"triage": {"is_bug": true, "category": "bug", "severity": "high", "confidence": 0.9},
		"fix":    {"done": true, "areas": []any{"db", "api"}},
	}

	trueCases := []string{
		`is_bug == true`,
		`category == "bug"`,
		`severity in ["high", "critical"]`,
		`confidence >= 0.9`,
		`fix.done == true && is_bug == true`,
		`category == "duplicate" || severity == "high"`,
		`!(category == "duplicate")`,
		`severity not in ["low"]`,
	}
	for _, source := range trueCases {
		if matched, err := mustParseExpr(t, source).Evaluate(pool); err != nil || !matched {
			t.Fatalf("%q expected true, got %v err=%v", source, matched, err)
		}
	}

	falseCases := []string{
		`is_bug == false`,
		`confidence < 0.5`,
		`category != "bug"`,
	}
	for _, source := range falseCases {
		if matched, err := mustParseExpr(t, source).Evaluate(pool); err != nil || matched {
			t.Fatalf("%q expected false, got %v err=%v", source, matched, err)
		}
	}
}

func TestExprFailClosed(t *testing.T) {
	// verify never delivered: every operator over its fields is false,
	// including negative ones — an absent value must not satisfy anything.
	pool := ExprPool{"triage": {"is_bug": true}}
	failClosed := []string{
		`verify.done == true`,
		`verify.done == false`,
		`verify.done != true`,
		`category != "bug"`,
		`severity not in ["low"]`,
	}
	for _, source := range failClosed {
		if matched, err := mustParseExpr(t, source).Evaluate(pool); err != nil || matched {
			t.Fatalf("%q must fail closed, got %v err=%v", source, matched, err)
		}
	}
}

func TestExprReferences(t *testing.T) {
	expr := mustParseExpr(t, `fix.done == true && severity in ["high"]`)
	references := expr.Nodes()
	if len(references) != 2 {
		t.Fatalf("expected 2 referenced nodes, got %v", references)
	}
	seen := map[string]bool{}
	for _, node := range references {
		seen[node] = true
	}
	if !seen["fix"] || !seen["triage"] {
		t.Fatalf("unexpected references %v", references)
	}
}
