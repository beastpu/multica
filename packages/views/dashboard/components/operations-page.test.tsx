import { describe, it, expect, beforeEach, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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
    last_comment_author_type: "agent",
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
// their key, with the debounced search term as the last key segment; the agent
// list carries "agents". Branch so each query resolves to its own fixture
// without dragging the real api client in. The fixes branch mirrors the server
// filter — a case-insensitive substring on last_comment — so typing in the
// search box narrows the rendered rows exactly as the backend would.
vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[] }) => {
      if (opts.queryKey.includes("operations-fixes")) {
        const term = String(
          opts.queryKey[opts.queryKey.length - 1] ?? "",
        ).toLowerCase();
        const data = term
          ? FIXES.filter((f) =>
              (f.last_comment ?? "").toLowerCase().includes(term),
            )
          : FIXES;
        return { data, isLoading: false };
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

import { OperationsPage, splitHighlight } from "./operations-page";

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

  it("filters rows by the comment search term and highlights the match", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    // All three issues present before searching.
    expect(screen.getByText("Login broke")).toBeTruthy();
    expect(screen.getByText("Parser cleanup")).toBeTruthy();

    await user.type(screen.getByLabelText("Search comments"), "review");

    // After the debounce, only the issue whose comment contains "review"
    // survives; the others drop out of the table.
    await waitFor(() => {
      expect(screen.getByText("Login broke")).toBeTruthy();
      expect(screen.queryByText("Parser cleanup")).toBeNull();
    });

    // The matched term is wrapped in a <mark> for highlighting.
    const marks = document.querySelectorAll("mark");
    expect(marks.length).toBeGreaterThanOrEqual(1);
    expect(
      Array.from(marks).some((m) => m.textContent?.toLowerCase() === "review"),
    ).toBe(true);
  });

  it("shows a search-specific empty state when nothing matches", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.type(
      screen.getByLabelText("Search comments"),
      "nonexistent-term",
    );

    await waitFor(() => {
      // Search-aware empty copy, not the default "No fixes yet".
      expect(screen.getByText("No matching comments")).toBeTruthy();
      expect(screen.queryByText("No fixes yet")).toBeNull();
    });
  });

  it("clears the search with the clear button, restoring all rows", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.type(screen.getByLabelText("Search comments"), "review");
    await waitFor(() => {
      expect(screen.queryByText("Parser cleanup")).toBeNull();
    });

    await user.click(screen.getByLabelText("Clear search"));
    await waitFor(() => {
      expect(screen.getByText("Parser cleanup")).toBeTruthy();
    });
  });
});

describe("splitHighlight", () => {
  it("returns a single plain part when the keyword is empty", () => {
    expect(splitHighlight("hello world", "")).toEqual([
      { text: "hello world", match: false },
    ]);
  });

  it("returns a single plain part when there is no match", () => {
    expect(splitHighlight("hello world", "xyz")).toEqual([
      { text: "hello world", match: false },
    ]);
  });

  it("marks every case-insensitive occurrence, preserving original casing", () => {
    const parts = splitHighlight("Review then review again", "review");
    expect(parts).toEqual([
      { text: "Review", match: true },
      { text: " then ", match: false },
      { text: "review", match: true },
      { text: " again", match: false },
    ]);
    // Reassembling the parts must reproduce the original string exactly.
    expect(parts.map((p) => p.text).join("")).toBe("Review then review again");
  });

  it("treats the keyword literally (no regex/wildcard semantics)", () => {
    const parts = splitHighlight("100% done now", "%");
    expect(parts.filter((p) => p.match).map((p) => p.text)).toEqual(["%"]);
  });
});
