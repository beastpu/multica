// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { WorkflowRunsPage } from "./workflow-runs-page";

const mocks = vi.hoisted(() => ({
  infiniteQuery: vi.fn(),
}));

const run = {
  id: "run-1",
  workflow_id: "wf-1",
  workflow_name: "Delivery workflow",
  title: "Release 2.0",
  status: "running",
  started_at: "2026-08-03T10:00:00.000Z",
  host_issue_title: "",
  current_activities: [
    { id: "node-1", node_key: "work", name: "Build", status: "active", attempt: 1 },
  ],
  activity_completed: 1,
  activity_total: 3,
};

const finishedRun = {
  ...run,
  id: "run-2",
  title: "",
  host_issue_title: "Hotfix",
  host_issue_id: "issue-2",
  host_issue_identifier: "MUL-7",
  status: "completed",
  current_activities: [],
  activity_completed: 3,
};

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useInfiniteQuery: (options: unknown) => mocks.infiniteQuery(options),
    useQuery: () => ({
      data: {
        workflows: [
          { id: "wf-1", name: "Delivery workflow" },
        ],
      },
      isLoading: false,
      isError: false,
    }),
  };
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return {
    ...actual,
    useWorkspacePaths: () => ({
      workflowRun: (id: string) => `/workspace/workflows/runs/${id}`,
      issueDetail: (id: string) => `/workspace/issues/${id}`,
    }),
  };
});

vi.mock("../navigation", async () => {
  const React = await import("react");
  return {
    AppLink: ({ href, children, ...rest }: {
      href: string;
      children: ReactNode;
    }) => React.createElement("a", { href, ...rest }, children),
  };
});

function wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, workflows: enWorkflows } }}
    >
      {children}
    </I18nProvider>
  );
}

function mockRuns(instances: unknown[]) {
  mocks.infiniteQuery.mockReturnValue({
    data: { pages: [{ instances, next_cursor: null }] },
    isLoading: false,
    isError: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
  });
}

describe("WorkflowRunsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists every run with the workflow it came from", () => {
    mockRuns([run, finishedRun]);
    render(<WorkflowRunsPage />, { wrapper });

    expect(screen.getByRole("link", { name: /Release 2.0/ }))
      .toHaveAttribute("href", "/workspace/workflows/runs/run-1");
    expect(screen.getAllByText("Delivery workflow").length).toBeGreaterThan(0);
    expect(screen.getByText("1/3")).toBeInTheDocument();
    expect(screen.getByText("Build")).toBeInTheDocument();
    // A standalone run has no title of its own, so the host issue names it.
    expect(screen.getByRole("link", { name: /Hotfix/ }))
      .toHaveAttribute("href", "/workspace/workflows/runs/run-2");
  });

  it("shows the host issue when a run has one, and says so when it does not", () => {
    mockRuns([run, finishedRun]);
    render(<WorkflowRunsPage />, { wrapper });

    expect(screen.getByRole("link", { name: "MUL-7" }))
      .toHaveAttribute("href", "/workspace/issues/issue-2");
    expect(within(screen.getByRole("table"))
      .getByText(enWorkflows.runs.standalone)).toBeInTheDocument();
  });

  it("filters by whether a run carries a host issue", async () => {
    const user = userEvent.setup();
    mockRuns([finishedRun]);
    render(<WorkflowRunsPage />, { wrapper });

    await user.click(
      screen.getByRole("button", { name: enWorkflows.runs.host_issue }),
    );
    expect(mocks.infiniteQuery).toHaveBeenLastCalledWith(
      expect.objectContaining({
        queryKey: expect.arrayContaining([
          expect.objectContaining({ has_host_issue: true }),
        ]),
      }),
    );

    await user.click(
      screen.getByRole("button", { name: enWorkflows.runs.standalone }),
    );
    expect(mocks.infiniteQuery).toHaveBeenLastCalledWith(
      expect.objectContaining({
        queryKey: expect.arrayContaining([
          expect.objectContaining({ has_host_issue: false }),
        ]),
      }),
    );
  });

  it("splits history by whether a run is still in flight", async () => {
    const user = userEvent.setup();
    mockRuns([run]);
    render(<WorkflowRunsPage />, { wrapper });

    await user.click(
      screen.getByRole("button", { name: enWorkflows.runs.scope_closed }),
    );
    expect(mocks.infiniteQuery).toHaveBeenLastCalledWith(
      expect.objectContaining({
        queryKey: expect.arrayContaining([
          expect.objectContaining({ status: "terminal" }),
        ]),
      }),
    );
  });

  it("opens narrowed to one workflow when asked", () => {
    mockRuns([run]);
    render(<WorkflowRunsPage workflowId="wf-1" />, { wrapper });

    expect(mocks.infiniteQuery).toHaveBeenLastCalledWith(
      expect.objectContaining({
        queryKey: expect.arrayContaining([
          expect.objectContaining({ workflow_id: "wf-1" }),
        ]),
      }),
    );
  });

  it("names the workflow when scoped to one, and dates each run", () => {
    mockRuns([run]);
    render(<WorkflowRunsPage workflowId="wf-1" />, { wrapper });

    // Reading history means lining rows up against each other and against
    // something that happened elsewhere; "18 hours ago" serves neither.
    expect(screen.getByText(/^2026-08-03 \d{2}:\d{2}$/)).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /Delivery workflow runs/ }),
    ).toBeInTheDocument();
  });

  it("says so when a workspace has no runs yet", () => {
    mockRuns([]);
    render(<WorkflowRunsPage />, { wrapper });

    expect(screen.getByText(enWorkflows.runs.empty_title))
      .toBeInTheDocument();
  });
});
