import type { Issue } from "@multica/core/types";
import type {
  WorkflowNodeInstance,
  WorkflowNodeTask,
} from "@multica/core/workflows";

export type WorkflowIssueScope = "current" | "all";

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

export function workflowIssuesForScope(
  issues: Issue[],
  tasks: WorkflowNodeTask[],
  selectedNodeId: string,
  scope: WorkflowIssueScope,
): Issue[] {
  if (scope === "all") return issues;
  const currentIssueIds = new Set(
    tasks
      .filter((task) =>
        task.workflow_node_instance_id === selectedNodeId
      )
      .flatMap((task) => task.issue_id ? [task.issue_id] : []),
  );
  return issues.filter((issue) => currentIssueIds.has(issue.id));
}
