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
  // Verifiable rows: AI produced a plan (shelve) AND a committed CL shipped.
  const aiPassed = fix({
    external: { done: true },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      quality_prediction: "likely_correct",
      ai_shelved_cls: [1],
      swarm_committed_cls: [101],
    },
  });
  const aiFailed = fix({
    external: { done: true },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_assisted",
      quality_prediction: "likely_wrong",
      ai_shelved_cls: [2],
      external_committed_cls: [102],
    },
  });
  const aiUnjudged = fix({
    external: { done: true },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      quality_prediction: "unknown",
      ai_shelved_cls: [3],
      swarm_committed_cls: [103],
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
    // MECE composition sums to externalDone (4): two ai_delivered, one
    // ai_assisted, and noOutput has no AI involvement.
    expect(kpis.composition).toEqual({
      directDelivered: 2,
      assisted: 1,
      unconverted: 0,
      notParticipated: 1,
    });
    // Scale (same 外部完成 base): 3 of 4 done tickets have an AI plan and all 3
    // are AI-contributed.
    expect(kpis.participationRate).toEqual({
      value: 0.75,
      numerator: 3,
      denominator: 4,
    });
    expect(kpis.contributionRate).toEqual({
      value: 0.75,
      numerator: 3,
      denominator: 4,
    });
    // Quality: 1 of 2 judged is likely_correct.
    expect(kpis.passRate).toEqual({ value: 0.5, numerator: 1, denominator: 2 });
    // Coverage: 2 of the 3 AI plans reached a verdict (aiUnjudged did not).
    expect(kpis.coverageRate).toEqual({
      value: 2 / 3,
      numerator: 2,
      denominator: 3,
    });
    // Demoted health counts. noOutput = the unattributed-with-no-shelve row.
    // unjudged = every completed assessment without a verdict: aiUnjudged
    // (unknown quality) AND noOutput (no quality at all).
    expect(kpis.noOutput).toBe(1);
    expect(kpis.unjudged).toBe(2);
  });

  it("counts a verifiable row regardless of noisy warnings", () => {
    // AI shelve + committed CL: the denominator is built from artifacts, so a
    // stray "*_unavailable" warning does not drop it.
    const warned = fix({
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "ai_delivered",
        quality_prediction: "likely_correct",
        ai_shelved_cls: [9],
        external_committed_cls: [909],
        warnings: ["evidence_endpoint_unavailable", "swarm_lookup_unavailable"],
      },
    });
    const kpis = computeOperationsKpis([aiPassed, warned]);
    expect(kpis.funnel.verifiable).toBe(2);
    expect(kpis.funnel.judged).toBe(2);
    expect(kpis.funnel.passed).toBe(2);
    expect(kpis.passRate).toEqual({ value: 1, numerator: 2, denominator: 2 });
  });

  it("excludes an AI plan that never shipped a committed CL", () => {
    // AI shelved a fix and judged it likely_correct, but no committed CL exists
    // — the fix didn't land, so it can't count toward the fix rate.
    const shelvedButUnshipped = fix({
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "ai_delivered",
        quality_prediction: "likely_correct",
        ai_shelved_cls: [9],
      },
    });
    const kpis = computeOperationsKpis([aiPassed, shelvedButUnshipped]);
    expect(kpis.funnel.verifiable).toBe(1);
    expect(kpis.passRate).toEqual({ value: 1, numerator: 1, denominator: 1 });
  });

  it("excludes a committed CL with no AI plan (human-delivered)", () => {
    // WAR-6085 shape: a committed CL shipped and the fix is judged correct, but
    // AI produced nothing — the human's fix must not count as an AI win.
    const humanDelivered = fix({
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "human_delivered",
        quality_prediction: "likely_correct",
        ai_shelved_cls: [],
        external_committed_cls: [261939],
      },
    });
    const kpis = computeOperationsKpis([aiPassed, humanDelivered]);
    expect(kpis.funnel.verifiable).toBe(1);
    expect(kpis.passRate).toEqual({ value: 1, numerator: 1, denominator: 1 });
  });

  it("counts an unused AI plan as participation but not contribution", () => {
    // AI shelved a fix (participation) but a human shipped a different CL, so
    // the attribution is human_delivered — it did not reach delivery, so it is
    // NOT a contribution. The gap between the two rates is exactly this case.
    const planNotUsed = fix({
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "human_delivered",
        quality_prediction: "likely_wrong",
        ai_shelved_cls: [283979],
        external_committed_cls: [285179],
      },
    });
    const kpis = computeOperationsKpis([planNotUsed]);
    expect(kpis.composition).toEqual({
      directDelivered: 0,
      assisted: 0,
      unconverted: 1,
      notParticipated: 0,
    });
    expect(kpis.participationRate).toEqual({
      value: 1,
      numerator: 1,
      denominator: 1,
    });
    expect(kpis.contributionRate).toEqual({
      value: 0,
      numerator: 0,
      denominator: 1,
    });
  });

  it("returns null rates on empty input instead of fake zeros", () => {
    const kpis = computeOperationsKpis([]);
    expect(kpis.participationRate.value).toBeNull();
    expect(kpis.contributionRate.value).toBeNull();
    expect(kpis.passRate.value).toBeNull();
    expect(kpis.coverageRate.value).toBeNull();
    expect(kpis.noOutput).toBe(0);
    expect(kpis.unjudged).toBe(0);
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

  it("requires completed status, an AI plan, and a committed CL", () => {
    const base = {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      quality_prediction: "likely_correct",
    };
    // AI shelve + committed CL → counts.
    expect(
      isVerifiableOutput(
        fix({
          p4_assessment: {
            ...base,
            ai_shelved_cls: [1],
            swarm_committed_cls: [10],
          },
        }),
      ),
    ).toBe(true);
    // Swarm review (fix proposal) + committed CL → counts even without a shelve.
    expect(
      isVerifiableOutput(
        fix({
          p4_assessment: {
            ...base,
            ai_shelved_cls: [],
            swarm_reviews: [{ review_id: 1 }],
            external_committed_cls: [11],
          },
        }),
      ),
    ).toBe(true);
    // Noisy warnings do not gate the denominator — artifacts do.
    expect(
      isVerifiableOutput(
        fix({
          p4_assessment: {
            ...base,
            ai_shelved_cls: [1],
            swarm_committed_cls: [10],
            warnings: ["swarm_lookup_unavailable", "evidence_endpoint_unavailable"],
          },
        }),
      ),
    ).toBe(true);
    // AI plan but no committed CL → excluded (the fix never shipped).
    expect(
      isVerifiableOutput(
        fix({ p4_assessment: { ...base, ai_shelved_cls: [1] } }),
      ),
    ).toBe(false);
    // Committed CL but no AI plan → excluded (human-delivered, WAR-6085 shape).
    expect(
      isVerifiableOutput(
        fix({
          p4_assessment: {
            ...base,
            delivery_attribution_prediction: "human_delivered",
            ai_shelved_cls: [],
            external_committed_cls: [261939],
          },
        }),
      ),
    ).toBe(false);
    // Not completed → excluded.
    expect(
      isVerifiableOutput(
        fix({
          p4_assessment: {
            ...base,
            assessment_status: "running",
            ai_shelved_cls: [1],
            swarm_committed_cls: [10],
          },
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
