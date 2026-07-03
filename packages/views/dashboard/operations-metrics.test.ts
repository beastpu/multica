import { describe, expect, it } from "vitest";
import type { AgentFixRecord } from "@multica/core/types";
import {
  AI_NO_OUTPUT,
  blockedWarningFamily,
  computeBlockedStats,
  computeOperationsKpis,
  deriveAttribution,
  fixDayIso,
  isVerifiableOutput,
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

  it("keeps unknown as unknown — insufficient evidence is not no-output", () => {
    // The assessment skill emits unknown + empty CL arrays when P4/Swarm
    // lookup was unavailable; that must not inflate the no-output rate.
    expect(
      deriveAttribution(
        fix({
          p4_assessment: {
            assessment_status: "completed",
            delivery_attribution_prediction: "unknown",
            ai_shelved_cls: [],
          },
        }),
      ),
    ).toBe("unknown");
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
  const aiPassed = fix({
    external: { done: true },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      quality_prediction: "likely_correct",
      ai_shelved_cls: [1],
    },
  });
  const aiFailed = fix({
    external: { done: true },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_assisted",
      quality_prediction: "likely_wrong",
      ai_shelved_cls: [2],
    },
  });
  const aiUnjudged = fix({
    external: { done: true },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      quality_prediction: "unknown",
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

  it("computes the nested funnel and the headline rates", () => {
    const kpis = computeOperationsKpis([
      aiPassed,
      aiFailed,
      aiUnjudged,
      noOutput,
      notDone,
    ]);
    expect(kpis.funnel).toEqual({
      total: 5,
      externalDone: 4,
      p4Covered: 4,
      verifiable: 3,
      judged: 2,
      passed: 1,
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
    // Completed but verdict-less: aiUnjudged (unknown quality) and noOutput
    // (no quality at all).
    expect(kpis.unjudgedRate).toEqual({
      value: 0.4,
      numerator: 2,
      denominator: 5,
    });
  });

  it("keeps access-blocked rows out of the fix-rate denominator", () => {
    const blocked = fix({
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "unknown",
        quality_prediction: "likely_correct",
        ai_shelved_cls: [9],
        warnings: ["claimed_shelved_cl_not_found_on_reachable_p4"],
      },
    });
    const kpis = computeOperationsKpis([aiPassed, blocked]);
    expect(kpis.funnel.verifiable).toBe(1);
    expect(kpis.passRate).toEqual({ value: 1, numerator: 1, denominator: 1 });
  });

  it("returns null rates on empty input instead of fake zeros", () => {
    const kpis = computeOperationsKpis([]);
    expect(kpis.passRate.value).toBeNull();
    expect(kpis.deliveryShare.value).toBeNull();
    expect(kpis.noOutputRate.value).toBeNull();
    expect(kpis.unjudgedRate.value).toBeNull();
  });

  it("does not count an unknown or drifting quality value as judged", () => {
    const kpis = computeOperationsKpis([
      aiUnjudged,
      fix({
        p4_assessment: {
          assessment_status: "completed",
          delivery_attribution_prediction: "ai_delivered",
          quality_prediction: "surprisingly_fine",
          ai_shelved_cls: [1],
        },
      }),
    ]);
    expect(kpis.funnel.judged).toBe(0);
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

describe("fixDayIso", () => {
  it("prefers the server's window instant (activity_at) over run timestamps", () => {
    expect(
      fixDayIso(
        fix({ activity_at: "2026-06-15T08:00:00Z" } as any),
        "UTC",
      ),
    ).toBe("2026-06-15");
  });

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

describe("blockedWarningFamily / isVerifiableOutput", () => {
  it("family-matches drifting warning variants, not just canonical values", () => {
    expect(blockedWarningFamily("p4_lookup_unavailable")).toBe("p4");
    expect(blockedWarningFamily("p4_lookup_unavailable_for_candidate_cls")).toBe(
      "p4",
    );
    expect(
      blockedWarningFamily("claimed_shelved_cl_not_found_on_reachable_p4"),
    ).toBe("p4");
    expect(blockedWarningFamily("swarm_lookup_unavailable")).toBe("swarm");
    expect(
      blockedWarningFamily(
        "multica_p4_evidence_endpoint_unavailable_cli_missing_api_command",
      ),
    ).toBe("evidence_endpoint");
    // Non-blocking warnings are not access blocks.
    expect(blockedWarningFamily("missing_external_cl")).toBeNull();
    expect(blockedWarningFamily("swarm_review_not_committed")).toBeNull();
    expect(blockedWarningFamily("final CL differs from AI shelve")).toBeNull();
  });

  it("requires completed status, AI evidence, and unblocked access", () => {
    const base = {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      quality_prediction: "likely_correct",
    };
    expect(
      isVerifiableOutput(
        fix({ p4_assessment: { ...base, ai_shelved_cls: [1] } }),
      ),
    ).toBe(true);
    // Swarm review evidence counts even without a shelve CL.
    expect(
      isVerifiableOutput(
        fix({
          p4_assessment: {
            ...base,
            ai_shelved_cls: [],
            swarm_reviews: [{ review_id: 1 }],
          },
        }),
      ),
    ).toBe(true);
    expect(
      isVerifiableOutput(
        fix({ p4_assessment: { ...base, ai_shelved_cls: [] } }),
      ),
    ).toBe(false);
    expect(
      isVerifiableOutput(
        fix({
          p4_assessment: {
            ...base,
            ai_shelved_cls: [1],
            warnings: ["swarm_lookup_unavailable"],
          },
        }),
      ),
    ).toBe(false);
    expect(
      isVerifiableOutput(
        fix({
          p4_assessment: { ...base, assessment_status: "running", ai_shelved_cls: [1] },
        }),
      ),
    ).toBe(false);
  });
});

describe("computeBlockedStats", () => {
  it("counts blocked completed assessments per family with dedup per row", () => {
    const stats = computeBlockedStats([
      fix({
        p4_assessment: {
          assessment_status: "completed",
          warnings: [
            "swarm_lookup_unavailable",
            "p4_lookup_unavailable",
            "p4_cl_not_found",
          ],
        },
      }),
      fix({
        p4_assessment: {
          assessment_status: "completed",
          warnings: ["evidence_endpoint_unavailable"],
        },
      }),
      fix({
        p4_assessment: {
          assessment_status: "completed",
          warnings: ["missing_external_cl"],
        },
      }),
      // Not completed — excluded entirely.
      fix({
        p4_assessment: {
          assessment_status: "failed",
          warnings: ["swarm_lookup_unavailable"],
        },
      }),
    ]);
    expect(stats.completed).toBe(3);
    expect(stats.families).toEqual([
      { family: "swarm", count: 1 },
      { family: "p4", count: 1 },
      { family: "evidence_endpoint", count: 1 },
    ]);
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
