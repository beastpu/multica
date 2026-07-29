// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import type { Issue, IssueStatus } from "@multica/core/types";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enIssues from "../locales/en/issues.json";
import enWorkflows from "../locales/en/workflows.json";
import { WorkflowNodeIssues } from "./workflow-node-issues";

const mutate = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/issues/mutations", () => ({
  useUpdateIssue: () => ({ mutate }),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/issues/${id}` }),
}));

vi.mock("../navigation", () => ({
  AppLink: (
    { href, children, ...rest }: { href: string; children: React.ReactNode },
  ) => <a href={href} {...rest}>{children}</a>,
}));

function issue(overrides: Partial<Issue> & { id: string }): Issue {
  return {
    workspace_id: "ws-1",
    number: 1,
    identifier: "MUL-1",
    title: "Implement the thing",
    description: null,
    status: "in_review" as IssueStatus,
    priority: "none",
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
    metadata: {},
    properties: {},
    created_at: "2026-07-28T00:00:00Z",
    updated_at: "2026-07-28T00:00:00Z",
    ...overrides,
  } as Issue;
}

function renderList(issues: Issue[], canManage = true) {
  return render(
    <I18nProvider
      locale="en"
      resources={{ en: { workflows: enWorkflows, issues: enIssues } }}
    >
      <WorkflowNodeIssues issues={issues} canManage={canManage} />
    </I18nProvider>,
  );
}

beforeEach(() => {
  mutate.mockClear();
});

describe("WorkflowNodeIssues", () => {
  it("lists each issue with its identifier and title", () => {
    renderList([
      issue({ id: "i-1", identifier: "MUL-1", title: "First" }),
      issue({ id: "i-2", identifier: "MUL-2", title: "Second" }),
    ]);

    expect(screen.getByText("MUL-1")).toBeInTheDocument();
    expect(screen.getByText("First")).toBeInTheDocument();
    expect(screen.getByText("MUL-2")).toBeInTheDocument();
    expect(screen.getByText("Second")).toBeInTheDocument();
  });

  // The whole point of the surface: the review → done move must not require
  // leaving the run.
  it("changes status inline without navigating away", async () => {
    const user = userEvent.setup();
    renderList([issue({ id: "i-1", status: "in_review" as IssueStatus })]);

    await user.click(screen.getByRole("button", { name: /in review/i }));
    // The picker renders each option as a plain <button>, so match the label
    // exactly — a loose match would also hit the trigger.
    await user.click(
      screen.getByRole("button", { name: enIssues.status.done }),
    );

    expect(mutate).toHaveBeenCalledWith(
      expect.objectContaining({ id: "i-1", status: "done" }),
    );
  });

  // A viewer without manage rights still needs to read the run's state; what
  // they must not get is a control that fails on the server.
  it("renders status read-only when the user cannot manage the node", () => {
    renderList([issue({ id: "i-1" })], false);

    expect(screen.queryByRole("button", { name: /in review/i })).toBeNull();
    expect(screen.getByText("MUL-1")).toBeInTheDocument();
  });

  it("still offers a way into the issue itself", () => {
    renderList([issue({ id: "i-1", identifier: "MUL-1" })]);

    expect(screen.getByRole("link", { name: /MUL-1/ })).toHaveAttribute(
      "href",
      "/issues/i-1",
    );
  });

  // "Nothing is blocking" is a result, not an absence: rendering an empty area
  // would read as "still loading" at the exact moment the node is ready.
  it("says so when nothing blocks the node", () => {
    renderList([]);
    expect(screen.getByText(enWorkflows.workbench.node_issues_clear))
      .toBeInTheDocument();
  });
});

describe("WorkflowNodeIssues completion block", () => {
  // The rule, the count and the action are one question — "can this node move,
  // and if not, why" — so they have to arrive together.
  it("carries the rule, the count and the action alongside the list", () => {
    render(
      <I18nProvider
        locale="en"
        resources={{ en: { workflows: enWorkflows, issues: enIssues } }}
      >
        <WorkflowNodeIssues
          issues={[issue({ id: "i-1" })]}
          canManage
          rule="All required issues are done"
          completed={0}
          total={1}
          action={<button type="button">Complete node</button>}
        />
      </I18nProvider>,
    );

    expect(screen.getByText("0/1")).toBeInTheDocument();
    expect(screen.getByText("All required issues are done")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Complete node" }))
      .toBeInTheDocument();
  });

  // An empty area reads as "not loaded"; being clear is a result worth saying.
  it("states the clear case instead of rendering an empty area", () => {
    render(
      <I18nProvider
        locale="en"
        resources={{ en: { workflows: enWorkflows, issues: enIssues } }}
      >
        <WorkflowNodeIssues issues={[]} canManage rule="rule text" total={0} />
      </I18nProvider>,
    );

    expect(screen.getByText(enWorkflows.workbench.node_issues_clear))
      .toBeInTheDocument();
    // A node with no required-issue rule shows no fraction at all.
    expect(screen.queryByText("0/0")).toBeNull();
  });
});
