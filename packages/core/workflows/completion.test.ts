import { describe, expect, it } from "vitest";
import type { WorkflowNodeDefinition } from "./types";
import { workflowCompletionMode } from "./completion";

function activity(
  overrides: Partial<WorkflowNodeDefinition> = {},
): WorkflowNodeDefinition {
  return {
    key: "work",
    kind: "activity",
    name: "Work",
    ...overrides,
  };
}

describe("workflowCompletionMode", () => {
  it("uses an explicit mode even when completion conditions are configured", () => {
    expect(workflowCompletionMode(activity({
      issue_templates: [{
        key: "implementation",
        title: "Implement",
        required: true,
      }],
      completion: {
        mode: "manual",
        required_issue_outcome: "done",
      },
    }))).toBe("manual");
  });

  it("preserves legacy implicit manual and automatic behavior", () => {
    expect(workflowCompletionMode(activity())).toBe("manual");
    expect(workflowCompletionMode(activity({
      submission_schema: { policy: "single" },
    }))).toBe("automatic");
    expect(workflowCompletionMode(activity({
      issue_templates: [{
        key: "implementation",
        title: "Implement",
        required: true,
      }],
    }))).toBe("automatic");
  });

  it("treats control nodes as automatic", () => {
    expect(workflowCompletionMode(activity({
      kind: "gateway",
    }))).toBe("automatic");
    expect(workflowCompletionMode({
      key: "end",
      kind: "end",
      name: "End",
      completion: { mode: "manual" },
    })).toBe("automatic");
  });
});
