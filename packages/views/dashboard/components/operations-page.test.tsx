import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act, cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useOperationsViewStore } from "@multica/core/dashboard";
import { renderWithI18n } from "../../test/i18n";

// Fixture timestamps are relative to "now" so the page's trailing-window trim
// stays deterministic no matter when the tests run. All primary rows sit
// inside the default 30d window; the t-8 fixture sits outside it.
const dayIso = vi.hoisted(() => {
  return (daysAgo: number) =>
    new Date(Date.now() - daysAgo * 86_400_000).toISOString();
});
const dayLabel = vi.hoisted(() => {
  return (daysAgo: number) =>
    new Date(Date.now() - daysAgo * 86_400_000).toISOString().slice(0, 10);
});

// One row per issue (the backend already collapses to the latest run). Each
// carries the issue's workflow status and its most recent comment. The table
// must surface the agent, the issue identifier/title, the localized
// issue-status badge, and the comment.
const FIXES = vi.hoisted(() => [
  {
    task_id: "t-1",
    agent_id: "a-1",
    agent_name: "Fixer",
    issue_id: "i-1",
    issue_identifier: "MUL-7",
    issue_title: "Login broke",
    issue_status: "done",
    last_comment: "looks good, ready for review",
    last_comment_author_type: "agent",
    started_at: null,
    completed_at: dayIso(6),
    created_at: dayIso(6),
    external: {
      binding_id: "binding-1",
      work_item_id: "BUG-93218",
      status: "test-passed",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
      version: "1.7.2",
      url: "https://meego.example.com/items/BUG-93218",
    },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      quality_prediction: "likely_correct",
      prediction_reasons: ["complete_usable"],
      confidence: 0.86,
      workstream: "rel_1.7.2/server",
      swarm_reviews: [
        {
          review_id: "SW-11872",
          state: "approved",
          changes: [282941, 282944],
          commits: [283006],
          swarm_branch: "main",
          event_type: "review.committed",
          sent_at: "2026-06-01T00:30:00Z",
        },
      ],
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
    completed_at: dayIso(5),
    created_at: dayIso(5),
    external: {
      binding_id: "binding-2",
      work_item_id: "BUG-93219",
      status: "test-passed",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
      final_cl: "284805",
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
    created_at: dayIso(4),
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
  },
  {
    task_id: "t-4",
    agent_id: "a-2",
    agent_name: "Reviewer",
    issue_id: "i-4",
    issue_identifier: "MUL-10",
    issue_title: "Client crash",
    issue_status: "done",
    last_comment: "needs follow-up validation",
    last_comment_author_type: "agent",
    started_at: null,
    completed_at: dayIso(3),
    created_at: dayIso(3),
    external: {
      binding_id: "binding-4",
      work_item_id: "BUG-10000",
      status: "test-passed",
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
      swarm_reviews: [
        {
          review_id: "SW-11900",
          state: "needsReview",
          changes: [283111, 283112],
          commits: [],
          swarm_branch: "release/client",
          event_type: "review.updated",
          sent_at: "2026-06-04T00:30:00Z",
        },
      ],
      ai_shelved_cls: [283111],
      external_committed_cls: [283222],
      warnings: ["final CL differs from AI shelve", "p4_shelve_unavailable"],
    },
    human_review: {
      outcome: "needs_changes",
      reasons: ["coverage_incomplete"],
      note: "Needs more validation.",
      reviewed_at: "2026-06-04T01:00:00Z",
    },
  },
  {
    task_id: "t-5",
    agent_id: "a-1",
    agent_name: "Fixer",
    issue_id: "i-5",
    issue_identifier: "MUL-11",
    issue_title: "Assessment parser failed",
    issue_status: "done",
    last_comment: "",
    last_comment_author_type: "",
    started_at: null,
    completed_at: dayIso(2),
    created_at: dayIso(2),
    external: {
      binding_id: "binding-5",
      work_item_id: "BUG-10001",
      status: "test-passed",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
    },
    p4_assessment: {
      assessment_status: "failed",
      delivery_attribution_prediction: "unknown",
      quality_prediction: "unknown",
      warnings: ["parser_error: expected a JSON object or one fenced json block"],
      attempt_count: 3,
      last_error: "task output parse failed: no fenced json block",
    },
  },
  {
    task_id: "t-6",
    agent_id: "a-1",
    agent_name: "Fixer",
    issue_id: "i-6",
    issue_identifier: "WAR-9581",
    issue_title: "Shelved CL should not look final",
    issue_status: "done",
    last_comment:
      "CL 287451 已 shelve，修复 FPS 求助分享在 IM 发送失败时仍记录 MsgID=0 的问题。",
    last_comment_author_type: "agent",
    started_at: null,
    completed_at: null,
    created_at: dayIso(1),
    external: {
      binding_id: "binding-6",
      work_item_id: "7035395614",
      status: "test-passed",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
      workstream: "rel_1.1.0",
    },
  },
  // A completed assessment with NO AI shelve — derives to "AI no output".
  {
    task_id: "t-7",
    agent_id: "a-1",
    agent_name: "Fixer",
    issue_id: "i-7",
    issue_identifier: "MUL-12",
    issue_title: "Auth blocked the agent",
    issue_status: "done",
    last_comment: "",
    last_comment_author_type: "",
    started_at: null,
    completed_at: dayIso(2),
    created_at: dayIso(2),
    external: {
      binding_id: "binding-7",
      work_item_id: "BUG-10002",
      status: "test-passed",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
    },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "unattributed",
      quality_prediction: "unknown",
      ai_shelved_cls: [],
      // One data-gap warning (missing_external_cl — not an access block) and
      // one access block (auth family), so the coverage drawer's blocked
      // section has a row while the process-gaps card keeps its count.
      warnings: ["missing_external_cl", "swarm_api_unauthorized"],
    },
  },
  // External done, synced from Meegle, but no Agent was assigned and no normal
  // repair task exists. The Operations opportunity pool is explicitly scoped
  // to current Agent assignments, so this row must stay out of every metric.
  {
    task_id: "",
    agent_id: "",
    agent_name: "",
    issue_id: "i-9",
    issue_identifier: "MUL-13",
    issue_title: "No agent pickup",
    issue_status: "done",
    started_at: null,
    completed_at: null,
    created_at: dayIso(1),
    external: {
      binding_id: "binding-9",
      work_item_id: "BUG-10003",
      status: "test-passed",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
    },
  },
  // A different Feishu terminal state may still map to done, but it is not
  // part of the Operations denominator unless its raw status is 测试通过.
  {
    task_id: "t-10",
    agent_id: "a-1",
    agent_name: "Fixer",
    issue_id: "i-10",
    issue_identifier: "MUL-101",
    issue_title: "Closed without test pass",
    issue_status: "done",
    started_at: null,
    completed_at: dayIso(1),
    created_at: dayIso(1),
    external: {
      binding_id: "binding-10",
      work_item_id: "BUG-10101",
      status: "Done",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
    },
  },
  // Feishu has reached 测试通过, but the synced Multica issue has not reached
  // done yet. Both sides of the intersection are required.
  {
    task_id: "t-11",
    agent_id: "a-1",
    agent_name: "Fixer",
    issue_id: "i-11",
    issue_identifier: "MUL-102",
    issue_title: "Test passed but sync pending",
    issue_status: "in_progress",
    started_at: null,
    completed_at: dayIso(1),
    created_at: dayIso(1),
    external: {
      binding_id: "binding-11",
      work_item_id: "BUG-10102",
      status: "test-passed",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
    },
  },
  // Derived assessment issues can themselves be done, but without a Feishu
  // 测试通过 binding they must never inflate the denominator.
  {
    task_id: "t-12",
    agent_id: "a-1",
    agent_name: "Fixer",
    issue_id: "i-12",
    issue_identifier: "MUL-103",
    issue_title: "AI assessment projection",
    issue_status: "done",
    started_at: null,
    completed_at: dayIso(1),
    created_at: dayIso(1),
  },
  // Older than the selected 30d window — must never appear in the table
  // or the KPIs.
  {
    task_id: "t-8",
    agent_id: "a-1",
    agent_name: "Fixer",
    issue_id: "i-8",
    issue_identifier: "MUL-99",
    issue_title: "Previous period fix",
    issue_status: "done",
    last_comment: "",
    last_comment_author_type: "",
    started_at: null,
    completed_at: dayIso(40),
    created_at: dayIso(40),
    external: {
      binding_id: "binding-8",
      work_item_id: "BUG-777",
      status: "Done",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
    },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      quality_prediction: "likely_correct",
      ai_shelved_cls: [270001],
    },
    human_review: { outcome: "rejected", reasons: ["wrong_direction"] },
  },
]);

