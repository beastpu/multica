import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act, cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useOperationsViewStore } from "@multica/core/dashboard";
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
    external: {
      binding_id: "binding-1",
      work_item_id: "BUG-93218",
      status: "Done",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
      version: "1.7.2",
    },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      quality_prediction: "likely_correct",
      prediction_reasons: ["complete_usable"],
      confidence: 0.86,
      workstream: "rel_1.7.2/server",
      swarm_reviews: [{ review_id: "SW-11872", state: "approved" }],
      ai_shelved_cls: [282941],
      external_committed_cls: [283006],
      summary: "AI shelve was submitted as the final CL.",
      warnings: [],
    },
    human_review: {
      outcome: "accepted",
      reasons: ["complete_usable"],
      note: "",
      reviewed_at: "2026-06-01T01:00:00Z",
    },
    display_result_status: "accepted",
    ai_judgement_eval: "match",
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
    external: {
      binding_id: "binding-2",
      work_item_id: "BUG-93219",
      status: "Done",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
    },
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
    external: {
      binding_id: "binding-3",
      work_item_id: "BUG-99999",
      status: "In Progress",
      mapped_status: "in_progress",
      done: false,
      project: "Future",
    },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "robot_wrote_it",
      quality_prediction: "surprisingly_fine",
      confidence: 0.42,
      workstream: "rel_future/server",
      swarm_reviews: [{ review_id: "SW-00000" }],
      ai_shelved_cls: ["999001"],
      external_committed_cls: ["999002"],
    },
    human_review: {
      outcome: "mystery_outcome",
      reasons: ["new_reason"],
    },
    display_result_status: "brand_new_display_state",
    ai_judgement_eval: "brand_new_eval",
  },
  {
    task_id: "t-4",
    agent_id: "a-2",
    agent_name: "Reviewer",
    issue_id: "i-4",
    issue_identifier: "MUL-10",
    issue_title: "Client crash",
    issue_status: "in_review",
    last_comment: "needs follow-up validation",
    last_comment_author_type: "agent",
    started_at: null,
    completed_at: "2026-06-04T00:00:00Z",
    created_at: "2026-06-04T00:00:00Z",
    external: {
      binding_id: "binding-4",
      work_item_id: "BUG-10000",
      status: "Done",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
      version: "1.7.3",
    },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "human_delivered",
      quality_prediction: "likely_wrong",
      prediction_reasons: ["wrong_direction"],
      confidence: 0.64,
      workstream: "rel_1.7.3/client",
      swarm_reviews: [{ review_id: "SW-11900", state: "needsReview" }],
      ai_shelved_cls: [283111],
      external_committed_cls: [283222],
      warnings: ["final CL differs from AI shelve"],
    },
    human_review: {
      outcome: "needs_changes",
      reasons: ["coverage_incomplete"],
      note: "Needs more validation.",
      reviewed_at: "2026-06-04T01:00:00Z",
    },
    display_result_status: "needs_changes",
    ai_judgement_eval: "overestimated",
  },
]);

const AGENTS = vi.hoisted(() => [
  { id: "a-1", name: "Fixer" },
  { id: "a-2", name: "Reviewer" },
]);

const TRIGGER_ASSESSMENT = vi.hoisted(() => vi.fn());

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
    useMutation: (opts: {
      mutationFn: (vars: any) => unknown | Promise<unknown>;
    }) => ({
      mutate: (vars: any, callbacks?: any) => {
        TRIGGER_ASSESSMENT(vars);
        Promise.resolve(opts.mutationFn(vars))
          .then((result) => callbacks?.onSuccess?.(result, vars, undefined))
          .catch((err) => callbacks?.onError?.(err, vars, undefined))
          .finally(() => callbacks?.onSettled?.(undefined, null, vars, undefined));
      },
      isPending: false,
    }),
    useQueryClient: () => ({
      invalidateQueries: vi.fn(),
    }),
  };
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    updateAgentFixReview: vi.fn().mockResolvedValue({
      outcome: "accepted",
      reasons: [],
      note: "",
      reviewer_id: "u-1",
      reviewed_at: "2026-06-01T01:00:00Z",
    }),
    triggerAgentFixP4Assessment: vi.fn().mockResolvedValue({
      created: true,
      reason: "created",
      assessment_status: "pending",
      task_id: "task-new",
    }),
  },
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

import {
  OperationsPage,
  buildOperationsP4AssessmentCsv,
  splitHighlight,
} from "./operations-page";

let exportedBlob: Blob | null = null;

