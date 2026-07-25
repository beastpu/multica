import type {
  WorkflowDefinition,
  WorkflowNodeDefinition,
} from "@multica/core/workflows";
import { describe, expect, it } from "vitest";

import {
  buildWorkflowCanvasLayout,
  workflowCanvasEdgeGeometry,
  workflowCanvasEdgeKey,
  WORKFLOW_CANVAS_MIN_HEIGHT,
  WORKFLOW_CANVAS_MIN_WIDTH,
} from "./workflow-canvas-layout";

const node = (
  key: string,
  kind: WorkflowNodeDefinition["kind"] = "activity",
): WorkflowNodeDefinition => ({ key, kind, name: key });

function workflow(
  nodes: WorkflowNodeDefinition[],
  edges: WorkflowDefinition["edges"],
): WorkflowDefinition {
  return {
    schema_version: 1,
    name: "Layout test",
    applies_to: { kind: "issue" },
    roles: [],
    nodes,
    edges,
    acceptance: {},
  };
}

describe("workflow canvas layout", () => {
  it("uses a spacious minimum canvas and centers a serial flow", () => {
    const definition = workflow(
      [node("start", "start"), node("work"), node("end", "end")],
      [
        { from: "start", to: "work" },
        { from: "work", to: "end" },
      ],
    );

    const layout = buildWorkflowCanvasLayout(definition, []);

    expect(layout.width).toBeGreaterThanOrEqual(WORKFLOW_CANVAS_MIN_WIDTH);
    expect(layout.height).toBeGreaterThanOrEqual(WORKFLOW_CANVAS_MIN_HEIGHT);
    for (const canvasNode of layout.nodes) {
      expect(canvasNode.y).toBeGreaterThan(100);
      expect(canvasNode.y + canvasNode.height).toBeLessThan(
        layout.height - 100,
      );
    }
  });

  it("reorders layers to reduce avoidable edge crossings", () => {
    const definition = workflow(
      [
        node("start", "start"),
        node("a"),
        node("b"),
        node("c"),
        node("d"),
        node("end", "end"),
      ],
      [
        { from: "start", to: "a" },
        { from: "start", to: "b" },
        { from: "a", to: "d" },
        { from: "b", to: "c" },
        { from: "c", to: "end" },
        { from: "d", to: "end" },
      ],
    );

    const layout = buildWorkflowCanvasLayout(definition, []);
    const byKey = new Map(
      layout.nodes.map((canvasNode) => [
        canvasNode.definition.key,
        canvasNode,
      ]),
    );

    expect(byKey.get("a")!.y).toBeLessThan(byKey.get("b")!.y);
    expect(byKey.get("d")!.y).toBeLessThan(byKey.get("c")!.y);
  });

  it("routes a cross-layer edge through an obstacle-free outer lane", () => {
    const definition = workflow(
      [
        node("start", "start"),
        node("fast"),
        node("slow_1"),
        node("slow_2"),
        node("merge"),
        node("end", "end"),
      ],
      [
        { from: "start", to: "fast" },
        { from: "start", to: "slow_1" },
        { from: "slow_1", to: "slow_2" },
        { from: "fast", to: "merge" },
        { from: "slow_2", to: "merge" },
        { from: "merge", to: "end" },
      ],
    );

    const layout = buildWorkflowCanvasLayout(definition, []);
    const byKey = new Map(
      layout.nodes.map((canvasNode) => [
        canvasNode.definition.key,
        canvasNode,
      ]),
    );
    const lane = layout.edgeLanes.get(
      workflowCanvasEdgeKey("fast", "merge"),
    );
    const geometry = workflowCanvasEdgeGeometry(
      byKey.get("fast")!,
      byKey.get("merge")!,
      lane,
    );
    const nodeTop = Math.min(...layout.nodes.map((canvasNode) => canvasNode.y));
    const nodeBottom = Math.max(
      ...layout.nodes.map(
        (canvasNode) => canvasNode.y + canvasNode.height,
      ),
    );

    expect(lane).toBeDefined();
    expect(geometry.routed).toBe(true);
    expect(
      geometry.control.y < nodeTop || geometry.control.y > nodeBottom,
    ).toBe(true);
  });

  it("keeps adjacent-layer controls in the horizontal gap", () => {
    const definition = workflow(
      [node("start", "start"), node("work"), node("end", "end")],
      [
        { from: "start", to: "work" },
        { from: "work", to: "end" },
      ],
    );
    const layout = buildWorkflowCanvasLayout(definition, []);
    const byKey = new Map(
      layout.nodes.map((canvasNode) => [
        canvasNode.definition.key,
        canvasNode,
      ]),
    );
    const from = byKey.get("work")!;
    const to = byKey.get("end")!;
    const geometry = workflowCanvasEdgeGeometry(from, to);

    expect(geometry.routed).toBe(false);
    expect(geometry.control.x).toBeGreaterThan(from.x + from.width);
    expect(geometry.control.x).toBeLessThan(to.x);
  });
});
