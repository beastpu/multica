import type { WorkflowNodeDefinition } from "./types";

export type WorkflowCompletionMode = "automatic" | "manual";

/**
 * Resolves the explicit completion mode while preserving the behavior of
 * templates published before completion modes were stored.
 */
export function workflowCompletionMode(
  node: WorkflowNodeDefinition,
): WorkflowCompletionMode {
  if (node.kind !== "activity") {
    return "automatic";
  }
  if (node.completion?.mode) return node.completion.mode;

  const completion = node.completion ?? {};
  const hasRequiredIssue = (node.issue_templates ?? []).some(
    (task) => task.required,
  );
  const hasConfiguredGate = Boolean(
    node.submission_schema ||
      node.reviewer ||
      completion.submission_required === true ||
      hasRequiredIssue,
  );

  return hasConfiguredGate ? "automatic" : "manual";
}
