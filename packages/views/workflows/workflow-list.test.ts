import type { WorkflowInstance } from "@multica/core/workflows";
import { describe, expect, it } from "vitest";

import {
  canManageWorkflowTemplates,
  partitionWorkflowRuns,
  workflowStatusForTab,
} from "./workflow-list";

function run(id: string, nextAction: string): WorkflowInstance {
  return {
    id,
    workspace_id: "workspace-1",
    template_id: "template-1",
    template_version_id: "version-1",
    host_issue_id: `host-${id}`,
    status: "running",
    host_status_mode: "independent",
    input: {},
    result: {},
    revision: 1,
    started_by_type: "member",
    started_by_id: "member-1",
    started_at: "2026-07-24T00:00:00Z",
    paused_at: null,
    completed_at: null,
    cancelled_at: null,
    last_reconciled_at: null,
    created_at: "2026-07-24T00:00:00Z",
    updated_at: "2026-07-24T00:00:00Z",
    next_action: nextAction,
    intervention_reason: "",
    host_issue_title: id,
    host_issue_identifier: `MUL-${id}`,
    host_issue_priority: "none",
    project_id: null,
    template_name: "Delivery",
    template_version: 1,
    current_activities: [],
    activity_completed: 0,
    activity_total: 1,
    current_owners: [],
  };
}

describe("workflow list presentation", () => {
  it("keeps real user interventions above healthy runs without creating false todos", () => {
    const result = partitionWorkflowRuns([
      run("healthy", "view_current_activity"),
      run("acceptance", "review_acceptance"),
      run("terminal", "none"),
      run("submission", "submit_result"),
    ]);

    expect(result.actionable.map((item) => item.id)).toEqual([
      "acceptance",
      "submission",
    ]);
    expect(result.healthy.map((item) => item.id)).toEqual([
      "healthy",
      "terminal",
    ]);
  });

  it("defaults the workspace entry to active runs and preserves explicit filters", () => {
    expect(workflowStatusForTab("active", "")).toBe("active");
    expect(workflowStatusForTab("mine", "")).toBe("active");
    expect(workflowStatusForTab("completed", "")).toBe("terminal");
    expect(workflowStatusForTab("active", "paused")).toBe("paused");
  });

  it("limits template management to workspace owners and admins", () => {
    expect(canManageWorkflowTemplates("owner")).toBe(true);
    expect(canManageWorkflowTemplates("admin")).toBe(true);
    expect(canManageWorkflowTemplates("member")).toBe(false);
    expect(canManageWorkflowTemplates(undefined)).toBe(false);
  });
});
