import { describe, expect, it } from "vitest";
import type { AgentFixRecord } from "@multica/core/types";
import {
  AI_NO_OUTPUT,
  AI_PLAN_NO_RECORD,
  attributionBucket,
  qualityBucket,
  UNASSESSED,
  blockedWarningFamily,
  computeBlockedStats,
  computeOperationsKpis,
  deriveAttribution,
  fixDayIso,
  hasMissingExternalClWarning,
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

  it("splits no-output into plan-no-record when the agent left a comment", () => {
    // The agent completed a comment task (the feed's last_comment is the
    // latest agent comment) but no shelve landed — a process gap, not a
    // no-show. Distinguishing the two drives different fixes.
    expect(
      deriveAttribution(
        fix({
          last_comment: "proposed a fix plan in the ticket",
          last_comment_author_type: "agent",
          p4_assessment: {
            assessment_status: "completed",
            delivery_attribution_prediction: "unattributed",
            ai_shelved_cls: [],
          },
        }),
      ),
    ).toBe(AI_PLAN_NO_RECORD);
    // A whitespace-only comment is not a plan.
    expect(
      deriveAttribution(
        fix({
          last_comment: "  ",
          last_comment_author_type: "agent",
          p4_assessment: {
            assessment_status: "completed",
            delivery_attribution_prediction: "unattributed",
            ai_shelved_cls: [],
          },
        }),
      ),
    ).toBe(AI_NO_OUTPUT);
  });

  it("prefers the server's agent_comment_count over the last-comment fallback", () => {
    // A member reply pushes the agent comment out of last_comment on old
    // servers; the count survives it. A server that sends the count wins.
    const base = {
      p4_assessment: {
        assessment_status: "completed" as const,
        delivery_attribution_prediction: "unattributed",
        ai_shelved_cls: [],
      },
    };
    expect(
      deriveAttribution(
        fix({ ...base, agent_comment_count: 2, last_comment: "" }),
      ),
    ).toBe(AI_PLAN_NO_RECORD);
    expect(
      deriveAttribution(fix({ ...base, agent_comment_count: 0 })),
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
      aiEngaged: 3,
      aiPlanned: 3,
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
    // Contribution: 3 of 4 done tickets have an AI plan. Its numerator is the
    // SAME count as coverage's denominator — the headline nesting chain.
    expect(kpis.contributionRate).toEqual({
      value: 0.75,
      numerator: 3,
      denominator: 4,
    });
    // Coverage: 2 of the 3 AI plans reached a verdict (aiUnjudged did not).
    expect(kpis.coverageRate).toEqual({
      value: 2 / 3,
      numerator: 2,
      denominator: 3,
    });
    expect(kpis.coverageRate.denominator).toBe(kpis.contributionRate.numerator);
    // Quality (plan-quality, one rate): 1 of 2 judged is likely_correct.
    expect(kpis.passRate).toEqual({ value: 0.5, numerator: 1, denominator: 2 });
    expect(kpis.passRate.denominator).toBe(kpis.coverageRate.numerator);
    // Demoted health counts. noOutput = the unattributed-with-no-shelve row.
    // unjudged = every completed assessment without a verdict: aiUnjudged
    // (unknown quality) AND noOutput (no quality at all).
    expect(kpis.noOutput).toBe(1);
    expect(kpis.unjudged).toBe(2);
  });

  it("counts a verifiable row regardless of noisy warnings", () => {
    // AI shelve + committed CL: the gate is built from artifacts, so a stray
    // "*_unavailable" warning does not drop it.
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

  it("judges an AI plan even without a committed CL (assessment ≠ delivery)", () => {
    // AI shelved a fix and the assessment judged the plan likely_correct. Even
    // though nothing shipped (no committed CL), the quality verdict is valid —
    // quality is about the plan, not delivery. Whether it shipped is
    // contribution's job, not the quality pipeline's.
    const shelvedButUnshipped = fix({
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "unknown",
        quality_prediction: "likely_correct",
        ai_shelved_cls: [9],
      },
    });
    const kpis = computeOperationsKpis([aiPassed, shelvedButUnshipped]);
    expect(kpis.funnel.verifiable).toBe(2);
    expect(kpis.funnel.judged).toBe(2);
    expect(kpis.funnel.passed).toBe(2);
    // The unshipped plan is judged and passed — pass rate does not require a
    // committed CL.
    expect(kpis.passRate).toEqual({ value: 1, numerator: 2, denominator: 2 });
    // Both plans count as contribution (artifact-driven) and both were
    // assessed, so coverage is 2/2 — delivery attribution plays no role here.
    expect(kpis.contributionRate.numerator).toBe(2);
    expect(kpis.coverageRate).toEqual({ value: 1, numerator: 2, denominator: 2 });
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

  it("counts an unused AI plan as contribution (artifact-driven)", () => {
    // AI shelved a fix but a human shipped a different CL (human_delivered).
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
    // Contribution is artifact-driven: the plan exists, so it counts even
    // though a human shipped a different CL. Where it landed (unconverted)
    // stays visible in the composition partition and the analysis tab.
    expect(kpis.contributionRate).toEqual({
      value: 1,
      numerator: 1,
      denominator: 1,
    });
    // The failed plan is judged (it reached a verdict) but not passed.
    expect(kpis.passRate).toEqual({ value: 0, numerator: 0, denominator: 1 });
  });

  it("counts a comment-only plan as engaged but not planned or participated", () => {
    // The agent commented a plan but never produced a shelve or Swarm review —
    // visible as the aiEngaged→aiPlanned funnel drop and the plan-no-record
    // share of the no-output footnote. Participation stays artifact-driven, so
    // the composition bar keeps this row in not-participated.
    const planNoRecord = fix({
      external: { done: true },
      last_comment: "proposed a fix plan in the ticket",
      last_comment_author_type: "agent",
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "unattributed",
        ai_shelved_cls: [],
      },
    });
    const kpis = computeOperationsKpis([planNoRecord]);
    expect(kpis.funnel.aiEngaged).toBe(1);
    expect(kpis.funnel.aiPlanned).toBe(0);
    expect(kpis.composition.notParticipated).toBe(1);
    expect(kpis.noOutput).toBe(1);
  });

  it("returns null rates on empty input instead of fake zeros", () => {
    const kpis = computeOperationsKpis([]);
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

describe("distribution buckets reconcile with the KPI numerators", () => {
  const completedUnknown = fix({
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "unknown",
      quality_prediction: "unknown",
    },
  });
  const running = fix({
    p4_assessment: { assessment_status: "running" },
  });
  const failed = fix({
    p4_assessment: { assessment_status: "failed", quality_prediction: "unknown" },
  });
  const neverAssessed = fix();
  const passed = fix({
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      quality_prediction: "likely_correct",
      ai_shelved_cls: [1],
      swarm_committed_cls: [2],
    },
  });

  it("splits unfinished assessments into the unassessed bucket", () => {
    expect(qualityBucket(running)).toBe(UNASSESSED);
    expect(qualityBucket(failed)).toBe(UNASSESSED);
    expect(qualityBucket(neverAssessed)).toBe(UNASSESSED);
    expect(qualityBucket(completedUnknown)).toBe("unknown");
    expect(qualityBucket(passed)).toBe("likely_correct");
    expect(attributionBucket(running)).toBe(UNASSESSED);
    expect(attributionBucket(completedUnknown)).toBe("unknown");
    expect(attributionBucket(passed)).toBe("ai_delivered");
  });

  it("quality-card unknown equals the undetermined footnote count", () => {
    const rows = [completedUnknown, running, failed, neverAssessed, passed];
    const kpis = computeOperationsKpis(rows);
    const unknownInCard = rows.filter((r) => qualityBucket(r) === "unknown").length;
    // The reported mismatch (208 vs 157) came from counting unfinished
    // assessments as "unknown" in the card; with the bucket split both
    // surfaces count exactly the completed-without-verdict rows.
    expect(unknownInCard).toBe(kpis.unjudged);
    expect(
      rows.filter((r) => qualityBucket(r) === UNASSESSED).length,
    ).toBe(3);
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
    expect(blockedWarningFamily("swarm_lookup_unavailable")).toBe("swarm");
    expect(
      blockedWarningFamily(
        "multica_p4_evidence_endpoint_unavailable_cli_missing_api_command",
      ),
    ).toBe("evidence_endpoint");
    // Auth failures classify by cause (鉴权), not by which system raised them.
    expect(blockedWarningFamily("perforce_unauthorized")).toBe("auth");
    expect(blockedWarningFamily("swarm_api_unauthorized")).toBe("auth");
    // A CL/shelve/branch the agent claimed but nobody can find is an
    // identification failure, not an unreachable system.
    expect(
      blockedWarningFamily("claimed_shelved_cl_not_found_on_reachable_p4"),
    ).toBe("identification");
    expect(blockedWarningFamily("bug_branch_not_found")).toBe("identification");
    // Non-blocking warnings are not access blocks.
    expect(blockedWarningFamily("missing_external_cl")).toBeNull();
    expect(blockedWarningFamily("swarm_review_not_committed")).toBeNull();
    expect(blockedWarningFamily("final CL differs from AI shelve")).toBeNull();
  });

  it("requires completed status and an AI plan; a committed CL is not required", () => {
    const base = {
      assessment_status: "completed",
      delivery_attribution_prediction: "ai_delivered",
      quality_prediction: "likely_correct",
    };
    // AI shelve alone → counts: the plan is assessable before it ships.
    expect(
      isVerifiableOutput(
        fix({ p4_assessment: { ...base, ai_shelved_cls: [1] } }),
      ),
    ).toBe(true);
    // Swarm review (fix proposal) → counts even without a shelve.
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
    // Noisy warnings do not gate the pipeline — artifacts do.
    expect(
      isVerifiableOutput(
        fix({
          p4_assessment: {
            ...base,
            ai_shelved_cls: [1],
            warnings: ["swarm_lookup_unavailable", "evidence_endpoint_unavailable"],
          },
        }),
      ),
    ).toBe(true);
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
          },
        }),
      ),
    ).toBe(false);
  });
});

