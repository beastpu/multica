import type {
  WorkflowDefinition,
  WorkflowNodeDefinition,
} from "@multica/core/workflows";
import { describe, expect, it } from "vitest";

import {
  addWorkflowBranch,
  connectWorkflowNodes,
  insertWorkflowNodeOnEdge,
  nextWorkflowNodeKey,
  removeWorkflowEdge,
  removeWorkflowNode,
  workflowConnectionTargets,
} from "./workflow-graph-editor";

function workflow(
  nodes: WorkflowNodeDefinition[],
  edges: WorkflowDefinition["edges"],
): WorkflowDefinition {
  return {
    schema_version: 1,
    name: "Test",
    roles: [],
    nodes,
    edges,
    acceptance: {},
  };
}

const start: WorkflowNodeDefinition = {
  key: "start",
  kind: "start",
  name: "Start",
};
const end: WorkflowNodeDefinition = {
  key: "end",
  kind: "end",
  name: "End",
};
const activity = (key: string): WorkflowNodeDefinition => ({
  key,
  kind: "activity",
  name: key,
});

describe("workflow graph editor", () => {
  it("only offers legal and non-redundant connection targets", () => {
    const definition = workflow(
      [start, activity("a"), activity("b"), activity("branch"), end],
      [
        { from: "start", to: "a" },
        { from: "a", to: "b" },
        { from: "b", to: "end" },
        { from: "start", to: "branch" },
      ],
    );

    expect(
      workflowConnectionTargets(definition, "a").map((node) => node.key),
    ).toEqual(["branch"]);
    expect(workflowConnectionTargets(definition, "end")).toEqual([]);
  });

  it("allows a leaf branch to merge into an activity or End", () => {
    const definition = workflow(
      [start, activity("left"), activity("right"), activity("merge"), end],
      [
        { from: "start", to: "left" },
        { from: "start", to: "right" },
        { from: "left", to: "merge" },
        { from: "merge", to: "end" },
      ],
    );

    expect(
      workflowConnectionTargets(definition, "right").map((node) => node.key),
    ).toEqual(["left", "merge", "end"]);
  });

  it("rejects duplicate, cyclic, and transitive shortcut connections", () => {
    const definition = workflow(
      [start, activity("a"), activity("b"), end],
      [
        { from: "start", to: "a" },
        { from: "a", to: "b" },
        { from: "b", to: "end" },
      ],
    );

    expect(connectWorkflowNodes(definition, { from: "a", to: "b" })).toBeNull();
    expect(connectWorkflowNodes(definition, { from: "b", to: "a" })).toBeNull();
    expect(connectWorkflowNodes(definition, { from: "a", to: "end" })).toBeNull();
  });

  it("adds a direct branch without generating split or join nodes", () => {
    const definition = workflow(
      [start, activity("work"), end],
      [
        { from: "start", to: "work" },
        { from: "work", to: "end" },
      ],
    );
    const branch = activity("branch");

    const next = addWorkflowBranch(definition, branch, "work");

    expect(next?.nodes).toEqual([...definition.nodes, branch]);
    expect(next?.edges).toEqual([
      ...definition.edges,
      { from: "work", to: "branch" },
    ]);
    expect(
      next?.nodes.some((node) =>
        node.kind === "parallel_split" || node.kind === "parallel_join"
      ),
    ).toBe(false);
  });

  it("preserves the original edge metadata when inserting in series", () => {
    const gateway: WorkflowNodeDefinition = {
      key: "choice",
      kind: "gateway",
      name: "Choice",
    };
    const definition = workflow(
      [start, gateway, end],
      [
        { from: "start", to: "choice" },
        { from: "choice", to: "end", default: true },
      ],
    );
    const inserted = activity("inserted");

    const next = insertWorkflowNodeOnEdge(
      definition,
      inserted,
      { from: "choice", to: "end" },
    );

    expect(next?.edges).toEqual([
      { from: "start", to: "choice" },
      { from: "choice", to: "inserted", default: true },
      { from: "inserted", to: "end" },
    ]);
  });

  it("returns no change for missing edges, sources, and duplicate keys", () => {
    const definition = workflow(
      [start, activity("work"), end],
      [{ from: "start", to: "work" }],
    );

    expect(
      insertWorkflowNodeOnEdge(
        definition,
        activity("new"),
        { from: "work", to: "end" },
      ),
    ).toBeNull();
    expect(addWorkflowBranch(definition, activity("work"), "start")).toBeNull();
    expect(addWorkflowBranch(definition, activity("new"), "missing")).toBeNull();
  });

  it("creates stable collision-free keys", () => {
    const definition = workflow(
      [start, activity("activity_1"), activity("activity_3"), end],
      [],
    );

    expect(nextWorkflowNodeKey(definition, "activity")).toBe("activity_2");
    expect(nextWorkflowNodeKey(definition, "gateway")).toBe("gateway_1");
  });

  it("removes an edge only when it exists", () => {
    const definition = workflow(
      [start, activity("work"), end],
      [{ from: "start", to: "work" }],
    );

    expect(
      removeWorkflowEdge(definition, { from: "work", to: "end" }),
    ).toBeNull();
    expect(
      removeWorkflowEdge(definition, { from: "start", to: "work" })?.edges,
    ).toEqual([]);
  });

  it("splices a deleted serial node and preserves predecessor edge metadata", () => {
    const gateway: WorkflowNodeDefinition = {
      key: "choice",
      kind: "gateway",
      name: "Choice",
    };
    const definition = workflow(
      [start, gateway, activity("work"), end],
      [
        { from: "start", to: "choice" },
        { from: "choice", to: "work", default: true },
        { from: "work", to: "end" },
      ],
    );

    const next = removeWorkflowNode(definition, "work");

    expect(next?.nodes.map((node) => node.key)).toEqual([
      "start",
      "choice",
      "end",
    ]);
    expect(next?.edges).toEqual([
      { from: "start", to: "choice" },
      { from: "choice", to: "end", default: true },
    ]);
  });

  it("does not invent cross-product edges when deleting a branch or merge", () => {
    const definition = workflow(
      [start, activity("left"), activity("right"), activity("merge"), end],
      [
        { from: "start", to: "left" },
        { from: "start", to: "right" },
        { from: "left", to: "merge" },
        { from: "right", to: "merge" },
        { from: "merge", to: "end" },
      ],
    );

    const next = removeWorkflowNode(definition, "merge");

    expect(next?.edges).toEqual([
      { from: "start", to: "left" },
      { from: "start", to: "right" },
    ]);
  });

  it("leaves acceptance alone on deletion and never deletes Start", () => {
    const definition = {
      ...workflow(
        [start, activity("accept"), end],
        [
          { from: "start", to: "accept" },
          { from: "accept", to: "end" },
        ],
      ),
      acceptance: { policy: "member" },
    } satisfies WorkflowDefinition;

    expect(removeWorkflowNode(definition, "start")).toBeNull();
    // Acceptance belongs to the run, and its rework destinations come from the
    // graph — deleting a node updates them by definition.
    expect(removeWorkflowNode(definition, "accept")?.acceptance).toEqual({
      policy: "member",
    });
  });
});
