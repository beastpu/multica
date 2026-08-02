import { describe, expect, it } from "vitest";
import {
  isWorkflowNodeOpen,
  workflowNodeDisplayStatus,
} from "./workflow-status";

describe("workflow node status", () => {
  it("counts a node awaiting its reviewer as open", () => {
    // in_review was added to the engine and six hand-copied status lists in
    // the workbench did not learn it, which hid the verdict form from the
    // reviewer the node was waiting on.
    expect(isWorkflowNodeOpen("in_review")).toBe(true);
  });

  it("counts every state the flow is standing on as open", () => {
    for (const status of ["active", "in_review", "waiting", "blocked"]) {
      expect(isWorkflowNodeOpen(status), status).toBe(true);
    }
  });

  it("counts settled and unreached states as closed", () => {
    for (const status of ["pending", "completed", "skipped", "superseded"]) {
      expect(isWorkflowNodeOpen(status), status).toBe(false);
    }
  });

  it("shows a rolled-back downstream node as not yet run", () => {
    expect(workflowNodeDisplayStatus("superseded")).toBe("pending");
  });

  it("leaves every other node status alone", () => {
    for (const status of ["active", "in_review", "completed", "skipped"]) {
      expect(workflowNodeDisplayStatus(status)).toBe(status);
    }
  });
});
