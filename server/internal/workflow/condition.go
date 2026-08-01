package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

const (
	maxConditionDepth = 16
	maxConditionTerms = 256
)

type conditionExpression struct {
	Source string            `json:"source,omitempty"`
	Node   string            `json:"node,omitempty"`
	Key    string            `json:"key,omitempty"`
	Op     string            `json:"op,omitempty"`
	Value  json.RawMessage   `json:"value,omitempty"`
	All    []json.RawMessage `json:"all,omitempty"`
	Any    []json.RawMessage `json:"any,omitempty"`
	Not    json.RawMessage   `json:"not,omitempty"`
}

type ConditionReference struct {
	Source string
	Node   string
}

// ConditionResolver returns a structured value for a supported condition data
// source. found=false is fail-closed for every operator, including neq.
type ConditionResolver func(source, node, key string) (value any, found bool)

func ValidateCondition(raw json.RawMessage, nodes map[string]NodeDefinition) error {
	terms := 0
	return walkCondition(raw, 0, &terms, func(expression conditionExpression) error {
		switch expression.Source {
		case "host_issue":
			switch expression.Key {
			case "title", "status", "priority", "assignee_type", "assignee_id", "project_id":
			default:
				return fmt.Errorf("unknown host_issue key %q", expression.Key)
			}
		case "host_property":
			if !validKey(expression.Key) {
				return fmt.Errorf("invalid host_property key %q", expression.Key)
			}
		case "node_choice":
			// The value is the key of an outgoing node, so validity is a graph
			// question, not a schema one: the referenced node must exist, and
			// the compared value must be somewhere it can actually branch to.
			node, ok := nodes[expression.Node]
			if !ok || node.Kind == "start" {
				return fmt.Errorf("node_choice references unknown node %q", expression.Node)
			}
			// A node carries exactly one choice, so the key is fixed rather
			// than free. Spelling it out keeps the condition readable and
			// stops a template inventing a field that will never be read.
			if expression.Key != "choice" {
				return fmt.Errorf(
					"node_choice key must be \"choice\", got %q", expression.Key,
				)
			}
		case "node_verdict":
			node, ok := nodes[expression.Node]
			if !ok || node.Kind != "activity" || node.Verdict == nil {
				return fmt.Errorf("node_verdict references unknown verdict node %q", expression.Node)
			}
			switch expression.Key {
			case "result", "reason", "confidence":
			default:
				return fmt.Errorf("unknown node_verdict key %q", expression.Key)
			}
		default:
			return fmt.Errorf("unknown condition source %q", expression.Source)
		}
		return nil
	})
}

