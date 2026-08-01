/**
 * The branches a node may pick, derived from the gateways that read its choice.
 *
 * Mirrors `ChoiceBranchesForNode` on the server, which validates the same thing
 * on submission. Listing anything wider — every node, or everything reachable —
 * offers values the gateway has no condition for: picking one is accepted by
 * the server and then routes nowhere, landing on the default edge as if no
 * decision had been made.
 */
export type WorkflowGraphNode = {
  key: string;
  kind: string;
  name?: string;
};

export type WorkflowGraphEdge = {
  from: string;
  to: string;
  condition?: unknown;
  default?: boolean;
};

export type BranchChoice = {
  /** The value to submit. */
  key: string;
  /** Human name of the node this branch leads to. */
  target: string;
};

export type BranchChoiceDuty = {
  gatewayName: string;
  /** Where the run goes when no choice is submitted. */
  defaultTarget: string;
  options: BranchChoice[];
};

/** Collects the values an equality test compares this node's choice against. */
function choiceValues(condition: unknown, nodeKey: string): string[] {
  if (!condition || typeof condition !== "object") return [];
  const expression = condition as Record<string, unknown>;
  for (const group of ["all", "any"]) {
    const branch = expression[group];
    if (Array.isArray(branch)) {
      return branch.flatMap((item) => choiceValues(item, nodeKey));
    }
  }
  if (expression.not) return choiceValues(expression.not, nodeKey);
  if (
    expression.source === "node_choice" &&
    expression.node === nodeKey &&
    expression.op === "eq" &&
    typeof expression.value === "string" &&
    expression.value
  ) {
    return [expression.value];
  }
  return [];
}

export function branchChoiceDuty(
  nodes: WorkflowGraphNode[],
  edges: WorkflowGraphEdge[],
  nodeKey: string,
): BranchChoiceDuty | null {
  const names = new Map(nodes.map((node) => [node.key, node.name || node.key]));
  const options: BranchChoice[] = [];
  const seen = new Set<string>();
  let gatewayName = "";
  let defaultTarget = "";

  for (const gateway of nodes.filter((node) => node.kind === "gateway")) {
    const outgoing = edges.filter((edge) => edge.from === gateway.key);
    const readsNode = outgoing.some((edge) =>
      !edge.default && choiceValues(edge.condition, nodeKey).length > 0
    );
    if (!readsNode) continue;
    if (!gatewayName) gatewayName = names.get(gateway.key) ?? gateway.key;
    for (const edge of outgoing) {
      if (edge.default) {
        if (!defaultTarget) defaultTarget = names.get(edge.to) ?? edge.to;
        continue;
      }
      for (const value of choiceValues(edge.condition, nodeKey)) {
        if (seen.has(value)) continue;
        seen.add(value);
        options.push({ key: value, target: names.get(edge.to) ?? edge.to });
      }
    }
  }
  return options.length > 0
    ? { gatewayName, defaultTarget, options }
    : null;
}