describe("OperationsPage", () => {
  beforeEach(() => {
    cleanup();
    // Each test starts from the default column layout, regardless of prior runs.
    useOperationsViewStore.getState().resetColumnWidths();
    TRIGGER_ASSESSMENT.mockClear();
    exportedBlob = null;
    Object.defineProperty(URL, "createObjectURL", {
      configurable: true,
      value: vi.fn((blob: Blob) => {
        exportedBlob = blob;
        return "blob:operations-p4-assessment";
      }),
    });
    Object.defineProperty(URL, "revokeObjectURL", {
      configurable: true,
      value: vi.fn(),
    });
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

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
    expect(screen.getByText("Reviewer")).toBeTruthy();

    // "状态" column = ISSUE workflow status (labels from the issues namespace).
    expect(screen.getAllByText("In Review").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Done").length).toBeGreaterThanOrEqual(1);

    // "原因/描述" column = the issue's most recent comment; the dash when none.
    expect(screen.getByText("looks good, ready for review")).toBeTruthy();
    expect(screen.getAllByText("—").length).toBeGreaterThanOrEqual(1);

    // By-day time column (UTC) renders the latest-run day per row.
    expect(screen.getByText("2026-06-01")).toBeTruthy();
    expect(screen.getByText("2026-06-02")).toBeTruthy();
  });

  it("renders demo-like P4 assessment evidence and review outcomes", () => {
    renderWithI18n(<OperationsPage />);

    expect(screen.getByText("AI fix assessment")).toBeTruthy();
    expect(screen.getByText("Operations · P4 assessment")).toBeTruthy();
    expect(screen.getByText("P4 details")).toBeTruthy();
    expect(screen.getByText("Analysis report")).toBeTruthy();
    expect(screen.getByText("External done")).toBeTruthy();
    expect(screen.getByText("P4 coverage")).toBeTruthy();
    expect(screen.getByText("BUG-93218")).toBeTruthy();
    expect(screen.getAllByText("Done").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("In stats").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("AI assessed").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("stream rel_1.7.2/server")).toBeTruthy();
    expect(screen.getByText("Swarm SW-11872")).toBeTruthy();
    expect(screen.getByText("shelve 282941")).toBeTruthy();
    expect(screen.getByText("final CL 283006")).toBeTruthy();
    expect(screen.getAllByText("AI delivered").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Likely correct")).toBeTruthy();
    expect(screen.getByText("confidence 86%")).toBeTruthy();
    expect(screen.getAllByText("Accepted").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Accurate")).toBeTruthy();
  });

  it("shows assessment trigger actions only for external done bindings", () => {
    renderWithI18n(<OperationsPage />);

    expect(screen.getByText("Run assessment")).toBeTruthy();
    expect(screen.getAllByText("Rerun assessment").length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText("Assessment running")).toBeNull();

    const futureRow = screen.getByText("Future work").closest(".grid");
    expect(futureRow).not.toBeNull();
    expect(within(futureRow as HTMLElement).queryByText("Run assessment")).toBeNull();
    expect(within(futureRow as HTMLElement).queryByText("Rerun assessment")).toBeNull();
  });

  it("triggers a new assessment with binding_id and force=false", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.click(screen.getByText("Run assessment"));

    await waitFor(() => {
      expect(TRIGGER_ASSESSMENT).toHaveBeenCalledWith({
        binding_id: "binding-2",
        force: false,
      });
    });
  });

  it("reruns a completed assessment with binding_id and force=true", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.click(screen.getAllByText("Rerun assessment")[0]!);

    await waitFor(() => {
      expect(TRIGGER_ASSESSMENT).toHaveBeenCalledWith({
        binding_id: "binding-1",
        force: true,
      });
    });
  });

  it("switches to the lightweight analysis report tab", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.click(screen.getByText("Analysis report"));

    expect(screen.getByText("AI delivery attribution")).toBeTruthy();
    expect(screen.getByText("Human review outcome")).toBeTruthy();
    expect(screen.getByText("AI judgement eval")).toBeTruthy();
    expect(screen.getByText("Workstream outcome")).toBeTruthy();
    expect(screen.getByText("Top human reasons")).toBeTruthy();
    expect(screen.getByText("Top AI reasons")).toBeTruthy();
    expect(screen.getByText("rel_1.7.3/client")).toBeTruthy();
    expect(screen.getByText("Incomplete coverage")).toBeTruthy();
    expect(screen.getByText("Wrong direction")).toBeTruthy();
    expect(screen.getAllByText("AI delivered").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Accepted").length).toBeGreaterThanOrEqual(1);
  });

  it("filters assessment rows by workstream, predictions, and mismatch-only", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.click(screen.getByLabelText("Workstream"));
    await user.click(
      within(await screen.findByRole("listbox")).getByText("rel_1.7.3/client"),
    );

    expect(screen.getByText("Client crash")).toBeTruthy();
    expect(screen.queryByText("Login broke")).toBeNull();

    await user.click(screen.getByLabelText("AI attribution"));
    await user.click(
      within(await screen.findByRole("listbox")).getByText("Human delivered"),
    );

    expect(screen.getByText("Client crash")).toBeTruthy();
    expect(screen.queryByText("Login broke")).toBeNull();

    await user.click(screen.getByLabelText("AI quality"));
    await user.click(
      within(await screen.findByRole("listbox")).getByText("Likely wrong"),
    );

    expect(screen.getByText("Client crash")).toBeTruthy();
    expect(screen.queryByText("Login broke")).toBeNull();

    await user.click(screen.getByText("Mismatch only"));

    expect(screen.getByText("Client crash")).toBeTruthy();
    expect(screen.getByText("AI overestimated")).toBeTruthy();
    expect(screen.queryByText("Login broke")).toBeNull();
  });

  it("exports the current filtered P4 assessment rows as CSV", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.click(screen.getByLabelText("Workstream"));
    await user.click(
      within(await screen.findByRole("listbox")).getByText("rel_1.7.3/client"),
    );
    await user.click(screen.getByText("Export CSV"));

    expect(exportedBlob).not.toBeNull();
    const csv = await exportedBlob!.text();
    expect(csv).toContain("Issue,Issue Title,External Work Item ID");
    expect(csv).toContain("MUL-10,Client crash,BUG-10000");
    expect(csv).toContain("rel_1.7.3/client");
    expect(csv).toContain("SW-11900");
    expect(csv).toContain("283111");
    expect(csv).toContain("283222");
    expect(csv).toContain("needs_changes");
    expect(csv).toContain("coverage_incomplete");
    expect(csv).toContain("overestimated");
    expect(csv).not.toContain("Login broke");
  });

  it("renders a resize handle for each sizable column (agent, issue, status)", () => {
    renderWithI18n(<OperationsPage />);
    const handles = screen.getAllByRole("separator");
    expect(handles.length).toBe(3);
    // Reason (flex filler) and Date (fixed, last) are not resizable.
    expect(
      handles.map((h) => h.getAttribute("aria-label")).every(Boolean),
    ).toBe(true);
  });

  it("shows the reset control only after a column is resized, then hides it again", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    // Default layout: no reset affordance.
    expect(screen.queryByText("Reset columns")).toBeNull();

    // Simulate a drag commit by writing a width to the store.
    act(() => {
      useOperationsViewStore.getState().setColumnWidth("issue", 480);
    });
    expect(screen.getByText("Reset columns")).toBeTruthy();

    // Clicking it restores defaults and the control disappears.
    await user.click(screen.getByText("Reset columns"));
    await waitFor(() => {
      expect(screen.queryByText("Reset columns")).toBeNull();
    });
  });

  it("downgrades an unknown issue status to its raw string instead of crashing", () => {
    renderWithI18n(<OperationsPage />);
    // The unknown status renders verbatim (no i18n key, no throw).
    expect(screen.getByText("triaged")).toBeTruthy();
    expect(screen.getByText("Future work")).toBeTruthy();
    expect(screen.getByText("robot_wrote_it")).toBeTruthy();
    expect(screen.getByText("surprisingly_fine")).toBeTruthy();
    expect(screen.getByText("mystery_outcome")).toBeTruthy();
    expect(screen.getByText("brand_new_eval")).toBeTruthy();
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

describe("buildOperationsP4AssessmentCsv", () => {
  it("writes a BOM, stable headers, escaped cells, arrays, and sparse fields", () => {
    const csv = buildOperationsP4AssessmentCsv([
      {
        ...(FIXES[0] as any),
        issue_title: 'Login, "broke"',
        human_review: {
          ...((FIXES[0] as any).human_review ?? {}),
          note: "first line\nsecond line",
        },
      },
      { ...(FIXES[1] as any), external: undefined },
    ]);

    expect(csv.startsWith("\uFEFF")).toBe(true);
    expect(csv).toContain("Issue,Issue Title,External Work Item ID");
    expect(csv).toContain('MUL-7,"Login, ""broke""",BUG-93218');
    expect(csv).toContain('"first line\nsecond line"');
    expect(csv).toContain("SW-11872");
    expect(csv).toContain("282941");
    expect(csv).toContain("283006");
    expect(csv).toContain("MUL-8,Parser cleanup,,,,,,,,Fixer");
  });
});
