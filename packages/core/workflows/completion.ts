import type { WorkflowNodeDefinition } from "./types";

export type WorkflowCompletionMode = "automatic" | "manual";

/**
 * Resolves the explicit completion mode while preserving the behavior of
 * templates published before completion modes were stored.
 */
export function workflowCompletionMode(
  node: WorkflowNodeDefinition,
): WorkflowCompletionMode {
  if (node.kind !== "activity" || node.activity_mode === "acceptance") {
    return "automatic";
  }
  if (node.completion?.mode) return node.completion.mode;

  const completion = node.completion ?? {};
  const hasRequiredIssue = (node.issue_templates ?? []).some(
    (task) => task.required,
  );
  const hasConfiguredGate = Boolean(
    node.submission_schema ||
      node.verdict ||
      completion.submission_required === true ||
      (
        completion.verdict_required !== undefined &&
        completion.verdict_required !== "none"
      ) ||
      (
        completion.confirmation !== undefined &&
        completion.confirmation !== "none"
      ) ||
      hasRequiredIssue,
  );

  return hasConfiguredGate ? "automatic" : "manual";
}
