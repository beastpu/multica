package workflow

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// OutputField declares one structured field a node owes on delivery. The five
// types are closed on purpose: each maps to a fixed operator set, which is what
// lets a gateway condition editor offer dropdowns instead of free JSON.
type OutputField struct {
	Key      string   `json:"key"`
	Type     string   `json:"type"`
	Values   []string `json:"values,omitempty"`
	Required bool     `json:"required,omitempty"`
	Desc     string   `json:"desc,omitempty"`
	MaxLen   int      `json:"max_len,omitempty"`
}

// OutputFieldError is one structured validation failure. It carries enough for
// an agent to fix its own submission: which field, what went wrong, and what
// would have been accepted.
type OutputFieldError struct {
	Key      string   `json:"key"`
	Problem  string   `json:"problem"`
	Got      string   `json:"got,omitempty"`
	Expected []string `json:"expected,omitempty"`
}

const maxOutputFields = 32

// reservedOutputKeys are names the runtime claims for itself: "issue" is the
// host-issue namespace and the error pair is injected on failure branches.
var reservedOutputKeys = map[string]struct{}{
	"issue": {}, "error_type": {}, "error_message": {},
}

// ValidateOutputFields checks a node's outputs declaration itself, at template
// save time.
func ValidateOutputFields(fields []OutputField) error {
	if len(fields) > maxOutputFields {
		return fmt.Errorf("outputs exceed limit %d", maxOutputFields)
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if !validKey(field.Key) {
			return fmt.Errorf("invalid output key %q", field.Key)
		}
		if _, reserved := reservedOutputKeys[field.Key]; reserved {
			return fmt.Errorf("output key %q is reserved", field.Key)
		}
		if _, exists := seen[field.Key]; exists {
			return fmt.Errorf("duplicate output key %q", field.Key)
		}
		seen[field.Key] = struct{}{}
		switch field.Type {
		case "bool", "number", "string", "string[]":
			if len(field.Values) > 0 {
				return fmt.Errorf("output %q values are only valid for enum", field.Key)
			}
		case "enum":
			if len(field.Values) == 0 {
				return fmt.Errorf("output %q enum requires values", field.Key)
			}
			values := make(map[string]struct{}, len(field.Values))
			for _, value := range field.Values {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("output %q enum value cannot be empty", field.Key)
				}
				if _, exists := values[value]; exists {
					return fmt.Errorf("output %q duplicate enum value %q", field.Key, value)
				}
				values[value] = struct{}{}
			}
		default:
			return fmt.Errorf("output %q has unknown type %q", field.Key, field.Type)
		}
		if field.MaxLen != 0 && field.Type != "string" {
			return fmt.Errorf("output %q max_len is only valid for string", field.Key)
		}
		if field.MaxLen < 0 {
			return fmt.Errorf("output %q max_len cannot be negative", field.Key)
		}
	}
	return nil
}

// ValidateOutputValues checks a submitted value map against the declaration and
// returns the normalized values that may enter the variable pool. String inputs
// are coerced to the declared type so a CLI --set flag can carry every kind of
// field; undeclared keys are dropped rather than rejected — they are useful for
// debugging prompt drift but must not become unschema'd condition inputs.
func ValidateOutputValues(
	fields []OutputField,
	values map[string]any,
) (map[string]any, []OutputFieldError) {
	normalized := make(map[string]any, len(values))
	errs := make([]OutputFieldError, 0)
	for _, field := range fields {
		raw, present := values[field.Key]
		if !present || raw == nil {
			if field.Required {
				errs = append(errs, OutputFieldError{
					Key: field.Key, Problem: "missing_required",
				})
			}
			continue
		}
		value, fieldError := normalizeOutputValue(field, raw)
		if fieldError != nil {
			errs = append(errs, *fieldError)
			continue
		}
		normalized[field.Key] = value
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return normalized, errs
}

func normalizeOutputValue(field OutputField, raw any) (any, *OutputFieldError) {
	invalid := func(problem string, expected ...string) *OutputFieldError {
		return &OutputFieldError{
			Key: field.Key, Problem: problem,
			Got: fmt.Sprintf("%v", raw), Expected: expected,
		}
	}
	switch field.Type {
	case "bool":
		switch value := raw.(type) {
		case bool:
			return value, nil
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(value))
			if err != nil {
				return nil, invalid("invalid_type", "true", "false")
			}
			return parsed, nil
		}
		return nil, invalid("invalid_type", "true", "false")
	case "number":
		switch value := raw.(type) {
		case float64:
			return value, nil
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				return nil, invalid("invalid_type", "a number")
			}
			return parsed, nil
		}
		return nil, invalid("invalid_type", "a number")
	case "enum":
		value, ok := raw.(string)
		if !ok || !slices.Contains(field.Values, value) {
			return nil, invalid("invalid_enum", field.Values...)
		}
		return value, nil
	case "string":
		value, ok := raw.(string)
		if !ok {
			return nil, invalid("invalid_type", "a string")
		}
		if field.MaxLen > 0 && len([]rune(value)) > field.MaxLen {
			return nil, invalid("too_long", strconv.Itoa(field.MaxLen))
		}
		return value, nil
	case "string[]":
		switch value := raw.(type) {
		case []any:
			items := make([]any, 0, len(value))
			for _, item := range value {
				text, ok := item.(string)
				if !ok {
					return nil, invalid("invalid_type", "an array of strings")
				}
				items = append(items, text)
			}
			return items, nil
		case string:
			var items []string
			if err := json.Unmarshal([]byte(value), &items); err != nil {
				return nil, invalid("invalid_type", `a JSON array like ["a","b"]`)
			}
			result := make([]any, len(items))
			for i, item := range items {
				result[i] = item
			}
			return result, nil
		}
		return nil, invalid("invalid_type", "an array of strings")
	}
	return nil, invalid("invalid_type")
}

// MissingRequiredOutputs lists the required fields a delivery does not carry,
// in declaration order. Values are not re-checked here: anything stored has
// already been through ValidateOutputValues at submit time.
func MissingRequiredOutputs(fields []OutputField, values map[string]any) []string {
	missing := make([]string, 0)
	for _, field := range fields {
		if !field.Required {
			continue
		}
		if value, present := values[field.Key]; !present || value == nil {
			missing = append(missing, field.Key)
		}
	}
	return missing
}

// OutputFieldByKey returns the declaration for key, if any.
func OutputFieldByKey(fields []OutputField, key string) (OutputField, bool) {
	for _, field := range fields {
		if field.Key == key {
			return field, true
		}
	}
	return OutputField{}, false
}
