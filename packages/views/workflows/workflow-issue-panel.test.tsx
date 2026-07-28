// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import type { Issue } from "@multica/core/types";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import enWorkflows from "../locales/en/workflows.json";
import { WorkflowIssuePanel } from "./workflow-issue-panel";

vi.mock("../issues/components", () => ({
  IssueDetail: ({ issueId }: { issueId: string }) => (
    <div data-testid="issue-detail">{issueId}</div>
  ),
}));

const issues = [
  { id: "i-1", identifier: "MUL-1" },
  { id: "i-2", identifier: "MUL-2" },
  { id: "i-3", identifier: "MUL-3" },
] as Issue[];

function renderPanel(
  issueId: string,
  handlers: { onClose?: () => void; onNavigate?: (id: string) => void } = {},
) {
  return render(
    <I18nProvider locale="en" resources={{ en: { workflows: enWorkflows } }}>
      <WorkflowIssuePanel
        issueId={issueId}
        issues={issues}
        onClose={handlers.onClose ?? vi.fn()}
        onNavigate={handlers.onNavigate ?? vi.fn()}
      />
    </I18nProvider>,
  );
}

describe("WorkflowIssuePanel", () => {
  it("shows the issue and its position within the node", () => {
    renderPanel("i-2");

    expect(screen.getByTestId("issue-detail")).toHaveTextContent("i-2");
    expect(screen.getByText("2/3")).toBeInTheDocument();
  });

  // Sweeping a node means going issue to issue without returning to the board
  // between each one — that round trip is what the panel removes.
  it("moves to the next and previous issue in the node", async () => {
    const onNavigate = vi.fn();
    const user = userEvent.setup();
    renderPanel("i-2", { onNavigate });

    await user.click(screen.getByRole("button", { name: /next issue/i }));
    expect(onNavigate).toHaveBeenCalledWith("i-3");

    await user.click(screen.getByRole("button", { name: /previous issue/i }));
    expect(onNavigate).toHaveBeenCalledWith("i-1");
  });

  it("stops at the ends instead of wrapping", () => {
    const { unmount } = renderPanel("i-1");
    expect(screen.getByRole("button", { name: /previous issue/i })).toBeDisabled();
    unmount();

    renderPanel("i-3");
    expect(screen.getByRole("button", { name: /next issue/i })).toBeDisabled();
  });

  it("returns to the node view", async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    renderPanel("i-2", { onClose });

    await user.click(screen.getByRole("button", { name: /back to node/i }));
    expect(onClose).toHaveBeenCalled();
  });

  // A single-issue node should not carry navigation it cannot use.
  it("hides navigation when the node has one issue", () => {
    render(
      <I18nProvider locale="en" resources={{ en: { workflows: enWorkflows } }}>
        <WorkflowIssuePanel
          issueId="i-1"
          issues={[issues[0]!]}
          onClose={vi.fn()}
          onNavigate={vi.fn()}
        />
      </I18nProvider>,
    );

    expect(screen.queryByRole("button", { name: /next issue/i })).toBeNull();
  });
});
