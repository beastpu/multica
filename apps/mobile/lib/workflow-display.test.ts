import { describe, expect, it } from "vitest";
import type { Issue } from "@multica/core/types";
import type {
  WorkflowDefinition,
  WorkflowNodeInstance,
  WorkflowNodeTask,
} from "@multica/core/workflows";
import {
  latestWorkflowAttemptNodes,
  workflowCanvasColumns,
  workflowIssuesForScope,
} from "./workflow-display";

const node = (
  id: string,
  key: string,
  attempt: number,
  displayOrder: number,
): WorkflowNodeInstance => ({
  id,
  workflow_instance_id: "workflow-1",
  node_key: key,
  node_kind: "activity",
  attempt,
  name: key,
  display_order: displayOrder,
  definition: { key, kind: "activity", name: key },
  status: "active",
  waiting_reasons: [],
  latest_submission_id: null,
  latest_verdict_id: null,
  activated_at: null,
  completed_at: null,
});

describe("workflow mobile display", () => {
  it("keeps only the latest attempt for each activity", () => {
    expect(
      latestWorkflowAttemptNodes([
        node("review-1", "review", 1, 1),
        node("build-1", "build", 1, 0),
        node("review-2", "review", 2, 1),
      ]).map((item) => item.id),
    ).toEqual(["build-1", "review-2"]);
  });

  it("keeps parallel activities in the same canvas column", () => {
    const definition: WorkflowDefinition = {
      schema_version: 1,
      name: "Delivery",
      applies_to: { kind: "issue" },
      roles: [],
      nodes: [
        { key: "start", kind: "start", name: "Start" },
        { key: "frontend", kind: "activity", name: "Frontend" },
        { key: "backend", kind: "activity", name: "Backend" },
        { key: "join", kind: "parallel_join", name: "Join" },
      ],
      edges: [
        { from: "start", to: "frontend" },
        { from: "start", to: "backend" },
        { from: "frontend", to: "join" },
        { from: "backend", to: "join" },
      ],
      acceptance: {},
    };
    const columns = workflowCanvasColumns(definition, [
      node("start", "start", 1, 0),
      node("frontend", "frontend", 1, 1),
      node("backend", "backend", 1, 2),
      node("join", "join", 1, 3),
    ]);

    expect(columns.map((column) => column.nodes.map((item) => item.node_key)))
      .toEqual([["start"], ["frontend", "backend"], ["join"]]);
  });

  it("isolates current activity issues from the all-workflow scope", () => {
    const issues = [
      { id: "issue-a" },
      { id: "issue-b" },
    ] as Issue[];
    const tasks = [
      {
        workflow_node_instance_id: "node-a",
        issue_id: "issue-a",
      },
      {
        workflow_node_instance_id: "node-b",
        issue_id: "issue-b",
      },
    ] as WorkflowNodeTask[];

    expect(workflowIssuesForScope(issues, tasks, "node-a", "current"))
      .toEqual([issues[0]]);
    expect(workflowIssuesForScope(issues, tasks, "node-a", "all"))
      .toEqual(issues);
  });
});