describe("hasMissingExternalClWarning", () => {
  it("flags a completed assessment with the canonical warning and no committed CL", () => {
    expect(
      hasMissingExternalClWarning(
        fix({
          p4_assessment: {
            assessment_status: "completed",
            warnings: ["missing_external_cl"],
          },
        }),
      ),
    ).toBe(true);
    // A committed CL means the gap is closed even if the warning is stale.
    expect(
      hasMissingExternalClWarning(
        fix({
          p4_assessment: {
            assessment_status: "completed",
            warnings: ["missing_external_cl"],
            swarm_committed_cls: [101],
          },
        }),
      ),
    ).toBe(false);
    // Unfinished assessments are a queue state, not a data gap.
    expect(
      hasMissingExternalClWarning(
        fix({
          p4_assessment: {
            assessment_status: "running",
            warnings: ["missing_external_cl"],
          },
        }),
      ),
    ).toBe(false);
    expect(
      hasMissingExternalClWarning(
        fix({ p4_assessment: { assessment_status: "completed" } }),
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
            "perforce_unauthorized",
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
    // The first row hits four families at once (one warning each): auth,
    // identification (p4_cl_not_found), swarm, and p4 — each counted once.
    expect(stats.families).toEqual([
      { family: "auth", count: 1 },
      { family: "identification", count: 1 },
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
