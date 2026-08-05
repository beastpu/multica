package workflow

import (
	"encoding/json"
	"testing"
)

func fieldSpecs(t *testing.T, raw string) []OutputField {
	t.Helper()
	var fields []OutputField
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatalf("decode fields: %v", err)
	}
	return fields
}

func TestValidateOutputFields(t *testing.T) {
	valid := fieldSpecs(t, `[
		{"key": "is_bug", "type": "bool", "required": true, "desc": "real defect"},
		{"key": "category", "type": "enum", "values": ["bug", "duplicate"], "required": true},
		{"key": "severity", "type": "enum", "values": ["low", "high"]},
		{"key": "root_cause", "type": "string", "max_len": 500},
		{"key": "retries", "type": "number"},
		{"key": "areas", "type": "string[]"}
	]`)
	if err := ValidateOutputFields(valid); err != nil {
		t.Fatalf("valid fields rejected: %v", err)
	}

	cases := []struct {
		name string
		raw  string
	}{
		{"duplicate key", `[{"key":"a","type":"bool"},{"key":"a","type":"string"}]`},
		{"invalid key", `[{"key":"Is Bug","type":"bool"}]`},
		{"unknown type", `[{"key":"a","type":"json"}]`},
		{"enum without values", `[{"key":"a","type":"enum"}]`},
		{"enum duplicate value", `[{"key":"a","type":"enum","values":["x","x"]}]`},
		{"values outside enum", `[{"key":"a","type":"bool","values":["x"]}]`},
		{"reserved key issue", `[{"key":"issue","type":"bool"}]`},
		{"reserved key error_type", `[{"key":"error_type","type":"string"}]`},
		{"reserved key error_message", `[{"key":"error_message","type":"string"}]`},
		{"max_len outside string", `[{"key":"a","type":"bool","max_len":10}]`},
		{"negative max_len", `[{"key":"a","type":"string","max_len":-1}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateOutputFields(fieldSpecs(t, tc.raw)); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestValidateOutputValues(t *testing.T) {
	fields := fieldSpecs(t, `[
		{"key": "is_bug", "type": "bool", "required": true},
		{"key": "category", "type": "enum", "values": ["bug", "duplicate"], "required": true},
		{"key": "confidence", "type": "number"},
		{"key": "note", "type": "string", "max_len": 5},
		{"key": "areas", "type": "string[]"}
	]`)

	t.Run("valid typed values pass and normalize", func(t *testing.T) {
		values, errs := ValidateOutputValues(fields, map[string]any{
			"is_bug": true, "category": "bug", "confidence": 0.9,
			"note": "ok", "areas": []any{"db", "api"},
		})
		if len(errs) != 0 {
			t.Fatalf("unexpected errors: %+v", errs)
		}
		if values["is_bug"] != true || values["category"] != "bug" {
			t.Fatalf("values not normalized: %+v", values)
		}
	})

	t.Run("string coercion for CLI --set", func(t *testing.T) {
		values, errs := ValidateOutputValues(fields, map[string]any{
			"is_bug": "false", "category": "duplicate", "confidence": "0.7",
			"areas": `["db"]`,
		})
		if len(errs) != 0 {
			t.Fatalf("unexpected errors: %+v", errs)
		}
		if values["is_bug"] != false {
			t.Fatalf("bool not coerced: %+v", values["is_bug"])
		}
		if values["confidence"] != 0.7 {
			t.Fatalf("number not coerced: %+v", values["confidence"])
		}
	})

	t.Run("missing required", func(t *testing.T) {
		_, errs := ValidateOutputValues(fields, map[string]any{"is_bug": true})
		if !hasFieldError(errs, "category", "missing_required") {
			t.Fatalf("expected missing_required for category, got %+v", errs)
		}
	})

	t.Run("invalid enum reports expected values", func(t *testing.T) {
		_, errs := ValidateOutputValues(fields, map[string]any{
			"is_bug": true, "category": "urgent",
		})
		if !hasFieldError(errs, "category", "invalid_enum") {
			t.Fatalf("expected invalid_enum, got %+v", errs)
		}
		for _, fieldError := range errs {
			if fieldError.Key == "category" && len(fieldError.Expected) != 2 {
				t.Fatalf("expected enum values in error, got %+v", fieldError)
			}
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		_, errs := ValidateOutputValues(fields, map[string]any{
			"is_bug": true, "category": "bug", "confidence": "not a number",
		})
		if !hasFieldError(errs, "confidence", "invalid_type") {
			t.Fatalf("expected invalid_type, got %+v", errs)
		}
	})

	t.Run("string over max_len", func(t *testing.T) {
		_, errs := ValidateOutputValues(fields, map[string]any{
			"is_bug": true, "category": "bug", "note": "too long note",
		})
		if !hasFieldError(errs, "note", "too_long") {
			t.Fatalf("expected too_long, got %+v", errs)
		}
	})

	t.Run("undeclared field is dropped with warning entry", func(t *testing.T) {
		values, errs := ValidateOutputValues(fields, map[string]any{
			"is_bug": true, "category": "bug", "mystery": 1,
		})
		if len(errs) != 0 {
			t.Fatalf("undeclared field must not error: %+v", errs)
		}
		if _, exists := values["mystery"]; exists {
			t.Fatalf("undeclared field must not enter the pool")
		}
	})
}

func hasFieldError(errs []OutputFieldError, key, problem string) bool {
	for _, fieldError := range errs {
		if fieldError.Key == key && fieldError.Problem == problem {
			return true
		}
	}
	return false
}