const AGENTS = vi.hoisted(() => [
  { id: "a-1", name: "Fixer" },
  { id: "a-2", name: "Reviewer" },
]);

const TRIGGER_ASSESSMENT = vi.hoisted(() => vi.fn());
const FIXES_QUERY_ERROR = vi.hoisted(() => ({ value: false }));
const REFRESH_FIXES = vi.hoisted(() => vi.fn());

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
        if (FIXES_QUERY_ERROR.value) {
          return {
            data: undefined,
            isLoading: false,
            isError: true,
            refetch: REFRESH_FIXES,
          };
        }
        const term = String(
          opts.queryKey[opts.queryKey.length - 1] ?? "",
        ).toLowerCase();
        const data = term
          ? FIXES.filter((f) =>
              (f.last_comment ?? "").toLowerCase().includes(term),
            )
          : FIXES;
        return {
          data,
          isLoading: false,
          isError: false,
          refetch: REFRESH_FIXES,
        };
      }
      if (opts.queryKey.includes("agents")) {
        return { data: AGENTS, isLoading: false };
      }
      if (opts.queryKey.includes("issue-statuses")) {
        return {
          data: {
            statuses: [
              { key: "test-passed", name: "测试通过" },
              { key: "Done", name: "Done" },
              { key: "In Progress", name: "In Progress" },
            ],
          },
          isLoading: false,
          isError: false,
          refetch: vi.fn(),
        };
      }
      if (
        opts.queryKey.includes("perforce") &&
        opts.queryKey.includes("connection")
      ) {
        return {
          data: {
            connection: {
              workspace_id: "ws-1",
              swarm_url: "https://swarm.example.com",
            },
            configured: true,
          },
          isLoading: false,
        };
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

import { OperationsPage, splitHighlight } from "./operations-page";

async function openAssessments(
  user: ReturnType<typeof userEvent.setup>,
  label = "Assessments",
) {
  // The detail table is now the only main-page surface; attribution and
  // quality distributions moved into their KPI drawers.
  void user;
  void label;
}

async function openRepairDetails(
  user: ReturnType<typeof userEvent.setup>,
  issueIdentifier: string,
) {
  await user.click(
    screen.getByRole("button", { name: new RegExp(issueIdentifier) }),
  );
}

describe("OperationsPage", () => {
  beforeEach(() => {
    cleanup();
    // Each test starts from the default column layout, regardless of prior runs.
    useOperationsViewStore.getState().resetColumnWidths();
    TRIGGER_ASSESSMENT.mockClear();
    FIXES_QUERY_ERROR.value = false;
    REFRESH_FIXES.mockClear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders one row per issue with agent, issue, status, and last comment", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    // Issue identifier + title, linked to the issue detail under the slug.
    const issueLink = screen.getByText("Login broke").closest("a");
    expect(issueLink).not.toBeNull();
    expect(issueLink?.getAttribute("href")).toBe("/acme/issues/MUL-7");
    expect(screen.getByText("MUL-7")).toBeTruthy();
    expect(screen.getByText("MUL-8")).toBeTruthy();
    expect(screen.getByText("Parser cleanup")).toBeTruthy();
    expect(screen.getAllByText("测试通过").length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText("test-passed")).toBeNull();

    // Agent name appears for each visible row. The unassigned row is outside
    // the reporting pool and therefore absent from the detail table.
    expect(screen.getAllByText("Fixer").length).toBe(5);
    expect(screen.getByText("Reviewer")).toBeTruthy();

    // "状态" column = ISSUE workflow status (labels from the issues namespace).
    expect(screen.getAllByText("Done").length).toBeGreaterThanOrEqual(1);

    // CL evidence and AI assessment are consolidated under Repair method.
    expect(screen.getByText("Repair method")).toBeTruthy();
    expect(screen.queryByText("Submitted CL record")).toBeNull();
    expect(screen.queryByText("AI assessment")).toBeNull();
    expect(screen.queryByText("Human delivered")).toBeNull();

    // Issue cell includes a compact preview of the most recent comment.
    expect(screen.getByText("looks good, ready for review")).toBeTruthy();

    // By-day time column (UTC) renders the latest-run day per row.
    expect(screen.getAllByText(dayLabel(6)).length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText(dayLabel(5)).length).toBeGreaterThanOrEqual(1);
  });

  it("excludes rows older than the selected window from the table", () => {
    renderWithI18n(<OperationsPage />);
    expect(screen.queryByText("Previous period fix")).toBeNull();
  });

  it("shows a recoverable error state when the operations feed fails", async () => {
    FIXES_QUERY_ERROR.value = true;
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    expect(screen.getByText("Could not load operations data")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Reload" }));
    expect(REFRESH_FIXES).toHaveBeenCalledTimes(2);
  });

  it("renders the four operator KPI cards", () => {
    renderWithI18n(<OperationsPage />);

    expect(screen.getByText("AI coverage")).toBeTruthy();
    expect(screen.getByText("AI repair quality")).toBeTruthy();
    expect(screen.getByText("AI automatic repair rate")).toBeTruthy();
    expect(screen.getByText("AI-assisted repair rate")).toBeTruthy();
    expect(
      screen.getByText("AI-marked passes / AI-assessed repairable tickets"),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "Direct AI submissions passed / AI-assessed repairable tickets",
      ),
    ).toBeTruthy();
    expect(screen.getByText("2 / 6")).toBeTruthy();
    expect(screen.getAllByText("1 / 2")).toHaveLength(2);
    expect(screen.getByText("0 / 2")).toBeTruthy();
    expect(screen.getByText(/unassessed · \d+ missing human CL/)).toBeTruthy();
  });

  it("keeps quality, automatic, and assisted rate cards informational", () => {
    renderWithI18n(<OperationsPage />);

    expect(
      screen.getByText("AI repair quality").closest("button"),
    ).toBeNull();
    expect(
      screen.getByText("AI automatic repair rate").closest("button"),
    ).toBeNull();
    expect(
      screen.getByText("AI-assisted repair rate").closest("button"),
    ).toBeNull();
  });

  it("renders a complete delivery composition without a duplicate overview", () => {
    renderWithI18n(<OperationsPage />);

    expect(
      screen.queryByText(
        "Last 30 days: 6 assigned to an Agent, 2 picked up by AI, and 2 judged for quality.",
      ),
    ).toBeNull();
    const composition = screen.getByRole("region", {
      name: "AI delivery composition",
    });
    expect(composition.textContent).toContain(
      "AI picked up 2 / 6 assigned to an Agent",
    );
    expect(composition.textContent).toContain("AI automatic repair1 · 17%");
    expect(composition.textContent).toContain(
      "Non-automatic AI involvement (including unconverted)1 · 17%",
    );
    expect(composition.textContent).toContain(
      "No verifiable plan found4 · 67%",
    );
  });

  it("keeps delivery attribution out of the contribution drawer", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    // The whole contribution card is a button opening the breakdown drawer.
    await user.click(
      screen.getByRole("button", { name: /AI coverage/ }),
    );
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).queryByText("Delivery attribution")).toBeNull();
    expect(
      within(dialog).queryByRole("button", { name: /AI automatic repair/ }),
    ).toBeNull();
    expect(
      within(dialog).queryByRole("button", { name: /AI-assisted/ }),
    ).toBeNull();
  });

  it("excludes items that are not currently assigned to an Agent", () => {
    renderWithI18n(<OperationsPage />);

    expect(screen.queryByText("No agent pickup")).toBeNull();
    expect(screen.getByText("2 / 6")).toBeTruthy();
  });

  it("shows assessment progress and no-plan reasons inside the coverage drawer", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.click(
      screen.getByRole("button", { name: /AI coverage/ }),
    );
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("Processed by AI")).toBeTruthy();
    expect(within(dialog).getByText("AI assessment completed")).toBeTruthy();
    expect(within(dialog).getByText("AI assessment blocked")).toBeTruthy();
    expect(within(dialog).getByText("Not processed by AI")).toBeTruthy();
    expect(within(dialog).getByText("Why no verifiable plan was found")).toBeTruthy();
    expect(within(dialog).queryByText("Why conversion is unconfirmed")).toBeNull();
    await user.click(
      within(dialog).getByRole("button", { name: /Plan assessment incomplete/ }),
    );
    expect(within(dialog).getByText("Parser cleanup")).toBeTruthy();
  });

  it("opens the issue drawer from a breakdown ticket card", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.click(
      screen.getByRole("button", { name: /AI coverage/ }),
    );
    const dialog = screen.getByRole("dialog");
    await user.click(
      within(dialog).getByRole("button", { name: /Processed by AI/ }),
    );
    await user.click(
      within(dialog).getByRole("button", { name: /Login broke/ }),
    );
    // Issue panel: summary and judgement-basis enum labels surface here.
    expect(
      within(dialog).getByText("AI shelve was submitted as the final CL."),
    ).toBeTruthy();
    expect(within(dialog).getByText("Complete")).toBeTruthy();
    // Back returns to the branch list.
    await user.click(within(dialog).getByRole("button", { name: "Back" }));
    expect(within(dialog).getByText("Login broke")).toBeTruthy();
  });

  it("opens the issue drawer from a detail-table row", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    // Click a non-interactive cell (the day text) of t-1's row; links and
    // buttons inside the row keep their own behavior.
    const [dayCell] = screen.getAllByText(dayLabel(6));
    expect(dayCell).toBeTruthy();
    await user.click(dayCell!);
    const dialog = screen.getByRole("dialog");
    expect(
      within(dialog).getByText("AI shelve was submitted as the final CL."),
    ).toBeTruthy();
    expect(within(dialog).getByText("Open issue")).toBeTruthy();
  });

  it("collapses internal non-AI attribution states to unable to determine", () => {
    renderWithI18n(<OperationsPage />);
    expect(screen.queryByText("AI no output")).toBeNull();
    expect(
      screen.getByRole("button", {
        name: "View repair details for MUL-12: Unable to determine",
      }),
    ).toBeTruthy();
  });

  it("renders demo-like P4 assessment evidence and quality analysis", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    expect(screen.getByText("AI fix assessment")).toBeTruthy();
    expect(screen.queryByText("Insights")).toBeNull();
    expect(screen.getByText("BUG-93218")).toBeTruthy();
    expect(screen.getAllByText("Done").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("In stats").length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText("Swarm SW-11872")).toBeNull();
    expect(screen.queryByText("final CL 283006")).toBeNull();

    await openRepairDetails(user, "MUL-7");
    expect(screen.getByText("Repair details")).toBeTruthy();
    expect(screen.getByText("Submitted CL record")).toBeTruthy();
    expect(screen.getByText("AI assessment")).toBeTruthy();
    expect(screen.getByText("AI assessed")).toBeTruthy();
    expect(screen.getByText("stream rel_1.7.2/server")).toBeTruthy();
    // The popover shows the submitted CL first; shelved CLs stay hidden when a
    // final committed CL exists.
    const badgeTexts = Array.from(document.querySelectorAll("span")).map((s) =>
      (s.textContent ?? "").replace(/\s+/g, " ").trim(),
    );
    expect(badgeTexts).toContain("final CL 283006");
    expect(badgeTexts).not.toContain("shelve 282941");
    expect(screen.queryByText("changes 282941, 282944")).toBeNull();

    await user.click(screen.getAllByRole("button", { name: "Details" })[0]!);
    expect(screen.getByText("SW-11872")).toBeTruthy();
    expect(screen.getByText("282941, 282944")).toBeTruthy();
    // "283006" also renders as the linked final-CL badge text in the popover.
    expect(screen.getAllByText("283006").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("main")).toBeTruthy();
    expect(screen.getByText("review.committed")).toBeTruthy();
    expect(screen.getByText("2026-06-01T00:30:00Z")).toBeTruthy();
    await user.keyboard("{Escape}");

    expect(screen.getAllByText("AI submitted").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Pass").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("confidence 86%")).toBeTruthy();
  });

  it("links external work items, swarm reviews, and CLs", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    // Feishu/Meego work item → external.url.
    const workItem = screen.getByText("BUG-93218").closest("a");
    expect(workItem?.getAttribute("href")).toBe(
      "https://meego.example.com/items/BUG-93218",
    );
    await openRepairDetails(user, "MUL-7");
    // Swarm review lives in the submitted-CL details popover.
    await user.click(screen.getAllByRole("button", { name: "Details" })[0]!);
    const swarm = screen.getByText("SW-11872").closest("a");
    expect(swarm?.getAttribute("href")).toBe(
      "https://swarm.example.com/reviews/SW-11872",
    );
    await user.keyboard("{Escape}");

    // Final CL → {base}/changes/{cl}.
    const cl = screen.getByText("283006").closest("a");
    expect(cl?.getAttribute("href")).toBe(
      "https://swarm.example.com/changes/283006",
    );
  });

  it("renders quality analysis copy in Chinese locale", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />, { locale: "zh-Hans" });
    await openAssessments(user, "评估明细");

    await openRepairDetails(user, "MUL-7");
    expect(screen.getByText("\u4fee\u590d\u65b9\u5f0f\u8be6\u60c5")).toBeTruthy();
    // Quality prediction moved into the localized repair-details popover.
    expect(screen.getAllByText("\u901a\u8fc7").length).toBeGreaterThanOrEqual(1);

    // The submitted-CL record popover still opens with the localized trigger.
    await user.click(screen.getAllByRole("button", { name: "\u8be6\u60c5" })[0]!);
    expect(screen.getByText("282941, 282944")).toBeTruthy();
  });

  it("counts only Feishu test-passed rows whose synced issue is done", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    // t-2 (done, no assessment yet) → enabled Run assessment.
    await openRepairDetails(user, "MUL-8");
    const runButtons = screen.getAllByRole("button", {
      name: "Queue assessment",
    });
    expect(runButtons.some((b) => !(b as HTMLButtonElement).disabled)).toBe(
      true,
    );

    // None of these satisfy both sides of the reporting intersection.
    expect(screen.queryByText("Future work")).toBeNull();
    expect(screen.queryByText("triaged")).toBeNull();
    expect(screen.queryByText("rel_future/server")).toBeNull();
    expect(screen.queryByText("Closed without test pass")).toBeNull();
    expect(screen.queryByText("Test passed but sync pending")).toBeNull();
    expect(screen.queryByText("AI assessment projection")).toBeNull();
  });

  it("triggers a new assessment with binding_id and force=false", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    await openRepairDetails(user, "MUL-8");
    const enabled = screen
      .getAllByRole("button", { name: "Queue assessment" })
      .find((b) => !(b as HTMLButtonElement).disabled)!;
    await user.click(enabled);

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
    await openAssessments(user);

    await openRepairDetails(user, "MUL-7");
    const enabled = screen
      .getAllByRole("button", { name: "Requeue assessment" })
      .find((b) => !(b as HTMLButtonElement).disabled)!;
    await user.click(enabled);

    await waitFor(() => {
      expect(TRIGGER_ASSESSMENT).toHaveBeenCalledWith({
        binding_id: "binding-1",
        force: true,
      });
    });
  });

  it("surfaces queue observability (attempts + last error) on failed assessments", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    // t-5 failed after 3 leases with a recorded reason — the detail line under
    // the status badge answers "why is this stuck" without psql.
    await openRepairDetails(user, "MUL-11");
    expect(
      screen.getByText(/3 attempts · task output parse failed/),
    ).toBeTruthy();
  });

  it("reruns a failed assessment with binding_id and force=true", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    await openRepairDetails(user, "MUL-11");
    await user.click(
      screen.getByRole("button", { name: "Requeue assessment" }),
    );

    await waitFor(() => {
      expect(TRIGGER_ASSESSMENT).toHaveBeenCalledWith({
        binding_id: "binding-5",
        force: true,
      });
    });
  });

  it("filters assessment rows by workstream, predictions, and pending-only", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    await user.click(screen.getByLabelText("Workstream"));
    await user.click(
      within(await screen.findByRole("listbox")).getByText("rel_1.7.3/client"),
    );

    expect(screen.getByText("Client crash")).toBeTruthy();
    expect(screen.queryByText("Login broke")).toBeNull();

    await user.click(screen.getByLabelText("Repair method"));
    await user.click(
      within(await screen.findByRole("listbox")).getByText("Unable to determine"),
    );

    expect(screen.getByText("Client crash")).toBeTruthy();
    expect(screen.queryByText("Login broke")).toBeNull();

    await user.click(screen.getByLabelText("AI quality prediction"));
    await user.click(
      within(await screen.findByRole("listbox")).getByText("Fail"),
    );

    expect(screen.getByText("Client crash")).toBeTruthy();
    expect(screen.queryByText("Login broke")).toBeNull();
  });

  it("toggles pending-judgement-only on and off", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    const toggle = screen.getByRole("button", {
      name: "Pending judgement only",
    });
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByText("Login broke")).toBeTruthy();
    expect(screen.getByText("Parser cleanup")).toBeTruthy();

    await user.click(toggle);

    expect(toggle).toHaveAttribute("aria-pressed", "true");
    // Rows with an AI quality verdict (likely_correct / likely_wrong) drop
    // out; rows without a judgement stay.
    expect(screen.queryByText("Login broke")).toBeNull();
    expect(screen.queryByText("Client crash")).toBeNull();
    expect(screen.getByText("Parser cleanup")).toBeTruthy();

    await user.click(toggle);

    expect(toggle).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByText("Login broke")).toBeTruthy();
  });

  it("can reset the workstream filter back to all workstreams", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    await user.click(screen.getByLabelText("Workstream"));
    await user.click(
      within(await screen.findByRole("listbox")).getByText("rel_1.7.3/client"),
    );

    expect(screen.getByText("Client crash")).toBeTruthy();
    expect(screen.queryByText("Login broke")).toBeNull();

    await user.click(screen.getByRole("button", { name: "All workstreams" }));

    expect(screen.getByText("Client crash")).toBeTruthy();
    expect(screen.getByText("Login broke")).toBeTruthy();
  });

  it("uses external workstream when P4 assessment has not populated one", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    await user.click(screen.getByLabelText("Workstream"));
    await user.click(
      within(await screen.findByRole("listbox")).getByText("rel_1.1.0"),
    );

    expect(screen.getByText("Shelved CL should not look final")).toBeTruthy();
    expect(screen.queryByText("Login broke")).toBeNull();
  });

  it("does not offer workstream filters from hidden ineligible rows", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.click(screen.getByLabelText("Workstream"));
    const listbox = await screen.findByRole("listbox");
    expect(within(listbox).queryByText("rel_future/server")).toBeNull();
    expect(within(listbox).getByText("rel_1.7.2/server")).toBeTruthy();
  });

  it("renders a resize handle for each sizable column", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);
    const handles = screen.getAllByRole("separator");
    expect(handles.length).toBe(4);
    expect(
      handles.map((h) => h.getAttribute("aria-label")),
    ).toEqual([
      "Resize Issue column",
      "Resize Agent column",
      "Resize Repair method column",
      "Resize Date column",
    ]);
    expect(
      handles.every((h) => h.getAttribute("aria-label") !== "External status"),
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

  it("filters out rows without the Operations display prerequisites", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);
    expect(screen.queryByText("Future work")).toBeNull();
    expect(screen.queryByText("triaged")).toBeNull();
    expect(screen.queryByText("robot_wrote_it")).toBeNull();
    expect(screen.queryByText("surprisingly_fine")).toBeNull();
    expect(screen.getByText("Login broke")).toBeTruthy();
  });

  it("filters rows by the comment search term and highlights the match", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    // All issues present before searching.
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

    // Detail filters never bend the stats: the headline ratio still reports
    // the full page-level pool while the table is narrowed by search.
    expect(screen.getByText("2 / 6")).toBeTruthy();
  });

  it("shows a search-specific empty state when nothing matches", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

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
    await openAssessments(user);

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
