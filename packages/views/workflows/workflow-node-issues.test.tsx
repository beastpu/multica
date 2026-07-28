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

  // A node with no issues should not leave an empty heading behind.
  it("renders nothing when the node produced no issues", () => {
    const { container } = renderList([]);
    expect(container).toBeEmptyDOMElement();
  });
});
