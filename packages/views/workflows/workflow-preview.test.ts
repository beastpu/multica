import { describe, expect, it } from "vitest";
import type { WorkflowDefinition } from "@multica/core/workflows";
import { workflowPreviewActivities } from "./workflow-preview";

function definition(
  nodes: WorkflowDefinition["nodes"],
  edges: WorkflowDefinition["edges"],
): WorkflowDefinition {
  return {
    schema_version: 1,
    name: "Preview",
    applies_to: { kind: "issue" },
    roles: [],
    nodes,
    edges,
    acceptance: {},
  };
}

describe("workflowPreviewActivities", () => {
  it("uses graph topology instead of node creation order", () => {
    const workflow = definition(
      [
        { key: "start", kind: "start", name: "Start" },
        { key: "acceptance", kind: "activity", name: "Acceptance" },
        { key: "end", kind: "end", name: "End" },
        { key: "analysis", kind: "activity", name: "Analysis" },
        { key: "design", kind: "activity", name: "Design" },
      ],
      [
        { from: "start", to: "analysis" },
        { from: "analysis", to: "design" },
        { from: "design", to: "acceptance" },
        { from: "acceptance", to: "end" },
      ],
    );

    expect(
      workflowPreviewActivities(workflow).map((node) => node.name),
    ).toEqual(["Analysis", "Design", "Acceptance"]);
  });

  it("keeps parallel activities deterministic and preserves malformed nodes", () => {
    const workflow = definition(
      [
        { key: "start", kind: "start", name: "Start" },
        { key: "right", kind: "activity", name: "Right" },
        { key: "left", kind: "activity", name: "Left" },
        { key: "orphan", kind: "activity", name: "Orphan" },
        { key: "end", kind: "end", name: "End" },
      ],
      [
        { from: "start", to: "right" },
        { from: "start", to: "left" },
        { from: "right", to: "end" },
        { from: "left", to: "end" },
        { from: "orphan", to: "orphan" },
      ],
    );

    expect(
      workflowPreviewActivities(workflow).map((node) => node.name),
    ).toEqual(["Right", "Left", "Orphan"]);
  });
});
