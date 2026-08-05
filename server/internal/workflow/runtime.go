package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
)

// WaitingReason is persisted on a node instance so clients can explain why an
// activity has not advanced without reverse engineering runtime state.
type WaitingReason struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// SerialPlan is the executable Phase 2 subset of a published definition.
// Definitions remain capable of describing the wider DAG contract, but a
// runtime must reject unsupported control flow rather than execute it
// incorrectly.
type SerialPlan struct {
	Ordered []NodeDefinition
	Index   map[string]int
}

func BuildSerialPlan(definition Definition) (SerialPlan, error) {
	nodes := make(map[string]NodeDefinition, len(definition.Nodes))
	incoming := make(map[string]int, len(definition.Nodes))
	outgoing := make(map[string][]string, len(definition.Nodes))
	startKey := ""
	for _, node := range definition.Nodes {
		switch node.Kind {
		case "start", "activity", "end":
		default:
			return SerialPlan{}, fmt.Errorf("node %q uses %q control flow, which is not enabled in the serial runtime", node.Key, node.Kind)
		}
		nodes[node.Key] = node
		if node.Kind == "start" {
			startKey = node.Key
		}
	}
	for _, edge := range definition.Edges {
		if edge.FromCase != "" {
			return SerialPlan{}, fmt.Errorf("conditional edge %q -> %q is not enabled in the serial runtime", edge.From, edge.To)
		}
		outgoing[edge.From] = append(outgoing[edge.From], edge.To)
		incoming[edge.To]++
	}
	for key, targets := range outgoing {
		if len(targets) > 1 {
			return SerialPlan{}, fmt.Errorf("node %q has multiple outgoing edges; parallel and branching execution is not enabled", key)
		}
	}
	for key, count := range incoming {
		if count > 1 {
			return SerialPlan{}, fmt.Errorf("node %q has multiple incoming edges; join execution is not enabled", key)
		}
	}

	ordered := make([]NodeDefinition, 0, len(nodes))
	seen := make(map[string]struct{}, len(nodes))
	current := startKey
	for current != "" {
		if _, exists := seen[current]; exists {
			return SerialPlan{}, errors.New("serial runtime path contains a cycle")
		}
		node, exists := nodes[current]
		if !exists {
			return SerialPlan{}, fmt.Errorf("serial runtime path references unknown node %q", current)
		}
		seen[current] = struct{}{}
		ordered = append(ordered, node)
		targets := outgoing[current]
		if len(targets) == 0 {
			current = ""
		} else {
			current = targets[0]
		}
	}
	if len(seen) != len(nodes) {
		return SerialPlan{}, errors.New("serial runtime requires every node to be on the single start-to-end path")
	}
	if len(ordered) < 2 || ordered[len(ordered)-1].Kind != "end" {
		return SerialPlan{}, errors.New("serial runtime path must terminate at an end node")
	}

	index := make(map[string]int, len(ordered))
	for i, node := range ordered {
		index[node.Key] = i
	}
	return SerialPlan{Ordered: ordered, Index: index}, nil
}

func (p SerialPlan) Next(nodeKey string) (NodeDefinition, bool) {
	index, ok := p.Index[nodeKey]
	if !ok || index+1 >= len(p.Ordered) {
		return NodeDefinition{}, false
	}
	return p.Ordered[index+1], true
}

func (p SerialPlan) Node(nodeKey string) (NodeDefinition, bool) {
	index, ok := p.Index[nodeKey]
	if !ok {
		return NodeDefinition{}, false
	}
	return p.Ordered[index], true
}

func EncodeWaitingReasons(reasons []WaitingReason) []byte {
	if len(reasons) == 0 {
		return []byte("[]")
	}
	encoded, err := json.Marshal(reasons)
	if err != nil {
		return []byte(`[{"code":"runtime_error","message":"failed to encode waiting reasons"}]`)
	}
	return encoded
}
