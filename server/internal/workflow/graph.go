package workflow

import (
	"errors"
	"fmt"
	"sort"
)

// GraphPlan is the executable, deterministic topological projection of a
// published Workflow DAG.
type GraphPlan struct {
	Ordered  []NodeDefinition
	Index    map[string]int
	Nodes    map[string]NodeDefinition
	Incoming map[string][]EdgeDefinition
	Outgoing map[string][]EdgeDefinition
	StartKey string
}

func BuildGraphPlan(definition Definition) (GraphPlan, error) {
	if err := ValidateDefinition(definition); err != nil {
		return GraphPlan{}, err
	}
	plan := GraphPlan{
		Index:    make(map[string]int, len(definition.Nodes)),
		Nodes:    make(map[string]NodeDefinition, len(definition.Nodes)),
		Incoming: make(map[string][]EdgeDefinition, len(definition.Nodes)),
		Outgoing: make(map[string][]EdgeDefinition, len(definition.Nodes)),
	}
	displayOrder := make(map[string]int, len(definition.Nodes))
	for index, node := range definition.Nodes {
		plan.Nodes[node.Key] = node
		displayOrder[node.Key] = index
		if node.Kind == "start" {
			plan.StartKey = node.Key
		}
	}
	for _, edge := range definition.Edges {
		plan.Outgoing[edge.From] = append(plan.Outgoing[edge.From], edge)
		plan.Incoming[edge.To] = append(plan.Incoming[edge.To], edge)
	}
	for key := range plan.Outgoing {
		sort.SliceStable(plan.Outgoing[key], func(i, j int) bool {
			return displayOrder[plan.Outgoing[key][i].To] < displayOrder[plan.Outgoing[key][j].To]
		})
	}

	degree := make(map[string]int, len(plan.Nodes))
	ready := make([]string, 0, len(plan.Nodes))
	for key := range plan.Nodes {
		degree[key] = len(plan.Incoming[key])
		if degree[key] == 0 {
			ready = append(ready, key)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		return displayOrder[ready[i]] < displayOrder[ready[j]]
	})
	for len(ready) > 0 {
		key := ready[0]
		ready = ready[1:]
		plan.Ordered = append(plan.Ordered, plan.Nodes[key])
		for _, edge := range plan.Outgoing[key] {
			degree[edge.To]--
			if degree[edge.To] == 0 {
				ready = append(ready, edge.To)
				sort.Slice(ready, func(i, j int) bool {
					return displayOrder[ready[i]] < displayOrder[ready[j]]
				})
			}
		}
	}
	if len(plan.Ordered) != len(plan.Nodes) {
		return GraphPlan{}, errors.New("workflow graph must be acyclic")
	}
	for index, node := range plan.Ordered {
		plan.Index[node.Key] = index
	}
	return plan, nil
}

func (p GraphPlan) Node(nodeKey string) (NodeDefinition, bool) {
	node, ok := p.Nodes[nodeKey]
	return node, ok
}

func (p GraphPlan) Successors(nodeKey string) []EdgeDefinition {
	return append([]EdgeDefinition(nil), p.Outgoing[nodeKey]...)
}

func (p GraphPlan) Predecessors(nodeKey string) []EdgeDefinition {
	return append([]EdgeDefinition(nil), p.Incoming[nodeKey]...)
}

func (p GraphPlan) Descendants(nodeKey string) map[string]struct{} {
	result := map[string]struct{}{}
	queue := []string{nodeKey}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range p.Outgoing[current] {
			if _, exists := result[edge.To]; exists {
				continue
			}
			result[edge.To] = struct{}{}
			queue = append(queue, edge.To)
		}
	}
	delete(result, nodeKey)
	return result
}

func (p GraphPlan) IsAncestor(ancestor, descendant string) bool {
	_, ok := p.Descendants(ancestor)[descendant]
	return ok
}

// Next is retained for callers that explicitly require a single successor.
func (p GraphPlan) Next(nodeKey string) (NodeDefinition, bool) {
	edges := p.Outgoing[nodeKey]
	if len(edges) != 1 {
		return NodeDefinition{}, false
	}
	next, ok := p.Nodes[edges[0].To]
	return next, ok
}

func (p GraphPlan) ValidateReworkTarget(target, current string) error {
	node, ok := p.Node(target)
	if !ok || node.Kind != "activity" {
		return fmt.Errorf("rework target %q must reference an activity", target)
	}
	if !p.IsAncestor(target, current) {
		return fmt.Errorf("rework target %q must precede %q", target, current)
	}
	return nil
}
