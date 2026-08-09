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

  // Mirrors RequiresManualCompletion in server/internal/workflow. An auto
  // policy synthesizes a required issue; a required output gates completion
  // the same way. completion.submission_required is retired and ignored.
  const hasRequiredIssue = node.issue_policy === "auto" ||
    (node.issue_templates ?? []).some((task) => task.required);
  const hasRequiredOutput = (node.outputs ?? []).some(
    (field) => field.required,
  );
  const hasConfiguredGate = Boolean(
    node.submission_schema ||
      node.reviewer ||
      hasRequiredOutput ||
      hasRequiredIssue,
  );

  return hasConfiguredGate ? "automatic" : "manual";
}
