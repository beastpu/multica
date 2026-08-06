import { render, screen } from "@testing-library/react";
import { ApiError } from "@multica/core/api";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { WorkflowAwareIssueDetail } from "./workflow-aware-issue-detail";

const mockState = vi.hoisted(() => ({
  enabled: false,
  query: {
    data: undefined as
      | { instance: { id: string; workflow_name: string; status: string } }
      | undefined,
    error: null as unknown,
    isPending: false,
    isError: false,
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => mockState.query,
  // The artifact-adoption provider wrapping the issue builds its query options
  // with this.
  queryOptions: (options: unknown) => options,
}));

// The provider contributes an attachment action only inside a workflow node
// issue; these cases are about the surface around it, so it stays inert.
vi.mock("./artifact-adopt-provider", () => ({
  ArtifactAdoptProvider: ({ children }: { children: React.ReactNode }) => children,
}));

vi.mock("@multica/core/config", () => ({
  useWorkspaceFeatureEnabled: () => mockState.enabled,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    workflowRun: (id: string) => `/workspace/workflows/runs/${id}`,
  }),
}));

vi.mock("@multica/core/workflows", () => ({
  issueWorkflowOptions: () => ({
    queryKey: ["workflows", "workspace-1", "issue", "issue-1"],
  }),
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (select: (dict: Record<string, Record<string, string>>) => string) =>
      select({
        workbench: {
          driven_by_run: "A workflow run drives this issue.",
          open_workbench: "Open workbench",
        },
      }),
  }),
}));

vi.mock("../navigation", () => ({
  AppLink: ({ href, children }: { href: string; children: ReactNode }) => (
    <a href={href}>{children}</a>
  ),
}));

vi.mock("./workflow-status", () => ({
  WorkflowStatusBadge: ({ status }: { status: string }) => <span>{status}</span>,
}));

vi.mock("../issues/components", () => ({
  IssueDetail: ({
    issueId,
    headerActions,
  }: {
    issueId: string;
    headerActions?: ReactNode;
  }) => (
    <div data-testid="issue-detail">
      {issueId}
      {headerActions}
    </div>
  ),
}));

vi.mock("./workflow-start-dialog", () => ({
  WorkflowStartDialog: () => <button>Start workflow</button>,
}));

describe("WorkflowAwareIssueDetail", () => {
  beforeEach(() => {
    mockState.enabled = false;
    mockState.query = {
      data: undefined,
      error: null,
      isPending: false,
      isError: false,
    };
  });

  it("shows an ordinary issue with no workflow affordance when the flag is off", () => {
    render(<WorkflowAwareIssueDetail issueId="issue-1" />);

    expect(screen.getByTestId("issue-detail")).toHaveTextContent("issue-1");
    expect(screen.queryByRole("button", { name: "Start workflow" })).toBeNull();
    expect(screen.queryByRole("link", { name: /Open workbench/ })).toBeNull();
  });

  it("offers to start a workflow on an issue that has never run one", () => {
    mockState.enabled = true;
    mockState.query.error = new ApiError(
      "workflow instance not found",
      404,
      "Not Found",
    );
    mockState.query.isError = true;

    render(<WorkflowAwareIssueDetail issueId="issue-1" />);

    expect(screen.getByTestId("issue-detail")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start workflow" }))
      .toBeInTheDocument();
  });

  // The host issue used to be replaced by the workbench, which left it with no
  // route of its own — and made the workbench's own "open parent issue" link
  // lead straight back to the workbench. The issue keeps its page; the banner
  // is how it points at the run.
  it("keeps the issue readable and links out to the run that drives it", () => {
    mockState.enabled = true;
    mockState.query.data = {
      instance: {
        id: "instance-1",
        workflow_name: "Defect triage",
        status: "running",
      },
    };

    render(<WorkflowAwareIssueDetail issueId="issue-1" />);

    expect(screen.getByTestId("issue-detail")).toHaveTextContent("issue-1");
    expect(screen.getByText("A workflow run drives this issue."))
      .toBeInTheDocument();
    expect(screen.getByText("Defect triage")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Open workbench/ }))
      .toHaveAttribute("href", "/workspace/workflows/runs/instance-1");
  });

  // Starting a second run on the same host is a 409, so the button that would
  // do it does not belong beside a run that is already there.
  it("does not offer to start another workflow while one is attached", () => {
    mockState.enabled = true;
    mockState.query.data = {
      instance: {
        id: "instance-1",
        workflow_name: "Defect triage",
        status: "running",
      },
    };

    render(<WorkflowAwareIssueDetail issueId="issue-1" />);

    expect(screen.queryByRole("button", { name: "Start workflow" })).toBeNull();
  });
});
