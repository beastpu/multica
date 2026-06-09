import { describe, it, expect, beforeEach, vi } from "vitest";
import { cleanup, screen } from "@testing-library/react";
import { renderWithI18n } from "../../test/i18n";

// One row per issue (the backend already collapses to the latest run). Each
// carries the issue's workflow status (the "状态" column) and its most recent
// comment (the "原因/描述" column). The table must surface the agent, the
// issue identifier/title, the localized issue-status badge, and the comment.
const FIXES = vi.hoisted(() => [
  {
    task_id: "t-1",
    agent_id: "a-1",
    agent_name: "Fixer",
    issue_id: "i-1",
    issue_identifier: "MUL-7",
    issue_title: "Login broke",
    issue_status: "in_review",
    last_comment: "looks good, ready for review",
    last_comment_author_type: "member",
    started_at: null,
    completed_at: "2026-06-01T00:00:00Z",
    created_at: "2026-06-01T00:00:00Z",
  },
  {
    task_id: "t-2",
    agent_id: "a-1",
    agent_name: "Fixer",
    issue_id: "i-2",
    issue_identifier: "MUL-8",
    issue_title: "Parser cleanup",
    issue_status: "done",
    last_comment: "",
    last_comment_author_type: "",
    started_at: null,
    completed_at: "2026-06-02T00:00:00Z",
    created_at: "2026-06-02T00:00:00Z",
  },
  // An issue status the client doesn't know — must downgrade to the raw
  // string, never crash (API Response Compatibility: enum drift downgrades).
  {
    task_id: "t-3",
    agent_id: "a-1",
    agent_name: "Fixer",
    issue_id: "i-3",
    issue_identifier: "MUL-9",
    issue_title: "Future work",
    issue_status: "triaged",
    last_comment: "",
    last_comment_author_type: "",
    started_at: null,
    completed_at: null,
    created_at: "2026-06-03T00:00:00Z",
  },
]);

const AGENTS = vi.hoisted(() => [{ id: "a-1", name: "Fixer" }]);

// useQuery is keyed: the operations-fixes options carry "operations-fixes" in
// their key; the agent list carries "agents". Branch so each query resolves to
// its own fixture without dragging the real api client in.
vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[] }) => {
      if (opts.queryKey.includes("operations-fixes")) {
        return { data: FIXES, isLoading: false };
      }
      if (opts.queryKey.includes("agents")) {
        return { data: AGENTS, isLoading: false };
      }
      return { data: undefined, isLoading: false };
    },
  };
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// Pin the viewing timezone to UTC so the by-day column is deterministic
// (useViewingTimezone reads the auth store's user.timezone).
vi.mock("@multica/core/auth", () => {
  const state = () => ({ user: { timezone: "UTC" } });
  const useAuthStore = Object.assign(
    (sel?: (s: ReturnType<typeof state>) => unknown) =>
      sel ? sel(state()) : state(),
    { getState: state },
  );
  return { useAuthStore };
});

vi.mock("@multica/core/paths", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/paths")>(
      "@multica/core/paths",
    );
  return {
    ...actual,
    useWorkspaceSlug: () => "acme",
  };
});

vi.mock("../../navigation", () => ({
  AppLink: ({ children, href, ...props }: any) => (
    <a href={href} {...props}>
      {children}
    </a>
  ),
  useNavigation: () => ({ push: vi.fn(), pathname: "/acme/usage" }),
  NavigationProvider: ({ children }: { children: React.ReactNode }) => children,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid="actor-avatar">{actorId}</span>
  ),
}));

import { OperationsPage } from "./operations-page";

describe("OperationsPage", () => {
  beforeEach(() => cleanup());

  it("renders one row per issue with agent, issue, status, and last comment", () => {
    renderWithI18n(<OperationsPage />);

    // Issue identifier + title, linked to the issue detail under the slug.
    const issueLink = screen.getByText("Login broke").closest("a");
    expect(issueLink).not.toBeNull();
    expect(issueLink?.getAttribute("href")).toBe("/acme/issues/MUL-7");
    expect(screen.getByText("MUL-7")).toBeTruthy();
    expect(screen.getByText("MUL-8")).toBeTruthy();
    expect(screen.getByText("Parser cleanup")).toBeTruthy();

    // Agent name appears for each row.
    expect(screen.getAllByText("Fixer").length).toBe(3);

    // "状态" column = ISSUE workflow status (labels from the issues namespace).
    expect(screen.getByText("In Review")).toBeTruthy();
    expect(screen.getByText("Done")).toBeTruthy();

    // "原因/描述" column = the issue's most recent comment; the dash when none.
    expect(screen.getByText("looks good, ready for review")).toBeTruthy();
    expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(1);

    // By-day time column (UTC) renders the latest-run day per row.
    expect(screen.getByText("2026-06-01")).toBeTruthy();
    expect(screen.getByText("2026-06-02")).toBeTruthy();
  });

  it("downgrades an unknown issue status to its raw string instead of crashing", () => {
    renderWithI18n(<OperationsPage />);
    // The unknown status renders verbatim (no i18n key, no throw).
    expect(screen.getByText("triaged")).toBeTruthy();
    expect(screen.getByText("Future work")).toBeTruthy();
  });
});