func ConditionReferences(raw json.RawMessage) ([]ConditionReference, error) {
	terms := 0
	references := make([]ConditionReference, 0)
	seen := map[ConditionReference]struct{}{}
	err := walkCondition(raw, 0, &terms, func(expression conditionExpression) error {
		if expression.Node == "" {
			return nil
		}
		reference := ConditionReference{
			Source: expression.Source,
			Node:   expression.Node,
		}
		if _, exists := seen[reference]; !exists {
			seen[reference] = struct{}{}
			references = append(references, reference)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return references, nil
}

func EvaluateCondition(raw json.RawMessage, resolver ConditionResolver) (bool, error) {
	terms := 0
	return evaluateCondition(raw, resolver, 0, &terms)
}

func walkCondition(
	raw json.RawMessage,
	depth int,
	terms *int,
	validateLeaf func(conditionExpression) error,
) error {
	expression, form, err := decodeCondition(raw, depth, terms)
	if err != nil {
		return err
	}
	switch form {
	case "all":
		for _, child := range expression.All {
			if err := walkCondition(child, depth+1, terms, validateLeaf); err != nil {
				return err
			}
		}
	case "any":
		for _, child := range expression.Any {
			if err := walkCondition(child, depth+1, terms, validateLeaf); err != nil {
				return err
			}
		}
	case "not":
		return walkCondition(expression.Not, depth+1, terms, validateLeaf)
	case "leaf":
		return validateLeaf(expression)
	}
	return nil
}

func evaluateCondition(
	raw json.RawMessage,
	resolver ConditionResolver,
	depth int,
	terms *int,
) (bool, error) {
	expression, form, err := decodeCondition(raw, depth, terms)
	if err != nil {
		return false, err
	}
	switch form {
	case "all":
		for _, child := range expression.All {
			matches, err := evaluateCondition(child, resolver, depth+1, terms)
			if err != nil || !matches {
				return false, err
			}
		}
		return true, nil
	case "any":
		for _, child := range expression.Any {
			matches, err := evaluateCondition(child, resolver, depth+1, terms)
			if err != nil {
				return false, err
			}
			if matches {
				return true, nil
			}
		}
		return false, nil
	case "not":
		matches, err := evaluateCondition(expression.Not, resolver, depth+1, terms)
		return !matches, err
	}

	value, found := resolver(expression.Source, expression.Node, expression.Key)
	if !found {
		return false, nil
	}
	return compareConditionValue(value, expression.Op, expression.Value)
}

func decodeCondition(
	raw json.RawMessage,
	depth int,
	terms *int,
) (conditionExpression, string, error) {
	if depth > maxConditionDepth {
		return conditionExpression{}, "", errors.New("condition exceeds maximum nesting depth")
	}
	*terms++
	if *terms > maxConditionTerms {
		return conditionExpression{}, "", errors.New("condition exceeds maximum term count")
	}
	var expression conditionExpression
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&expression); err != nil {
		return conditionExpression{}, "", fmt.Errorf("invalid condition: %w", err)
	}
	if err := ensureConditionEOF(decoder); err != nil {
		return conditionExpression{}, "", err
	}

	forms := 0
	form := ""
	if expression.All != nil {
		forms++
		form = "all"
		if len(expression.All) == 0 {
			return conditionExpression{}, "", errors.New("condition all requires at least one term")
		}
	}
	if expression.Any != nil {
		forms++
		form = "any"
		if len(expression.Any) == 0 {
			return conditionExpression{}, "", errors.New("condition any requires at least one term")
		}
	}
	if hasJSONValue(expression.Not) {
		forms++
		form = "not"
	}
	if expression.Source != "" || expression.Key != "" || expression.Op != "" {
		forms++
		form = "leaf"
	}
	if forms != 1 {
		return conditionExpression{}, "", errors.New("condition must contain exactly one of all, any, not, or a leaf expression")
	}
	if form != "leaf" {
		if expression.Source != "" || expression.Node != "" || expression.Key != "" ||
			expression.Op != "" || hasJSONValue(expression.Value) {
			return conditionExpression{}, "", errors.New("condition group cannot contain leaf fields")
		}
		return expression, form, nil
	}
	if expression.Source == "" || expression.Key == "" || expression.Op == "" {
		return conditionExpression{}, "", errors.New("condition leaf requires source, key, and op")
	}
	switch expression.Op {
	case "is_set", "is_not_set":
		if hasJSONValue(expression.Value) {
			return conditionExpression{}, "", fmt.Errorf("operator %s does not accept value", expression.Op)
		}
	case "eq", "neq", "gt", "gte", "lt", "lte":
		if !hasJSONValue(expression.Value) {
			return conditionExpression{}, "", fmt.Errorf("operator %s requires value", expression.Op)
		}
	case "in", "not_in":
		if !hasJSONValue(expression.Value) {
			return conditionExpression{}, "", fmt.Errorf("operator %s requires value", expression.Op)
		}
		var values []any
		if err := json.Unmarshal(expression.Value, &values); err != nil {
			return conditionExpression{}, "", fmt.Errorf("operator %s requires an array value", expression.Op)
		}
	default:
		return conditionExpression{}, "", fmt.Errorf("unknown condition operator %q", expression.Op)
	}
	return expression, form, nil
}

func ensureConditionEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("condition contains trailing JSON")
		}
		return fmt.Errorf("decode trailing condition: %w", err)
	}
	return nil
}

