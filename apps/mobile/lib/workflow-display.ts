import type { Issue } from "@multica/core/types";
import type {
  WorkflowDefinition,
  WorkflowNodeInstance,
  WorkflowNodeTask,
} from "@multica/core/workflows";

export type WorkflowIssueScope = "current" | "all";

/** Mirrors `latestWorkflowAttemptNodes` from the shared Workbench. */
export function latestWorkflowAttemptNodes(
  nodes: WorkflowNodeInstance[],
): WorkflowNodeInstance[] {
  const latest = new Map<string, WorkflowNodeInstance>();
  for (const node of nodes) {
    const current = latest.get(node.node_key);
    if (!current || node.attempt > current.attempt) {
      latest.set(node.node_key, node);
    }
  }
  return [...latest.values()].sort(
    (left, right) => left.display_order - right.display_order,
  );
}

/** Mirrors the shared current-node/all-workflow issue scope semantics. */
export function workflowIssuesForScope(
  issues: Issue[],
  tasks: WorkflowNodeTask[],
  selectedNodeId: string,
  scope: WorkflowIssueScope,
): Issue[] {
  if (scope === "all") return issues;
  const issueIds = new Set(
    tasks
      .filter((task) => task.workflow_node_instance_id === selectedNodeId)
      .flatMap((task) => (task.issue_id ? [task.issue_id] : [])),
  );
  return issues.filter((issue) => issueIds.has(issue.id));
}

export interface WorkflowCanvasColumn {
  rank: number;
  nodes: WorkflowNodeInstance[];
}

/**
 * Phone-sized read-only projection of the DAG. Nodes sharing a topological
 * rank are stacked in one column, preserving parallel branches while the
 * columns remain horizontally scrollable.
 */
export function workflowCanvasColumns(
  definition: WorkflowDefinition | undefined,
  instances: WorkflowNodeInstance[],
): WorkflowCanvasColumn[] {
  const latest = latestWorkflowAttemptNodes(instances);
  if (!definition) {
    return latest.map((node, rank) => ({ rank, nodes: [node] }));
  }

  const keys = new Set(definition.nodes.map((node) => node.key));
  const outgoing = new Map<string, string[]>();
  const indegree = new Map<string, number>();
  for (const key of keys) {
    outgoing.set(key, []);
    indegree.set(key, 0);
  }
  for (const edge of definition.edges) {
    if (!keys.has(edge.from) || !keys.has(edge.to)) continue;
    outgoing.get(edge.from)!.push(edge.to);
    indegree.set(edge.to, (indegree.get(edge.to) ?? 0) + 1);
  }

  const rankByKey = new Map<string, number>();
  const queue = [...keys].filter((key) => indegree.get(key) === 0);
  for (const key of queue) rankByKey.set(key, 0);
  for (let index = 0; index < queue.length; index += 1) {
    const key = queue[index]!;
    for (const next of outgoing.get(key) ?? []) {
      rankByKey.set(
        next,
        Math.max(rankByKey.get(next) ?? 0, (rankByKey.get(key) ?? 0) + 1),
      );
      const remaining = (indegree.get(next) ?? 1) - 1;
      indegree.set(next, remaining);
      if (remaining === 0) queue.push(next);
    }
  }

  let fallbackRank = Math.max(0, ...rankByKey.values()) + 1;
  for (const node of definition.nodes) {
    if (!rankByKey.has(node.key)) {
      rankByKey.set(node.key, fallbackRank);
      fallbackRank += 1;
    }
  }

  const byRank = new Map<number, WorkflowNodeInstance[]>();
  for (const node of latest) {
    const rank = rankByKey.get(node.node_key) ?? fallbackRank++;
    const bucket = byRank.get(rank) ?? [];
    bucket.push(node);
    byRank.set(rank, bucket);
  }
  return [...byRank.entries()]
    .sort(([left], [right]) => left - right)
    .map(([rank, nodes]) => ({ rank, nodes }));
}
