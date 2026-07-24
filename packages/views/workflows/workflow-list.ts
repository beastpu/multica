import type { WorkflowInstance } from "@multica/core/workflows";

export type WorkflowTab = "active" | "mine" | "completed" | "templates";

export function isWorkflowRunActionable(run: WorkflowInstance): boolean {
  return run.next_action !== "none" &&
    run.next_action !== "view_current_activity";
}

export function partitionWorkflowRuns(runs: WorkflowInstance[]): {
  actionable: WorkflowInstance[];
  healthy: WorkflowInstance[];
} {
  const actionable: WorkflowInstance[] = [];
  const healthy: WorkflowInstance[] = [];
  for (const run of runs) {
    if (isWorkflowRunActionable(run)) {
      actionable.push(run);
    } else {
      healthy.push(run);
    }
  }
  return { actionable, healthy };
}

export function workflowStatusForTab(
  tab: WorkflowTab,
  explicitStatus: string,
): string {
  if (explicitStatus) return explicitStatus;
  return tab === "completed" ? "terminal" : "active";
}

export function canManageWorkflowTemplates(
  role: string | null | undefined,
): boolean {
  return role === "owner" || role === "admin";
}
