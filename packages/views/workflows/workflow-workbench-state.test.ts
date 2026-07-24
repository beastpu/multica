import type { Issue } from "@multica/core/types";
import type {
  WorkflowNodeInstance,
  WorkflowNodeTask,
} from "@multica/core/workflows";
import { describe, expect, it } from "vitest";

import {
  latestWorkflowAttemptNodes,
  workflowIssuesForScope,
} from "./workflow-workbench-state";

describe("workflow workbench state", () => {
  it("shows only the latest attempt for each activity while preserving graph order", () => {
    const nodes = [
      { id: "work-1", node_key: "work", attempt: 1, display_order: 2 },
      { id: "review-1", node_key: "review", attempt: 1, display_order: 3 },
      { id: "work-2", node_key: "work", attempt: 2, display_order: 2 },
    ] as WorkflowNodeInstance[];

    expect(
      latestWorkflowAttemptNodes(nodes).map((node) => node.id),
    ).toEqual(["work-2", "review-1"]);
  });

  it("isolates current-node issues from the all-workflow scope", () => {
    const issues = [
      { id: "issue-current" },
      { id: "issue-other" },
      { id: "issue-unbound" },
    ] as Issue[];
    const tasks = [
      {
        id: "task-current",
        workflow_node_instance_id: "node-current",
        issue_id: "issue-current",
      },
      {
        id: "task-other",
        workflow_node_instance_id: "node-other",
        issue_id: "issue-other",
      },
      {
        id: "task-pending",
        workflow_node_instance_id: "node-current",
        issue_id: null,
      },
    ] as WorkflowNodeTask[];

    expect(
      workflowIssuesForScope(
        issues,
        tasks,
        "node-current",
        "current",
      ).map((issue) => issue.id),
    ).toEqual(["issue-current"]);
    expect(
      workflowIssuesForScope(
        issues,
        tasks,
        "node-current",
        "all",
      ).map((issue) => issue.id),
    ).toEqual(["issue-current", "issue-other", "issue-unbound"]);
  });
});
