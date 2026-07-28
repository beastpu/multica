// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { IssueOpenProvider, useIssueOpenClick } from "./issue-open-context";

function Card({ issueId }: { issueId: string }) {
  const openInPlace = useIssueOpenClick(issueId);
  return (
    <a href={`/issues/${issueId}`} onClick={openInPlace}>
      open {issueId}
    </a>
  );
}

describe("useIssueOpenClick", () => {
  // Without a provider the card must stay an ordinary link — every surface
  // outside the workbench depends on that.
  it("leaves the link alone when no surface handles opening", async () => {
    const user = userEvent.setup();
    render(<Card issueId="i-1" />);

    const link = screen.getByRole("link");
    const clickEvent = new MouseEvent("click", {
      bubbles: true,
      cancelable: true,
    });
    link.dispatchEvent(clickEvent);
    await user.click(link);

    expect(clickEvent.defaultPrevented).toBe(false);
  });

  it("hands a plain click to the surface instead of navigating", async () => {
    const onOpenIssue = vi.fn();
    const user = userEvent.setup();
    render(
      <IssueOpenProvider onOpenIssue={onOpenIssue}>
        <Card issueId="i-1" />
      </IssueOpenProvider>,
    );

    const link = screen.getByRole("link");
    const clickEvent = new MouseEvent("click", {
      bubbles: true,
      cancelable: true,
    });
    link.dispatchEvent(clickEvent);

    expect(onOpenIssue).toHaveBeenCalledWith("i-1");
    expect(clickEvent.defaultPrevented).toBe(true);
    // The href survives so "copy link address" still yields a real URL.
    expect(link).toHaveAttribute("href", "/issues/i-1");
    await user.click(link);
  });

  // ⌘-click / ctrl-click must keep opening a real tab even on an intercepting
  // surface; swallowing it would break a habit users rely on.
  it.each([
    ["metaKey", { metaKey: true }],
    ["ctrlKey", { ctrlKey: true }],
    ["shiftKey", { shiftKey: true }],
  ])("lets a %s click through to the browser", (_label, modifiers) => {
    const onOpenIssue = vi.fn();
    render(
      <IssueOpenProvider onOpenIssue={onOpenIssue}>
        <Card issueId="i-1" />
      </IssueOpenProvider>,
    );

    const clickEvent = new MouseEvent("click", {
      bubbles: true,
      cancelable: true,
      ...modifiers,
    });
    screen.getByRole("link").dispatchEvent(clickEvent);

    expect(onOpenIssue).not.toHaveBeenCalled();
    expect(clickEvent.defaultPrevented).toBe(false);
  });
});
