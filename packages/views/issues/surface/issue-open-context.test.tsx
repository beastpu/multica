// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { IssueOpenProvider, useIssueOpenIntercept } from "./issue-open-context";

function Card({ issueId }: { issueId: string }) {
  const intercept = useIssueOpenIntercept(issueId);
  // Mirrors how AppLink consults intercept, so the test exercises the shape
  // the card actually hands over rather than a click handler of its own.
  return (
    <button
      type="button"
      data-handled={intercept ? String(intercept()) : "none"}
    >
      open {issueId}
    </button>
  );
}

describe("useIssueOpenIntercept", () => {
  // Every surface outside the workbench must keep routing normally, and it
  // signals that by handing AppLink no intercept at all.
  it("returns nothing when no surface handles opening", () => {
    render(<Card issueId="i-1" />);
    expect(screen.getByRole("button")).toHaveAttribute("data-handled", "none");
  });

  it("claims the click and opens the issue on the surface", () => {
    const onOpenIssue = vi.fn();
    render(
      <IssueOpenProvider onOpenIssue={onOpenIssue}>
        <Card issueId="i-1" />
      </IssueOpenProvider>,
    );

    // Returning true is what tells AppLink to skip the push.
    expect(screen.getByRole("button")).toHaveAttribute("data-handled", "true");
    expect(onOpenIssue).toHaveBeenCalledWith("i-1");
  });
});
