import type {
  WorkflowDefinition,
  WorkflowNodeDefinition,
} from "@multica/core/workflows";

export type WorkflowCanvasInsertKind = "activity" | "gateway";
export type WorkflowCanvasBranchKind = WorkflowCanvasInsertKind | "end";

export interface WorkflowCanvasEdgeTarget {
  from: string;
  to: string;
}

export function workflowNodeIsBoundary(node: WorkflowNodeDefinition) {
  return node.kind === "start" || node.kind === "end";
}

function outgoingByNode(definition: WorkflowDefinition) {
  const outgoing = new Map<string, string[]>();
  for (const edge of definition.edges) {
    const targets = outgoing.get(edge.from) ?? [];
    targets.push(edge.to);
    outgoing.set(edge.from, targets);
  }
  return outgoing;
}

export function workflowPathExists(
  definition: WorkflowDefinition,
  from: string,
  to: string,
) {
  const outgoing = outgoingByNode(definition);
  const queue = [from];
  const visited = new Set<string>();

  while (queue.length > 0) {
    const current = queue.shift()!;
    if (current === to) return true;
    if (visited.has(current)) continue;
    visited.add(current);
    queue.push(...(outgoing.get(current) ?? []));
  }
  return false;
}

export function workflowNodeCanAddOutgoing(
  definition: WorkflowDefinition,
  nodeKey: string,
) {
  const node = definition.nodes.find((candidate) => candidate.key === nodeKey);
  if (!node || node.kind === "end") return false;

  const outgoingCount = definition.edges.filter(
    (edge) => edge.from === nodeKey,
  ).length;
  if (outgoingCount === 0) return true;

  return node.kind === "start" ||
    node.kind === "activity" ||
    node.kind === "gateway" ||
    node.kind === "parallel_split";
}

export function workflowConnectionTargets(
  definition: WorkflowDefinition,
  sourceKey: string,
) {
  if (!workflowNodeCanAddOutgoing(definition, sourceKey)) return [];

  const existingTargets = new Set(
    definition.edges
      .filter((edge) => edge.from === sourceKey)
      .map((edge) => edge.to),
  );
  const incomingCount = new Map<string, number>();
  for (const edge of definition.edges) {
    incomingCount.set(edge.to, (incomingCount.get(edge.to) ?? 0) + 1);
  }

  return definition.nodes.filter((target) => {
    if (
      target.key === sourceKey ||
      target.kind === "start" ||
      existingTargets.has(target.key)
    ) {
      return false;
    }

    // An existing path would only add a redundant shortcut. A reverse path
    // would create a cycle. Both make the graph harder to understand and edit.
    if (
      workflowPathExists(definition, sourceKey, target.key) ||
      workflowPathExists(definition, target.key, sourceKey)
    ) {
      return false;
    }

    // Activity and End nodes are implicit wait-all merge points. Legacy
    // parallel_join nodes remain connectable for backwards compatibility.
    return target.kind === "activity" ||
      target.kind === "end" ||
      target.kind === "parallel_join" ||
      (incomingCount.get(target.key) ?? 0) === 0;
  });
}

export function nextWorkflowNodeKey(
  definition: WorkflowDefinition,
  prefix: string,
) {
  const existing = new Set(definition.nodes.map((node) => node.key));
  let suffix = 1;
  while (existing.has(`${prefix}_${suffix}`)) suffix += 1;
  return `${prefix}_${suffix}`;
}

function branchEdge(
  definition: WorkflowDefinition,
  from: string,
  to: string,
) {
  const source = definition.nodes.find((node) => node.key === from);
  const sourceEdges = definition.edges.filter((edge) => edge.from === from);
  return {
    from,
    to,
    ...(source?.kind === "gateway" &&
        !sourceEdges.some((candidate) => candidate.default === true)
      ? { default: true }
      : {}),
  };
}

export function insertWorkflowNodeOnEdge(
  definition: WorkflowDefinition,
  node: WorkflowNodeDefinition,
  target: WorkflowCanvasEdgeTarget,
) {
  if (definition.nodes.some((candidate) => candidate.key === node.key)) {
    return null;
  }

  let replaced = false;
  const edges = definition.edges.flatMap((edge) => {
    if (replaced || edge.from !== target.from || edge.to !== target.to) {
      return [edge];
    }
    replaced = true;
    return [
      { ...edge, to: node.key },
      { from: node.key, to: target.to },
    ];
  });
  if (!replaced) return null;

  return {
    ...definition,
    nodes: [...definition.nodes, node],
    edges,
  };
}

export function addWorkflowBranch(
  definition: WorkflowDefinition,
  node: WorkflowNodeDefinition,
  from: string,
) {
  if (
    definition.nodes.some((candidate) => candidate.key === node.key) ||
    !workflowNodeCanAddOutgoing(definition, from)
  ) {
    return null;
  }

  return {
    ...definition,
    nodes: [...definition.nodes, node],
    edges: [...definition.edges, branchEdge(definition, from, node.key)],
  };
}

export function connectWorkflowNodes(
  definition: WorkflowDefinition,
  target: WorkflowCanvasEdgeTarget,
) {
  if (
    !workflowConnectionTargets(definition, target.from).some(
      (candidate) => candidate.key === target.to,
    )
  ) {
    return null;
  }

  return {
    ...definition,
    edges: [
      ...definition.edges,
      branchEdge(definition, target.from, target.to),
    ],
  };
}

export function removeWorkflowEdge(
  definition: WorkflowDefinition,
  target: WorkflowCanvasEdgeTarget,
) {
  const edges = definition.edges.filter(
    (edge) => edge.from !== target.from || edge.to !== target.to,
  );
  if (edges.length === definition.edges.length) return null;
  return { ...definition, edges };
}

export function removeWorkflowNode(
  definition: WorkflowDefinition,
  nodeKey: string,
) {
  const node = definition.nodes.find((candidate) => candidate.key === nodeKey);
  if (!node || workflowNodeIsBoundary(node)) return null;

  const incoming = definition.edges.filter((edge) => edge.to === nodeKey);
  const outgoing = definition.edges.filter((edge) => edge.from === nodeKey);
  const remainingEdges = definition.edges.filter(
    (edge) => edge.from !== nodeKey && edge.to !== nodeKey,
  );

  if (incoming.length === 1 && outgoing.length === 1) {
    const from = incoming[0]!.from;
    const to = outgoing[0]!.to;
    const duplicate = remainingEdges.some(
      (edge) => edge.from === from && edge.to === to,
    );
    if (from !== to && !duplicate) {
      remainingEdges.push({ ...incoming[0]!, to });
    }
  }

  // Acceptance survives a node deletion untouched: it belongs to the run, and
  // its rework destinations come from the graph, which the deletion updates.
  return {
    ...definition,
    nodes: definition.nodes.filter((candidate) => candidate.key !== nodeKey),
    edges: remainingEdges,
  };
}
