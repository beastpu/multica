import type {
  WorkflowDefinition,
  WorkflowGatewayCase,
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

function nextGatewayCaseId(cases: WorkflowGatewayCase[]) {
  const existing = new Set(cases.map((gatewayCase) => gatewayCase.id));
  let suffix = 1;
  while (existing.has(`c${suffix}`)) suffix += 1;
  return `c${suffix}`;
}

/**
 * Adds one outgoing branch from a gateway, keeping cases and edges in a
 * one-to-one mapping. The first branch binds the mandatory else case; every
 * later branch gets a fresh conditional case inserted before else, so the
 * declared order stays "conditions first, else last" by construction.
 */
function gatewayBranch(
  definition: WorkflowDefinition,
  gateway: WorkflowNodeDefinition,
  to: string,
): { nodes: WorkflowNodeDefinition[]; edge: WorkflowDefinition["edges"][number] } {
  const cases = gateway.cases ?? [];
  const boundCases = new Set(
    definition.edges
      .filter((edge) => edge.from === gateway.key)
      .map((edge) => edge.from_case),
  );
  let nextCases = cases;
  let caseId: string;
  if (!boundCases.has("else")) {
    caseId = "else";
    if (!cases.some((gatewayCase) => gatewayCase.id === "else")) {
      nextCases = [...cases, { id: "else" }];
    }
  } else {
    caseId = nextGatewayCaseId(cases);
    const elseIndex = cases.findIndex((gatewayCase) => gatewayCase.id === "else");
    const insertAt = elseIndex === -1 ? cases.length : elseIndex;
    nextCases = [
      ...cases.slice(0, insertAt),
      { id: caseId, when: "" },
      ...cases.slice(insertAt),
    ];
  }
  const nodes = nextCases === cases
    ? definition.nodes
    : definition.nodes.map((node) =>
      node.key === gateway.key ? { ...node, cases: nextCases } : node
    );
  return { nodes, edge: { from: gateway.key, to, from_case: caseId } };
}

function branchUpdate(
  definition: WorkflowDefinition,
  from: string,
  to: string,
): { nodes: WorkflowNodeDefinition[]; edge: WorkflowDefinition["edges"][number] } {
  const source = definition.nodes.find((node) => node.key === from);
  if (source?.kind === "gateway") {
    return gatewayBranch(definition, source, to);
  }
  return { nodes: definition.nodes, edge: { from, to } };
}

/** Updates one gateway case's label or condition in place. */
export function updateWorkflowGatewayCase(
  definition: WorkflowDefinition,
  gatewayKey: string,
  caseId: string,
  patch: Partial<Pick<WorkflowGatewayCase, "label" | "when">>,
) {
  const gateway = definition.nodes.find((node) => node.key === gatewayKey);
  if (!gateway?.cases?.some((gatewayCase) => gatewayCase.id === caseId)) {
    return null;
  }
  return {
    ...definition,
    nodes: definition.nodes.map((node) =>
      node.key === gatewayKey
        ? {
          ...node,
          cases: node.cases!.map((gatewayCase) =>
            gatewayCase.id === caseId
              // else never carries a condition; a stray when would be
              // rejected server-side, so it cannot be introduced here.
              ? {
                ...gatewayCase,
                ...patch,
                ...(caseId === "else" ? { when: undefined } : {}),
              }
              : gatewayCase
          ),
        }
        : node
    ),
  };
}

/**
 * Moves a conditional case one step up or down. Order is priority — first
 * match wins — and else stays pinned last.
 */
export function moveWorkflowGatewayCase(
  definition: WorkflowDefinition,
  gatewayKey: string,
  caseId: string,
  direction: "up" | "down",
) {
  const gateway = definition.nodes.find((node) => node.key === gatewayKey);
  const cases = gateway?.cases;
  if (!cases || caseId === "else") return null;
  const index = cases.findIndex((gatewayCase) => gatewayCase.id === caseId);
  const target = direction === "up" ? index - 1 : index + 1;
  if (
    index === -1 || target < 0 || target >= cases.length ||
    cases[target]!.id === "else"
  ) {
    return null;
  }
  const reordered = [...cases];
  [reordered[index], reordered[target]] = [reordered[target]!, reordered[index]!];
  return {
    ...definition,
    nodes: definition.nodes.map((node) =>
      node.key === gatewayKey ? { ...node, cases: reordered } : node
    ),
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

  // A gateway inserted on an edge starts with its pass-through bound to the
  // mandatory else case; the author then adds conditional branches.
  const inserted = node.kind === "gateway"
    ? { ...node, cases: [{ id: "else" }] }
    : node;
  let replaced = false;
  const edges = definition.edges.flatMap((edge) => {
    if (replaced || edge.from !== target.from || edge.to !== target.to) {
      return [edge];
    }
    replaced = true;
    return [
      { ...edge, to: inserted.key },
      {
        from: inserted.key,
        to: target.to,
        ...(inserted.kind === "gateway" ? { from_case: "else" } : {}),
      },
    ];
  });
  if (!replaced) return null;

  return {
    ...definition,
    nodes: [...definition.nodes, inserted],
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

  const branch = branchUpdate(definition, from, node.key);
  return {
    ...definition,
    nodes: [...branch.nodes, node],
    edges: [...definition.edges, branch.edge],
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

  const branch = branchUpdate(definition, target.from, target.to);
  return {
    ...definition,
    nodes: branch.nodes,
    edges: [...definition.edges, branch.edge],
  };
}

/**
 * Removes a conditional case together with its bound edge when the source is
 * a gateway; the else case itself survives an edge removal because the
 * gateway cannot exist without it — the next added branch re-binds it.
 */
export function removeWorkflowEdge(
  definition: WorkflowDefinition,
  target: WorkflowCanvasEdgeTarget,
) {
  const removed = definition.edges.find(
    (edge) => edge.from === target.from && edge.to === target.to,
  );
  if (!removed) return null;
  const edges = definition.edges.filter((edge) => edge !== removed);
  let nodes = definition.nodes;
  if (removed.from_case && removed.from_case !== "else") {
    nodes = nodes.map((node) =>
      node.key === removed.from
        ? {
          ...node,
          cases: node.cases?.filter(
            (gatewayCase) => gatewayCase.id !== removed.from_case,
          ),
        }
        : node
    );
  }
  return { ...definition, nodes, edges };
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

  let bridged = false;
  if (incoming.length === 1 && outgoing.length === 1) {
    const from = incoming[0]!.from;
    const to = outgoing[0]!.to;
    const duplicate = remainingEdges.some(
      (edge) => edge.from === from && edge.to === to,
    );
    if (from !== to && !duplicate) {
      remainingEdges.push({ ...incoming[0]!, to });
      bridged = true;
    }
  }

  // A gateway whose branch pointed at the removed node loses that edge, so
  // the conditional case bound to it goes too — else stays, it is structural.
  const orphanedCases = new Map<string, Set<string>>();
  if (!bridged) {
    for (const edge of incoming) {
      if (edge.from_case && edge.from_case !== "else") {
        const dropped = orphanedCases.get(edge.from) ?? new Set<string>();
        dropped.add(edge.from_case);
        orphanedCases.set(edge.from, dropped);
      }
    }
  }

  // Acceptance survives a node deletion untouched: it belongs to the run, and
  // its rework destinations come from the graph, which the deletion updates.
  return {
    ...definition,
    nodes: definition.nodes
      .filter((candidate) => candidate.key !== nodeKey)
      .map((candidate) => {
        const dropped = orphanedCases.get(candidate.key);
        if (!dropped) return candidate;
        return {
          ...candidate,
          cases: candidate.cases?.filter(
            (gatewayCase) => !dropped.has(gatewayCase.id),
          ),
        };
      }),
    edges: remainingEdges,
  };
}
