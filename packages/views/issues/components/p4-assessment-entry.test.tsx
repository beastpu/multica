import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Issue } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { IssueP4AssessmentTags } from "./p4-assessment-entry";

const defaultRecord = vi.hoisted(() => ({
    issue_id: "issue-1",
    issue_identifier: "TES-3",
    external: {
      binding_id: "binding-1",
    },
    human_review: {
      outcome: "needs_changes",
      reasons: ["coverage_incomplete"],
      note: "Needs more validation.",
    },
  }));
const records = vi.hoisted(() => [defaultRecord]);
const mutate = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/dashboard", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/dashboard")>(
      "@multica/core/dashboard",
    );
  return {
    ...actual,
    useUpdateAgentFixReview: () => ({
      mutate,
      isPending: false,
    }),
  };
});

vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: () => ({ data: records, isLoading: false }),
  };
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

const issue: Issue = {
  id: "issue-1",
  workspace_id: "ws-1",
  number: 3,
  identifier: "TES-3",
  title: "P4 assessment demo - visible summary",
  description: null,
  status: "done",
  priority: "medium",
  assignee_type: null,
  assignee_id: null,
  creator_type: "member",
  creator_id: "user-1",
  parent_issue_id: null,
  project_id: null,
  position: 0,
  stage: null,
  start_date: null,
  due_date: null,
  metadata: { p4_assessment: true },
  created_at: "2026-06-28T00:00:00Z",
  updated_at: "2026-06-28T00:00:00Z",
};

describe("IssueP4AssessmentTags", () => {
  beforeEach(() => {
    mutate.mockClear();
    records.splice(0, records.length, defaultRecord);
  });

  it("shows the shared human review outcome from operations assessment data", () => {
    renderWithI18n(<IssueP4AssessmentTags issue={issue} />);

    expect(screen.getByText("P4")).toBeTruthy();
    expect(screen.getByText("AI assessment")).toBeTruthy();
    expect(screen.getByText("Needs changes")).toBeTruthy();
    expect(screen.queryByText("Review")).toBeNull();
  });

  it("opens the shared human review editor and saves through the operations mutation", async () => {
    const user = userEvent.setup();
    renderWithI18n(<IssueP4AssessmentTags issue={issue} />);

    await user.click(screen.getByText("Needs changes"));
    expect(screen.getByText("Human review AI fix result")).toBeTruthy();

    await user.click(screen.getByText("Accepted"));
    await user.click(screen.getByText("Complete"));
    const note = screen.getByPlaceholderText(/Add judgement details/i);
    await user.clear(note);
    await user.type(note, "Looks good.");
    await user.click(screen.getByText("Save"));

    expect(mutate).toHaveBeenCalledWith(
      {
        issueId: "issue-1",
        bindingId: "binding-1",
        data: {
          outcome: "accepted",
          reasons: ["complete_usable"],
          note: "Looks good.",
        },
      },
      expect.objectContaining({
        onSuccess: expect.any(Function),
        onError: expect.any(Function),
      }),
    );
  });

  it("shows the human review trigger before any manual outcome exists", async () => {
    records.splice(0, records.length, {
      ...defaultRecord,
      human_review: {
        outcome: "",
        reasons: [],
        note: "",
      },
    });
    const user = userEvent.setup();
    renderWithI18n(<IssueP4AssessmentTags issue={issue} />);

    await user.click(screen.getByText("Human review"));

    expect(screen.getByText("Human review AI fix result")).toBeTruthy();
    expect(screen.getByText("Pending")).toBeTruthy();
  });

  it("shows assessment tags when only operations assessment data identifies the issue", () => {
    renderWithI18n(
      <IssueP4AssessmentTags
        issue={{
          ...issue,
          title: "Ordinary issue title",
          metadata: {},
        }}
      />,
    );

    expect(screen.getByText("P4")).toBeTruthy();
    expect(screen.getByText("Needs changes")).toBeTruthy();
  });
});
