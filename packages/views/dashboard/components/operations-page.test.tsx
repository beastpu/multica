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
      status: "vcvaCnnGi",
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
      status: "Done",
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
      warnings: ["final CL differs from AI shelve"],
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
      status: "Done",
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
      status: "Done",
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
    // The latest run hard-failed with a structured taskfailure code — the
    // analysis tab's fix-run failures card surfaces the raw code.
    task_status: "failed",
    task_failure_reason: "agent_error.provider_auth_or_access",
    started_at: null,
    completed_at: dayIso(2),
    created_at: dayIso(2),
    external: {
      binding_id: "binding-7",
      work_item_id: "BUG-10002",
      status: "Done",
      mapped_status: "done",
      done: true,
      project: "Warpath3",
    },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "unattributed",
      quality_prediction: "unknown",
      ai_shelved_cls: [],
      // Canonical data-gap warning (not an access block): the external item
      // is done but no submitted CL exists in any evidence source.
      warnings: ["missing_external_cl"],
    },
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
      if (opts.queryKey.includes("issue-statuses")) {
        return {
          data: {
            statuses: [
              { key: "vcvaCnnGi", name: "设计如此" },
              { key: "Done", name: "Done" },
              { key: "In Progress", name: "In Progress" },
            ],
          },
          isLoading: false,
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

import {
  OperationsPage,
  buildOperationsP4AssessmentCsv,
  splitHighlight,
} from "./operations-page";

let exportedBlob: Blob | null = null;

// Analysis is the default tab; tests exercising the per-ticket detail table
// switch to it first. `label` is the localized tab caption ("评估明细" in zh).
async function openAssessments(
  user: ReturnType<typeof userEvent.setup>,
  label = "Assessments",
) {
  await user.click(screen.getByText(label));
}

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
    expect(screen.getByText("设计如此")).toBeTruthy();
    expect(screen.queryByText("vcvaCnnGi")).toBeNull();

    // Agent name appears for each visible row (the feed hides rows without a
    // normal agent run, done issue status, and done external binding).
    expect(screen.getAllByText("Fixer").length).toBe(5);
    expect(screen.getByText("Reviewer")).toBeTruthy();

    // "状态" column = ISSUE workflow status (labels from the issues namespace).
    expect(screen.getAllByText("Done").length).toBeGreaterThanOrEqual(1);

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

  it("renders the KPI band with the headline rates and the funnel", () => {
    renderWithI18n(<OperationsPage />);

    // Three headline cards forming the nesting chain: contribution (produced /
    // external done) → coverage (judged / produced) → pass rate (correct /
    // judged).
    expect(screen.getByText("AI contribution rate")).toBeTruthy();
    expect(screen.getByText("Assessment coverage")).toBeTruthy();
    expect(screen.getByText("AI plan pass rate")).toBeTruthy();
    expect(screen.getByText("Delivery funnel")).toBeTruthy();
    expect(screen.getByText("Last 30 days")).toBeTruthy();
    // Funnel stages, with the verifiable-output stage as the quality pool.
    expect(screen.getByText("External done")).toBeTruthy();
    expect(screen.getByText("AI engaged")).toBeTruthy();
    expect(
      screen.getAllByText("Shelve/Swarm record").length,
    ).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Verifiable output")).toBeTruthy();
    expect(screen.getByText("Judged")).toBeTruthy();
        // External-done splits by resolved status label: five fixtures carry the
    // raw "Done" status and t-1 resolves vcvaCnnGi → 设计如此 via the status
    // name map.
    expect(screen.getByText("Done 5 · 设计如此 1")).toBeTruthy();
    // Coverage: both AI plans (t-1, t-4) reached a verdict → 2/2 = 100%.
    expect(screen.getAllByText("100%").length).toBeGreaterThanOrEqual(1);
    // Data-health footnote replaces the old no-output/undetermined cards.
    expect(screen.getByText(/no output · \d+ undetermined/)).toBeTruthy();
  });

  it("derives AI no output for completed assessments without an AI shelve", () => {
    renderWithI18n(<OperationsPage />);
    // t-7: unattributed prediction + empty ai_shelved_cls → derived label.
    expect(screen.getAllByText("AI no output").length).toBeGreaterThanOrEqual(1);
  });

  it("renders demo-like P4 assessment evidence and quality analysis", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    expect(screen.getByText("AI fix assessment")).toBeTruthy();
    expect(screen.getByText("Assessments")).toBeTruthy();
    expect(screen.getByText("Insights")).toBeTruthy();
    expect(screen.getByText("BUG-93218")).toBeTruthy();
    expect(screen.getAllByText("Done").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("In stats").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("AI assessed").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("stream rel_1.7.2/server")).toBeTruthy();
    expect(screen.queryByText("Swarm SW-11872")).toBeNull();
    // The detail table shows the submitted CL first; shelved CLs stay hidden
    // when a final committed CL exists.
    const badgeTexts = Array.from(document.querySelectorAll("span")).map((s) =>
      (s.textContent ?? "").replace(/\s+/g, " ").trim(),
    );
    expect(badgeTexts).toContain("final CL 283006");
    expect(badgeTexts).toContain("final CL 284805");
    expect(badgeTexts).toContain("shelve 287451");
    expect(badgeTexts).not.toContain("shelve 282941");
    expect(badgeTexts).not.toContain("final CL 287451");
    expect(screen.queryByText("changes 282941, 282944")).toBeNull();

    await user.click(screen.getAllByRole("button", { name: "Details" })[0]!);
    expect(screen.getByText("SW-11872")).toBeTruthy();
    expect(screen.getByText("282941, 282944")).toBeTruthy();
    // "283006" also renders as the linked final-CL badge text in the row.
    expect(screen.getAllByText("283006").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("main")).toBeTruthy();
    expect(screen.getByText("review.committed")).toBeTruthy();
    expect(screen.getByText("2026-06-01T00:30:00Z")).toBeTruthy();
    await user.keyboard("{Escape}");

    expect(screen.getAllByText("AI delivered").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Pass").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("confidence 86%")).toBeTruthy();
    expect(screen.getAllByText("Fail").length).toBeGreaterThanOrEqual(1);
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

    // Quality prediction badges + funnel stage labels.
    expect(screen.getByText("\u4e0d\u901a\u8fc7")).toBeTruthy();
    expect(screen.getByText("\u5df2\u5224\u5b9a")).toBeTruthy();
    expect(screen.getAllByText("\u901a\u8fc7").length).toBeGreaterThanOrEqual(1);

    // The submitted-CL record popover still opens with the localized trigger.
    await user.click(screen.getAllByRole("button", { name: "\u8be6\u60c5" })[0]!);
    expect(screen.getByText("282941, 282944")).toBeTruthy();
  });

  it("shows only done issues with an agent run and a done external binding", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    // t-2 (done, no assessment yet) → enabled Run assessment.
    const runButtons = screen.getAllByRole("button", {
      name: "Queue assessment",
    });
    expect(runButtons.some((b) => !(b as HTMLButtonElement).disabled)).toBe(
      true,
    );

    // t-3 has an agent run, but its issue status and external mapping are not
    // done, so it must not leak into the Operations detail table.
    expect(screen.queryByText("Future work")).toBeNull();
    expect(screen.queryByText("triaged")).toBeNull();
    expect(screen.queryByText("rel_future/server")).toBeNull();
  });

  it("triggers a new assessment with binding_id and force=false", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

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
    expect(
      screen.getByText(/3 attempts · task output parse failed/),
    ).toBeTruthy();
  });

  it("reruns a failed assessment with binding_id and force=true", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);

    let failedRow = screen.getByText("Assessment parser failed").parentElement;
    while (
      failedRow &&
      !failedRow.getAttribute("style")?.includes("grid-template-columns")
    ) {
      failedRow = failedRow.parentElement;
    }
    expect(failedRow).not.toBeNull();
    await user.click(
      within(failedRow as HTMLElement).getByRole("button", {
        name: "Requeue assessment",
      }),
    );

    await waitFor(() => {
      expect(TRIGGER_ASSESSMENT).toHaveBeenCalledWith({
        binding_id: "binding-5",
        force: true,
      });
    });
  });

  it("switches to the lightweight analysis report tab", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.click(screen.getByText("Insights"));

    expect(screen.getByText("Delivery attribution")).toBeTruthy();
    expect(screen.getByText("AI quality distribution")).toBeTruthy();
    // Blocked-evidence card: none of the current fixtures carry an
    // access-blocked warning, so the empty copy shows.
    expect(
      screen.getByText(/Evidence access blocked \(\d+ completed\)/),
    ).toBeTruthy();
    expect(screen.getByText("No blocked assessments")).toBeTruthy();
    // Process-gaps card: t-7 carries missing_external_cl with no committed CL
    // (its warning is a data gap, not an access block — the blocked card above
    // must stay empty). Zero-count rows stay visible.
    expect(screen.getByText("Process gaps")).toBeTruthy();
    expect(screen.getByText("Missing human CL on work item")).toBeTruthy();
    expect(screen.getAllByText("Plan, no record").length).toBeGreaterThanOrEqual(1);
    // Fix-run failures card: t-7's latest run failed with a structured
    // taskfailure code, shown raw.
    expect(screen.getByText("Fix-run failures")).toBeTruthy();
    expect(
      screen.getByText("agent_error.provider_auth_or_access"),
    ).toBeTruthy();
    expect(screen.getByText("Workstream outcome")).toBeTruthy();
    // The free-text AI-reasons ranking is gone: same-meaning sentences
    // fragment into distinct rows, so the card carried no signal.
    expect(screen.queryByText("Top AI reasons")).toBeNull();
    expect(screen.getByText("rel_1.7.3/client")).toBeTruthy();
    expect(screen.getAllByText("AI delivered").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Pass").length).toBeGreaterThanOrEqual(1);
  });

  it("drills down from an analysis distribution into the filtered detail table", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);

    await user.click(screen.getByText("Insights"));
    // Clicking a distribution row applies the filter and jumps back to detail.
    await user.click(screen.getAllByText("Human delivered")[0]!);

    await waitFor(() => {
      expect(screen.getByText("Client crash")).toBeTruthy();
      expect(screen.queryByText("Login broke")).toBeNull();
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

    await user.click(screen.getByLabelText("Delivery attribution"));
    await user.click(
      within(await screen.findByRole("listbox")).getByText("Human delivered"),
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
    expect(csv).toContain(
      "Swarm Review,Swarm Changes,Swarm Commits,Swarm Branch,Swarm Event Type,Swarm Sent At",
    );
    expect(csv).toContain("MUL-10,Client crash,BUG-10000");
    expect(csv).toContain("rel_1.7.3/client");
    expect(csv).toContain("SW-11900");
    expect(csv).toContain("283111; 283112");
    expect(csv).toContain("release/client");
    expect(csv).toContain("review.updated");
    expect(csv).toContain("2026-06-04T00:30:00Z");
    expect(csv).toContain("283111");
    expect(csv).toContain("283222");
    expect(csv).toContain("likely_wrong");
    expect(csv).not.toContain("Login broke");
  });

  it("renders a resize handle for each sizable column", async () => {
    const user = userEvent.setup();
    renderWithI18n(<OperationsPage />);
    await openAssessments(user);
    const handles = screen.getAllByRole("separator");
    expect(handles.length).toBe(6);
    expect(
      handles.map((h) => h.getAttribute("aria-label")),
    ).toEqual([
      "Resize Issue column",
      "Resize Agent column",
      "Resize Submitted CL record column",
      "Resize Delivery attribution column",
      "Resize AI assessment column",
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

describe("buildOperationsP4AssessmentCsv", () => {
  it("writes a BOM, stable headers, escaped cells, arrays, and sparse fields", () => {
    const csv = buildOperationsP4AssessmentCsv([
      {
        ...(FIXES[0] as any),
        issue_title: 'Login, "broke"',
        p4_assessment: {
          ...((FIXES[0] as any).p4_assessment ?? {}),
          summary: "first line\nsecond line",
        },
      },
      { ...(FIXES[1] as any), external: undefined },
    ]);

    expect(csv.startsWith("\uFEFF")).toBe(true);
    expect(csv).toContain("Issue,Issue Title,External Work Item ID");
    expect(csv).toContain("Derived Attribution");
    expect(csv).toContain('MUL-7,"Login, ""broke""",BUG-93218');
    expect(csv).toContain('"first line\nsecond line"');
    expect(csv).toContain("SW-11872");
    expect(csv).toContain("282941");
    expect(csv).toContain("283006");
    expect(csv).toContain("MUL-8,Parser cleanup,,,,,,,,Fixer");
  });
});
