import { render, screen } from "@testing-library/react";
import { ApiError } from "@multica/core/api";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { WorkflowAwareIssueDetail } from "./workflow-aware-issue-detail";

const mockState = vi.hoisted(() => ({
  enabled: false,
  query: {
    data: undefined as
      | { instance: { id: string } }
      | undefined,
    error: null as unknown,
    isPending: false,
    isError: false,
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => mockState.query,
  // The artifact-adoption provider wrapping the ordinary-issue branch builds
  // its query options with this.
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

vi.mock("@multica/core/workflows", () => ({
  issueWorkflowOptions: () => ({
    queryKey: ["workflows", "workspace-1", "issue", "issue-1"],
  }),
}));

vi.mock("@multica/ui/components/ui/skeleton", () => ({
  Skeleton: () => <div data-testid="workflow-loading" />,
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

vi.mock("./workflow-workbench", () => ({
  WorkflowWorkbench: ({ instanceId }: { instanceId: string }) => (
    <div data-testid="workflow-workbench">{instanceId}</div>
  ),
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

  it("keeps the legacy issue surface and hides workflow actions when the server flag is absent", () => {
    render(<WorkflowAwareIssueDetail issueId="issue-1" />);

    expect(screen.getByTestId("issue-detail")).toHaveTextContent("issue-1");
    expect(
      screen.queryByRole("button", { name: "Start workflow" }),
    ).not.toBeInTheDocument();
  });

  it("uses the single workflow-first workbench only when the issue hosts an instance", () => {
    mockState.enabled = true;
    mockState.query.data = { instance: { id: "instance-1" } };

    render(<WorkflowAwareIssueDetail issueId="issue-1" />);

    expect(screen.getByTestId("workflow-workbench")).toHaveTextContent(
      "instance-1",
    );
    expect(screen.queryByTestId("issue-detail")).not.toBeInTheDocument();
  });

  it("keeps an ordinary issue usable and offers start workflow after an expected 404", () => {
    mockState.enabled = true;
    mockState.query.error = new ApiError(
      "workflow not found",
      404,
      "Not Found",
    );
    mockState.query.isError = true;

    render(<WorkflowAwareIssueDetail issueId="issue-1" />);

    expect(screen.getByTestId("issue-detail")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Start workflow" }),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("workflow-workbench")).not.toBeInTheDocument();
  });

  it("renders a stable loading shell while checking an enabled workflow", () => {
    mockState.enabled = true;
    mockState.query.isPending = true;

    render(<WorkflowAwareIssueDetail issueId="issue-1" />);

    expect(screen.getAllByTestId("workflow-loading")).toHaveLength(3);
    expect(screen.queryByTestId("issue-detail")).not.toBeInTheDocument();
  });
});
