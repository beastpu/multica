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

// EdgeList flattens the outgoing adjacency back into a single edge slice, for
// helpers that walk the raw topology.
func (p GraphPlan) EdgeList() []EdgeDefinition {
	edges := make([]EdgeDefinition, 0)
	for _, list := range p.Outgoing {
		edges = append(edges, list...)
	}
	return edges
}

// GatewayModeFilter activates every matching case instead of only the first.
const GatewayModeFilter = "filter"

// ValidateReworkAttempt reports whether a node may be sent back again.
// currentAttempt is the attempt already on record; the rework about to happen
// would produce currentAttempt+1.
//
// Rework does not travel an edge — it rebuilds the node — so the graph's
// acyclic guarantee says nothing about how many times this can repeat. Without
// a cap a reviewer and an executor can hand work back indefinitely, and the run
// looks busy the whole time.
func ValidateReworkAttempt(node NodeDefinition, currentAttempt int) error {
	cap := node.Completion.MaxAttempts
	if cap <= 0 || currentAttempt < cap {
		return nil
	}
	return fmt.Errorf(
		"activity %q has used all %d attempts; a person has to decide instead of reworking again",
		displayName(node.Name, node.Key), cap,
	)
}

// GatewayRouting is a routing decision together with what it turned on.
type GatewayRouting struct {
	// Cases that won, in declared order. Exactly one unless mode is filter.
	Cases   []GatewayCase
	Targets []string
	// Per case id, whether it matched. Every conditional case is present,
	// including ones that ran after the winner in filter mode.
	Matched map[string]bool
	// The values every condition read, keyed by "node.field". A branch is
	// only reviewable if the data behind it is kept with it — read off the
	// submission later, a value shows what is true now, not what was true
	// when the branch was taken.
	Evidence map[string]any
}

// SelectGatewayCases routes a gateway against the variable pool. Cases evaluate
// in declared order. A switch gateway (the default) stops at the first match; a
// filter gateway takes all of them. Either way, no match at all falls through
// to the trailing else, alone.
//
// A parse error here means a stored template escaped validation — surfaced as
// an error rather than a silent else, because misrouting work is worse than
// halting it.
func SelectGatewayCases(
	gateway NodeDefinition,
	plan GraphPlan,
	pool ExprPool,
) (GatewayRouting, error) {
	targets := make(map[string]string, len(plan.Outgoing[gateway.Key]))
	for _, edge := range plan.Outgoing[gateway.Key] {
		targets[edge.FromCase] = edge.To
	}
	scope := GatewayExprScope(gateway.Key, plan.Nodes, plan.EdgeList())
	routing := GatewayRouting{
		Cases:    make([]GatewayCase, 0, 1),
		Targets:  make([]string, 0, 1),
		Matched:  map[string]bool{},
		Evidence: map[string]any{},
	}
	decided := false
	for _, gatewayCase := range gateway.Cases {
		if gatewayCase.ID == "else" {
			continue
		}
		expr, err := ParseExpr(gatewayCase.When, scope)
		if err != nil {
			return GatewayRouting{}, fmt.Errorf(
				"gateway %q case %q: %w", gateway.Key, gatewayCase.ID, err,
			)
		}
		// Collected for every case, not just the winner: "why not that one"
		// is as much of the answer as "why this one".
		for _, ref := range expr.ReferencedFields() {
			if value, ok := pool[ref.Node][ref.Key]; ok {
				routing.Evidence[ref.Path()] = value
			} else {
				routing.Evidence[ref.Path()] = nil
			}
		}
		hit, err := expr.Evaluate(pool)
		if err != nil {
			return GatewayRouting{}, fmt.Errorf(
				"gateway %q case %q: %w", gateway.Key, gatewayCase.ID, err,
			)
		}
		routing.Matched[gatewayCase.ID] = hit
		if !hit || decided {
			continue
		}
		routing.Cases = append(routing.Cases, gatewayCase)
		routing.Targets = append(routing.Targets, targets[gatewayCase.ID])
		if gateway.Mode != GatewayModeFilter {
			decided = true
		}
	}
	if len(routing.Cases) > 0 {
		return routing, nil
	}
	if len(gateway.Cases) == 0 {
		return GatewayRouting{}, fmt.Errorf("gateway %q has no cases", gateway.Key)
	}
	elseCase := gateway.Cases[len(gateway.Cases)-1]
	target, exists := targets[elseCase.ID]
	if elseCase.ID != "else" || !exists {
		return GatewayRouting{}, fmt.Errorf("gateway %q has no else path", gateway.Key)
	}
	routing.Cases = append(routing.Cases, elseCase)
	routing.Targets = append(routing.Targets, target)
	return routing, nil
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

// AcceptanceReworkTargets lists every activity an acceptance rejection may send
// the run back to. Acceptance judges the finished run, so that is every
// activity in the graph: validation already guarantees each one is reachable
// from start and reaches an end.
//
// Derived rather than configured. A template-maintained whitelist is a second
// copy of the graph that has to be updated alongside it, and the failure is
// silent — add a node, forget the list, and it simply cannot be rolled back to
// with no indication why.
func (p GraphPlan) AcceptanceReworkTargets() []string {
	targets := make([]string, 0, len(p.Ordered))
	for _, node := range p.Ordered {
		if node.Kind == "activity" {
			targets = append(targets, node.Key)
		}
	}
	return targets
}
