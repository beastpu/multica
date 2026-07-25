import type {
  WorkflowDefinition,
  WorkflowNodeDefinition,
} from "@multica/core/workflows";

/**
 * Returns activity nodes in graph order, independent of the order in which
 * nodes were appended to the template definition.
 */
export function workflowPreviewActivities(
  definition: WorkflowDefinition,
): WorkflowNodeDefinition[] {
  const nodeByKey = new Map(
    definition.nodes.map((node) => [node.key, node]),
  );
  const sourceIndex = new Map(
    definition.nodes.map((node, index) => [node.key, index]),
  );
  const indegree = new Map(
    definition.nodes.map((node) => [node.key, 0]),
  );
  const outgoing = new Map(
    definition.nodes.map((node) => [node.key, [] as string[]]),
  );

  for (const edge of definition.edges) {
    if (!nodeByKey.has(edge.from) || !nodeByKey.has(edge.to)) continue;
    outgoing.get(edge.from)!.push(edge.to);
    indegree.set(edge.to, (indegree.get(edge.to) ?? 0) + 1);
  }

  const compareSourceOrder = (left: string, right: string) =>
    (sourceIndex.get(left) ?? 0) - (sourceIndex.get(right) ?? 0);
  const queue = definition.nodes
    .filter((node) => indegree.get(node.key) === 0)
    .map((node) => node.key)
    .sort(compareSourceOrder);
  const ordered: WorkflowNodeDefinition[] = [];
  const visited = new Set<string>();

  while (queue.length > 0) {
    const key = queue.shift()!;
    if (visited.has(key)) continue;
    visited.add(key);
    const node = nodeByKey.get(key);
    if (node?.kind === "activity") ordered.push(node);

    for (const target of outgoing.get(key) ?? []) {
      const remaining = (indegree.get(target) ?? 1) - 1;
      indegree.set(target, remaining);
      if (remaining === 0) {
        queue.push(target);
        queue.sort(compareSourceOrder);
      }
    }
  }

  // Keep malformed drafts inspectable instead of dropping cyclic or
  // disconnected activity nodes from the preview.
  for (const node of definition.nodes) {
    if (!visited.has(node.key) && node.kind === "activity") ordered.push(node);
  }

  return ordered;
}
