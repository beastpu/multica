import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { WorkflowNodeContextPanel } from "./workflow-node-context-panel";

const mockState = vi.hoisted(() => ({
  enabled: true,
  query: { data: undefined as unknown, isError: false },
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => mockState.query,
  queryOptions: (options: unknown) => options,
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
  issueWorkflowNodeOptions: () => ({ queryKey: ["node"] }),
}));

vi.mock("../navigation", () => ({
  AppLink: ({ children, href }: { children: ReactNode; href: string }) => (
    <a href={href}>{children}</a>
  ),
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (select: (dict: Record<string, Record<string, string>>) => string) =>
      select({
        node_context: {
          open_run: "Open run",
          upstream: "From upstream",
          no_upstream: "This is the first activity in the run.",
          no_conclusion: "Nothing handed over yet.",
          extracted_output:
            "No conclusion was written — this is the raw execution output.",
          owes: "What this activity owes",
          owes_conclusion_only:
            "Nothing formal. Hand off a conclusion when you finish.",
          output_field: "output field",
          required: "required",
          optional: "optional",
          delivered: "delivered",
          not_delivered: "not submitted",
        },
      }),
  }),
}));

const nodeContext = {
  instance_id: "run-1",
  node_instance_id: "node-1",
  node_key: "implement",
  node_name: "Implementation",
  run_title: "Import users from CSV",
  instructions: "",
  host_issue: "MUL-123",
  node_issues: ["MUL-124"],
  artifacts: [{
    id: "",
    key: "impl_change",
    name: "Implementation MR",
    description: "",
    kind: "link",
    required: true,
    delivered: false,
    review_status: "",
  }],
  outputs: [],
  upstream: [{
    node_key: "design",
    name: "Technical design",
    status: "completed",
    summary: "Stream the CSV; two edge cases stay open.",
    worker_output: "",
    issues: ["MUL-122"],
    artifacts: [],
  }],
};

describe("WorkflowNodeContextPanel", () => {
  beforeEach(() => {
    mockState.enabled = true;
    mockState.query = { data: undefined, isError: false };
  });

  // Ordinary issues get a 404 from this endpoint, and there are far more of
  // them than node issues — a panel frame around nothing would be on most
  // issues in the workspace.
  it("renders nothing for an issue that is not a workflow node", () => {
    const { container } = render(<WorkflowNodeContextPanel issueId="issue-1" />);
    expect(container).toBeEmptyDOMElement();
  });

  // The point of the panel: the conclusion lives on the run, not on this
  // issue, so without it the executor has no way to see what came before.
  it("shows the predecessor's conclusion and what this node owes", () => {
    mockState.query = { data: nodeContext, isError: false };
    render(<WorkflowNodeContextPanel issueId="issue-1" />);

    expect(screen.getByText("Technical design")).toBeInTheDocument();
    expect(
      screen.getByText("Stream the CSV; two edge cases stay open."),
    ).toBeInTheDocument();
    expect(screen.getByText("Implementation MR")).toBeInTheDocument();
    expect(screen.getByText(/required/)).toBeInTheDocument();
    expect(screen.getByText(/not submitted/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Open run/ })).toHaveAttribute(
      "href",
      "/workspace/workflows/runs/run-1",
    );
  });

  // A platform extract is not a conclusion. Presenting it as one would tell the
  // reader a person concluded something nobody wrote.
  it("labels raw execution output as an extract rather than a conclusion", () => {
    mockState.query = {
      data: {
        ...nodeContext,
        upstream: [{
          ...nodeContext.upstream[0],
          summary: "",
          worker_output: "Ran the importer against staging.",
        }],
      },
      isError: false,
    };
    render(<WorkflowNodeContextPanel issueId="issue-1" />);

    expect(
      screen.getByText(/raw execution output/),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Ran the importer against staging."),
    ).toBeInTheDocument();
  });

  // A malformed or partial payload must not blank the issue page around it.
  it("survives a payload whose collections are missing", () => {
    mockState.query = {
      data: { node_key: "implement", node_name: "Implementation" },
      isError: false,
    };
    render(<WorkflowNodeContextPanel issueId="issue-1" />);

    expect(screen.getByText("Implementation")).toBeInTheDocument();
    expect(
      screen.getByText("This is the first activity in the run."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Nothing formal. Hand off a conclusion when you finish."),
    ).toBeInTheDocument();
  });
});