func compareConditionValue(actual any, operator string, rawExpected json.RawMessage) (bool, error) {
	if operator == "is_set" || operator == "is_not_set" {
		isSet := actual != nil
		if text, ok := actual.(string); ok {
			isSet = strings.TrimSpace(text) != ""
		}
		if operator == "is_not_set" {
			return !isSet, nil
		}
		return isSet, nil
	}
	var expected any
	if err := json.Unmarshal(rawExpected, &expected); err != nil {
		return false, fmt.Errorf("decode condition value: %w", err)
	}
	switch operator {
	case "eq", "neq":
		equal := reflect.DeepEqual(actual, expected)
		if operator == "neq" {
			return !equal, nil
		}
		return equal, nil
	case "in", "not_in":
		values := expected.([]any)
		found := false
		for _, candidate := range values {
			found = found || reflect.DeepEqual(actual, candidate)
		}
		if operator == "not_in" {
			return !found, nil
		}
		return found, nil
	case "gt", "gte", "lt", "lte":
		left, leftOK := conditionNumber(actual)
		right, rightOK := conditionNumber(expected)
		if !leftOK || !rightOK {
			return false, nil
		}
		switch operator {
		case "gt":
			return left > right, nil
		case "gte":
			return left >= right, nil
		case "lt":
			return left < right, nil
		default:
			return left <= right, nil
		}
	default:
		return false, fmt.Errorf("unknown condition operator %q", operator)
	}
}

func conditionNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

// ChoiceBranch is one branch a node can pick, paired with where it leads.
type ChoiceBranch struct {
	Value  string
	Target string
}

// ChoiceDuty describes the routing decision a node owes. It is derived from
// the graph rather than declared on the node, so a template cannot claim a
// decision nothing reads, nor omit one something does.
type ChoiceDuty struct {
	GatewayName   string
	DefaultTarget string
	Options       []ChoiceBranch
}

// ChoiceBranchesForNode reports the branches nodeKey may pick, by finding the
// gateways whose outgoing conditions read this node's choice.
//
// Deriving this is what lets the executor be told there is a decision to make.
// Without it the instruction has to be written by hand into the node's issue
// description, and a template that forgets produces a run that silently takes
// the default every time — with nothing anywhere reporting that a branch was
// never considered.
//
// Only equality leaves yield a listed option: a richer condition still marks
// the node as deciding, but its accepted values cannot be enumerated honestly,
// and guessing them would be worse than saying nothing.
func ChoiceBranchesForNode(definition Definition, nodeKey string) (ChoiceDuty, bool) {
	names := make(map[string]string, len(definition.Nodes))
	for _, node := range definition.Nodes {
		names[node.Key] = node.Name
	}
	duty := ChoiceDuty{}
	found := false
	seen := map[string]struct{}{}
	for _, node := range definition.Nodes {
		if node.Kind != "gateway" {
			continue
		}
		gatewayReadsNode := false
		for _, edge := range definition.Edges {
			if edge.From != node.Key || edge.Default {
				continue
			}
			references, err := ConditionReferences(edge.Condition)
			if err != nil {
				continue
			}
			for _, reference := range references {
				if reference.Source == "node_choice" && reference.Node == nodeKey {
					gatewayReadsNode = true
				}
			}
		}
		if !gatewayReadsNode {
			continue
		}
		found = true
		if duty.GatewayName == "" {
			duty.GatewayName = node.Name
		}
		for _, edge := range definition.Edges {
			if edge.From != node.Key {
				continue
			}
			if edge.Default {
				if duty.DefaultTarget == "" {
					duty.DefaultTarget = names[edge.To]
				}
				continue
			}
			for _, value := range conditionChoiceValues(edge.Condition, nodeKey) {
				if _, exists := seen[value]; exists {
					continue
				}
				seen[value] = struct{}{}
				duty.Options = append(duty.Options, ChoiceBranch{
					Value: value, Target: names[edge.To],
				})
			}
		}
	}
	return duty, found
}

// conditionChoiceValues collects the values an equality test compares this
// node's choice against.
func conditionChoiceValues(raw json.RawMessage, nodeKey string) []string {
	values := make([]string, 0, 1)
	terms := 0
	_ = walkCondition(raw, 0, &terms, func(expression conditionExpression) error {
		if expression.Source != "node_choice" ||
			expression.Node != nodeKey ||
			expression.Op != "eq" {
			return nil
		}
		var value string
		if json.Unmarshal(expression.Value, &value) == nil && value != "" {
			values = append(values, value)
		}
		return nil
	})
	return values
}
