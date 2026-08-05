import type {
  WorkflowDefinition,
  WorkflowNodeDefinition,
} from "@multica/core/workflows";
import { describe, expect, it } from "vitest";

import {
  addWorkflowBranch,
  connectWorkflowNodes,
  insertWorkflowNodeOnEdge,
  moveWorkflowGatewayCase,
  nextWorkflowNodeKey,
  removeWorkflowEdge,
  removeWorkflowNode,
  updateWorkflowGatewayCase,
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

  it("preserves the original edge case binding when inserting in series", () => {
    const gateway: WorkflowNodeDefinition = {
      key: "route",
      kind: "gateway",
      name: "Route",
      cases: [{ id: "else" }],
    };
    const definition = workflow(
      [start, gateway, end],
      [
        { from: "start", to: "route" },
        { from: "route", to: "end", from_case: "else" },
      ],
    );
    const inserted = activity("inserted");

    const next = insertWorkflowNodeOnEdge(
      definition,
      inserted,
      { from: "route", to: "end" },
    );

    expect(next?.edges).toEqual([
      { from: "start", to: "route" },
      { from: "route", to: "inserted", from_case: "else" },
      { from: "inserted", to: "end" },
    ]);
  });

  it("gives an inserted gateway an else case bound to its pass-through", () => {
    const definition = workflow(
      [start, activity("work"), end],
      [
        { from: "start", to: "work" },
        { from: "work", to: "end" },
      ],
    );
    const gateway: WorkflowNodeDefinition = {
      key: "route",
      kind: "gateway",
      name: "Route",
    };

    const next = insertWorkflowNodeOnEdge(
      definition,
      gateway,
      { from: "work", to: "end" },
    );

    expect(next?.nodes.find((node) => node.key === "route")?.cases).toEqual([
      { id: "else" },
    ]);
    expect(next?.edges).toEqual([
      { from: "start", to: "work" },
      { from: "work", to: "route" },
      { from: "route", to: "end", from_case: "else" },
    ]);
  });

  it("binds gateway branches to else first, then fresh conditional cases", () => {
    const gateway: WorkflowNodeDefinition = {
      key: "route",
      kind: "gateway",
      name: "Route",
      cases: [{ id: "else" }],
    };
    const definition = workflow(
      [start, gateway, activity("left"), end],
      [
        { from: "start", to: "route" },
        { from: "route", to: "left", from_case: "else" },
        { from: "left", to: "end" },
      ],
    );

    const next = addWorkflowBranch(definition, activity("right"), "route");

    expect(next?.edges.find((edge) => edge.to === "right")).toEqual({
      from: "route",
      to: "right",
      from_case: "c1",
    });
    // The new conditional case sits before else — declared order is priority.
    expect(
      next?.nodes.find((node) => node.key === "route")?.cases?.map((c) => c.id),
    ).toEqual(["c1", "else"]);
  });

  it("updates and reorders gateway cases with else pinned last", () => {
    const gateway: WorkflowNodeDefinition = {
      key: "route",
      kind: "gateway",
      name: "Route",
      cases: [
        { id: "c1", when: "a == true" },
        { id: "c2", when: "b == true" },
        { id: "else" },
      ],
    };
    const definition = workflow(
      [start, gateway, activity("x"), activity("y"), activity("z"), end],
      [
        { from: "start", to: "route" },
        { from: "route", to: "x", from_case: "c1" },
        { from: "route", to: "y", from_case: "c2" },
        { from: "route", to: "z", from_case: "else" },
        { from: "x", to: "end" },
        { from: "y", to: "end" },
        { from: "z", to: "end" },
      ],
    );

    const updated = updateWorkflowGatewayCase(definition, "route", "c1", {
      label: "严重缺陷",
      when: 'severity == "high"',
    });
    expect(
      updated?.nodes.find((node) => node.key === "route")?.cases?.[0],
    ).toEqual({ id: "c1", label: "严重缺陷", when: 'severity == "high"' });

    const moved = moveWorkflowGatewayCase(definition, "route", "c2", "up");
    expect(
      moved?.nodes.find((node) => node.key === "route")?.cases?.map((c) => c.id),
    ).toEqual(["c2", "c1", "else"]);

    // else cannot move, and nothing moves past it.
    expect(moveWorkflowGatewayCase(definition, "route", "else", "up")).toBeNull();
    expect(moveWorkflowGatewayCase(definition, "route", "c2", "down")).toBeNull();
  });

  it("drops the bound conditional case when its edge is removed", () => {
    const gateway: WorkflowNodeDefinition = {
      key: "route",
      kind: "gateway",
      name: "Route",
      cases: [{ id: "c1", when: "a == true" }, { id: "else" }],
    };
    const definition = workflow(
      [start, gateway, activity("x"), activity("y"), end],
      [
        { from: "start", to: "route" },
        { from: "route", to: "x", from_case: "c1" },
        { from: "route", to: "y", from_case: "else" },
        { from: "x", to: "end" },
        { from: "y", to: "end" },
      ],
    );

    const next = removeWorkflowEdge(definition, { from: "route", to: "x" });
    expect(
      next?.nodes.find((node) => node.key === "route")?.cases?.map((c) => c.id),
    ).toEqual(["else"]);

    // Removing the else edge keeps the structural else case for re-binding.
    const elseRemoved = removeWorkflowEdge(definition, { from: "route", to: "y" });
    expect(
      elseRemoved?.nodes.find((node) => node.key === "route")?.cases
        ?.map((c) => c.id),
    ).toEqual(["c1", "else"]);
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
      key: "route",
      kind: "gateway",
      name: "Route",
      cases: [{ id: "else" }],
    };
    const definition = workflow(
      [start, gateway, activity("work"), end],
      [
        { from: "start", to: "route" },
        { from: "route", to: "work", from_case: "else" },
        { from: "work", to: "end" },
      ],
    );

    const next = removeWorkflowNode(definition, "work");

    expect(next?.nodes.map((node) => node.key)).toEqual([
      "start",
      "route",
      "end",
    ]);
    expect(next?.edges).toEqual([
      { from: "start", to: "route" },
      { from: "route", to: "end", from_case: "else" },
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

  it("leaves acceptance alone and never deletes boundary nodes", () => {
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
    expect(removeWorkflowNode(definition, "end")).toBeNull();
    // Acceptance belongs to the run, and its rework destinations come from the
    // graph — deleting a node updates them by definition.
    expect(removeWorkflowNode(definition, "accept")?.acceptance).toEqual({
      policy: "member",
    });
  });
});
