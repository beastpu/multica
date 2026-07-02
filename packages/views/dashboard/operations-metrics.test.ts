import { describe, expect, it } from "vitest";
import type { AgentFixRecord } from "@multica/core/types";
import {
  AI_NO_OUTPUT,
  computeOperationsKpis,
  computeOperationsTrend,
  deriveAttribution,
  fixDayIso,
  splitOperationsWindow,
  swarmChangeUrl,
  swarmReviewUrl,
} from "./operations-metrics";

function fix(overrides: Partial<AgentFixRecord> = {}): AgentFixRecord {
  return {
    task_id: "task-1",
    agent_id: "agent-1",
    agent_name: "Fixer",
    issue_id: "issue-1",
    issue_identifier: "MUL-1",
    issue_title: "Fix it",
    issue_status: "done",
    started_at: null,
    completed_at: "2026-07-01T10:00:00Z",
    created_at: "2026-07-01T09:00:00Z",
    ...overrides,
  };
}

describe("deriveAttribution", () => {
  it("keeps the raw prediction while the assessment is not completed", () => {
    expect(
      deriveAttribution(
        fix({
          p4_assessment: {
            assessment_status: "running",
            delivery_attribution_prediction: "",
            ai_shelved_cls: [],
          },
        }),
      ),
    ).toBe("unknown");
    expect(deriveAttribution(fix())).toBe("unknown");
  });

  it("returns ai_no_output when a completed assessment found no AI shelve", () => {
    expect(
      deriveAttribution(
        fix({
          p4_assessment: {
            assessment_status: "completed",
            delivery_attribution_prediction: "human_delivered",
            ai_shelved_cls: [],
          },
        }),
      ),
    ).toBe(AI_NO_OUTPUT);
    // Whitespace-only CLs don't count as output.
    expect(
      deriveAttribution(
        fix({
          p4_assessment: {
            assessment_status: "completed",
            delivery_attribution_prediction: "unattributed",
            ai_shelved_cls: [" "],
          },
        }),
      ),
    ).toBe(AI_NO_OUTPUT);
  });

  it("keeps the prediction when the AI shelved something", () => {
    expect(
      deriveAttribution(
        fix({
          p4_assessment: {
            assessment_status: "completed",
            delivery_attribution_prediction: "human_delivered",
            ai_shelved_cls: [282941],
          },
        }),
      ),
    ).toBe("human_delivered");
  });

  it("never reclassifies an AI delivery as no-output", () => {
    expect(
      deriveAttribution(
        fix({
          p4_assessment: {
            assessment_status: "completed",
            delivery_attribution_prediction: "ai_delivered",
            ai_shelved_cls: [],
          },
        }),
      ),
    ).toBe("ai_delivered");
  });
});

describe("computeOperationsKpis", () => {
  const aiAccepted = fix({
    external: { done: true },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      ai_shelved_cls: [1],
    },
    human_review: { outcome: "accepted" },
  });
  const aiRejected = fix({
    external: { done: true },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_assisted",
      ai_shelved_cls: [2],
    },
    human_review: { outcome: "rejected" },
  });
  const aiUnreviewed = fix({
    external: { done: true },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      ai_shelved_cls: [3],
    },
  });
  const noOutput = fix({
    external: { done: true },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "unattributed",
      ai_shelved_cls: [],
    },
  });
  const notDone = fix({ external: { done: false } });

  it("computes the nested funnel and the three headline rates", () => {
    const kpis = computeOperationsKpis([
      aiAccepted,
      aiRejected,
      aiUnreviewed,
      noOutput,
      notDone,
    ]);
    expect(kpis.funnel).toEqual({
      total: 5,
      externalDone: 4,
      p4Covered: 4,
      aiDelivered: 3,
      reviewed: 2,
      accepted: 1,
    });
    expect(kpis.passRate).toEqual({ value: 0.5, numerator: 1, denominator: 2 });
    expect(kpis.deliveryShare).toEqual({
      value: 0.75,
      numerator: 3,
      denominator: 4,
    });
    expect(kpis.noOutputRate).toEqual({
      value: 0.2,
      numerator: 1,
      denominator: 5,
    });
  });

  it("returns null rates on empty input instead of fake zeros", () => {
    const kpis = computeOperationsKpis([]);
    expect(kpis.passRate.value).toBeNull();
    expect(kpis.deliveryShare.value).toBeNull();
    expect(kpis.noOutputRate.value).toBeNull();
  });

  it("does not count not_applicable as reviewed", () => {
    const kpis = computeOperationsKpis([
      fix({
        p4_assessment: {
          assessment_status: "completed",
          delivery_attribution_prediction: "ai_delivered",
          ai_shelved_cls: [1],
        },
        human_review: { outcome: "not_applicable" },
      }),
    ]);
    expect(kpis.funnel.reviewed).toBe(0);
  });
});

describe("splitOperationsWindow", () => {
  const tz = "UTC";
  const today = new Date().toISOString();
  const daysAgo = (n: number) =>
    new Date(Date.now() - n * 86_400_000).toISOString();

  it("splits rows into the trailing window and the one before it", () => {
    const recent = fix({ completed_at: today });
    const older = fix({ completed_at: daysAgo(10) });
    const ancient = fix({ completed_at: daysAgo(20) });
    const { current, previous } = splitOperationsWindow(
      [recent, older, ancient],
      7,
      tz,
    );
    expect(current).toEqual([recent]);
    expect(previous).toEqual([older]);
  });
});

describe("computeOperationsTrend", () => {
  it("folds rows into trailing calendar weeks with null gaps", () => {
    const now = new Date().toISOString();
    const rows = [
      fix({
        completed_at: now,
        external: { done: true },
        p4_assessment: {
          assessment_status: "completed",
          delivery_attribution_prediction: "ai_delivered",
          ai_shelved_cls: [1],
        },
        human_review: { outcome: "accepted" },
      }),
    ];
    const trend = computeOperationsTrend(rows, "UTC", 4);
    expect(trend).toHaveLength(4);
    const last = trend[trend.length - 1]!;
    expect(last.passRate).toBe(100);
    expect(last.deliveryShare).toBe(100);
    expect(last.noOutputRate).toBe(0);
    // Empty weeks render as gaps, not zeros.
    expect(trend[0]!.passRate).toBeNull();
  });
});

describe("fixDayIso", () => {
  it("prefers completed_at and falls back through started/created", () => {
    expect(
      fixDayIso(
        fix({
          completed_at: null,
          started_at: "2026-06-30T23:30:00Z",
        }),
        "UTC",
      ),
    ).toBe("2026-06-30");
    expect(
      fixDayIso(fix({ completed_at: null, started_at: null }), "UTC"),
    ).toBe("2026-07-01");
  });

  it("survives a bad timezone", () => {
    expect(fixDayIso(fix(), "Not/AZone")).toBe("2026-07-01");
  });
});

describe("swarm links", () => {
  it("builds review and change URLs from the connection base", () => {
    expect(swarmReviewUrl("https://swarm.example.com/", "11872")).toBe(
      "https://swarm.example.com/reviews/11872",
    );
    expect(swarmChangeUrl("https://swarm.example.com", "282941")).toBe(
      "https://swarm.example.com/changes/282941",
    );
  });

  it("returns empty without a base or id", () => {
    expect(swarmReviewUrl("", "11872")).toBe("");
    expect(swarmChangeUrl("https://swarm.example.com", "")).toBe("");
  });
});
