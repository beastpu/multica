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
  isAssignedToAgent,
  isAiParticipated,
  noPlanReason,
  repairMethod,
  unconvertedReason,
  isVerifiableOutput,
  trimOperationsWindow,
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
    issue_assignee_type: "agent",
    issue_assignee_id: "agent-1",
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

describe("repairMethod", () => {
  it("exposes only AI submitted, AI assisted, or unknown", () => {
    const withAttribution = (value: string) =>
      fix({
        p4_assessment: {
          assessment_status: "completed",
          delivery_attribution_prediction: value,
          ai_shelved_cls: ["123"],
        },
      });

    expect(repairMethod(withAttribution("ai_delivered"))).toBe("ai_delivered");
    expect(repairMethod(withAttribution("ai_assisted"))).toBe("ai_assisted");
    expect(repairMethod(withAttribution("human_delivered"))).toBe("unknown");
    expect(repairMethod(withAttribution("conflict"))).toBe("unknown");
    expect(repairMethod(fix())).toBe("unknown");
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
    task_id: "",
    agent_id: "",
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
      total: 4,
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
    // Contribution: 3 of 4 external-done tickets have a normal Agent task.
    expect(kpis.contributionRate).toEqual({
      value: 0.75,
      numerator: 3,
      denominator: 4,
    });
    // Outcome cards only use explicit judgements. An "unknown" assessment is
    // visible in the quality drawer but stays outside the denominator.
    expect(kpis.judgedCount).toBe(2);
    expect(kpis.judgedCount).toBeLessThanOrEqual(
      kpis.contributionRate.numerator,
    );
    expect(kpis.qualityRate).toEqual({
      value: 1 / 2,
      numerator: 1,
      denominator: 2,
    });
    expect(kpis.automaticRate).toEqual({
      value: 1 / 2,
      numerator: 1,
      denominator: 2,
    });
    expect(kpis.assistedRate).toEqual({
      value: 0,
      numerator: 0,
      denominator: 2,
    });
    expect(kpis.assistedRate.numerator).toBe(
      kpis.qualityRate.numerator - kpis.automaticRate.numerator,
    );
    // Health counts use the same assigned + external-done opportunity pool;
    // notDone is outside it. No fixture carries missing_external_cl.
    expect(kpis.unassessed).toBe(0);
    expect(kpis.missingExternalCl).toBe(0);
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
    expect(kpis.qualityRate).toEqual({ value: 1, numerator: 2, denominator: 2 });
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
    // The unshipped plan is judged and passed — quality does not require a
    // committed CL.
    expect(kpis.qualityRate).toEqual({ value: 1, numerator: 2, denominator: 2 });
    // Both external-done tickets had a normal Agent task.
    expect(kpis.contributionRate.numerator).toBe(2);
    // The passing unconverted plan is counted as an assisted success.
    expect(kpis.assistedRate).toEqual({ value: 0.5, numerator: 1, denominator: 2 });
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
    expect(kpis.qualityRate).toEqual({ value: 1, numerator: 1, denominator: 1 });
  });

  it("keeps a failed unused AI plan in participation but not assisted success", () => {
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
    // Contribution and delivery composition share the same participation
    // predicate, so the two headline counts cannot drift apart.
    expect(kpis.contributionRate).toEqual({
      value: 1,
      numerator: 1,
      denominator: 1,
    });
    // The failed, unconverted plan remains AI participation, but it is not a
    // successful assisted repair.
    expect(kpis.qualityRate).toEqual({ value: 0, numerator: 0, denominator: 1 });
    expect(kpis.assistedRate).toEqual({ value: 0, numerator: 0, denominator: 1 });
  });

  it("defines pickup by AI participation rather than normal task presence", () => {
    const participatedWithoutTask = fix({
      task_id: "",
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "ai_assisted",
        quality_prediction: "likely_needs_changes",
        ai_shelved_cls: [88],
      },
    });
    const taskWithoutParticipation = fix({
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "human_delivered",
        quality_prediction: "likely_correct",
        ai_shelved_cls: [],
      },
    });

    expect(isAiParticipated(participatedWithoutTask)).toBe(true);
    expect(isAiParticipated(taskWithoutParticipation)).toBe(false);
    const kpis = computeOperationsKpis([
      participatedWithoutTask,
      taskWithoutParticipation,
    ]);
    expect(kpis.contributionRate).toEqual({
      value: 0.5,
      numerator: 1,
      denominator: 2,
    });
    expect(
      kpis.composition.directDelivered +
        kpis.composition.assisted +
        kpis.composition.unconverted,
    ).toBe(kpis.contributionRate.numerator);
  });

  it("uses judged AI participation as every repair-rate denominator", () => {
    const judgedWithoutPlanArtifact = fix({
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "ai_delivered",
        quality_prediction: "likely_correct",
        ai_shelved_cls: [],
      },
    });
    const participatedButUnknown = fix({
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "ai_assisted",
        quality_prediction: "unknown",
        ai_shelved_cls: [91],
      },
    });

    const kpis = computeOperationsKpis([
      judgedWithoutPlanArtifact,
      participatedButUnknown,
    ]);
    expect(kpis.judgedCount).toBe(1);
    expect(kpis.qualityRate).toEqual({ value: 1, numerator: 1, denominator: 1 });
    expect(kpis.automaticRate).toEqual({
      value: 1,
      numerator: 1,
      denominator: 1,
    });
    expect(kpis.assistedRate).toEqual({
      value: 0,
      numerator: 0,
      denominator: 1,
    });
  });

  it("uses current Agent assignment as the contribution opportunity pool", () => {
    const historicalTaskOnly = fix({
      issue_assignee_type: "",
      issue_assignee_id: "",
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "ai_delivered",
        quality_prediction: "likely_correct",
      },
    });
    const assignedWithoutPlan = fix({
      external: { done: true },
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "human_delivered",
      },
    });
    expect(isAssignedToAgent(historicalTaskOnly)).toBe(false);
    expect(isAssignedToAgent(assignedWithoutPlan)).toBe(true);
    const kpis = computeOperationsKpis([historicalTaskOnly, assignedWithoutPlan]);
    expect(kpis.contributionRate).toEqual({
      value: 0,
      numerator: 0,
      denominator: 1,
    });
    expect(kpis.composition.notParticipated).toBe(1);
  });

  it("classifies contribution loss reasons into mutually exclusive buckets", () => {
    const unconvertedHuman = fix({
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "human_delivered",
        ai_shelved_cls: [1],
      },
    });
    const unconvertedUnknown = fix({
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "unknown",
        swarm_reviews: [{ sent_at: "2026-07-01T00:00:00Z" }],
      },
    });
    const cancelled = fix({ task_status: "cancelled" });
    const assessmentPending = fix({
      task_status: "completed",
      p4_assessment: { assessment_status: "pending" },
    });
    const commentOnly = fix({
      task_status: "completed",
      agent_comment_count: 1,
    });
    const noVisibleOutput = fix({
      task_status: "completed",
      agent_comment_count: 0,
      p4_assessment: {
        assessment_status: "completed",
        delivery_attribution_prediction: "human_delivered",
      },
    });

    expect(unconvertedReason(unconvertedHuman)).toBe("human_delivered");
    expect(unconvertedReason(unconvertedUnknown)).toBe("evidence_unconfirmed");
    expect(noPlanReason(cancelled)).toBe("task_cancelled");
    expect(noPlanReason(assessmentPending)).toBe("assessment_incomplete");
    expect(noPlanReason(commentOnly)).toBe("comment_only");
    expect(noPlanReason(noVisibleOutput)).toBe("no_visible_output");
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
  });

  it("returns null rates on empty input instead of fake zeros", () => {
    const kpis = computeOperationsKpis([]);
    expect(kpis.contributionRate.value).toBeNull();
    expect(kpis.qualityRate.value).toBeNull();
    expect(kpis.automaticRate.value).toBeNull();
    expect(kpis.assistedRate.value).toBeNull();
    expect(kpis.unassessed).toBe(0);
    expect(kpis.missingExternalCl).toBe(0);
  });

  it("keeps mapped external done rows in the denominator on older responses", () => {
    const kpis = computeOperationsKpis([
      fix({
        task_id: "",
        agent_id: "",
        external: { mapped_status: "done" },
      }),
    ]);
    expect(kpis.contributionRate).toEqual({
      value: 0,
      numerator: 0,
      denominator: 1,
    });
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
    external: { done: true },
    p4_assessment: {
      assessment_status: "completed",
      delivery_attribution_prediction: "unknown",
      quality_prediction: "unknown",
    },
  });
  const running = fix({
    external: { done: true },
    p4_assessment: { assessment_status: "running" },
  });
  const failed = fix({
    external: { done: true },
    p4_assessment: { assessment_status: "failed", quality_prediction: "unknown" },
  });
  const neverAssessed = fix({ external: { done: true } });
  const passed = fix({
    external: { done: true },
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

  it("unassessed bucket count equals the footnote's queue backlog", () => {
    const rows = [completedUnknown, running, failed, neverAssessed, passed];
    const kpis = computeOperationsKpis(rows);
    // The footnote's 未评估 and the (card-excluded) unassessed bucket count
    // the same rows, so the queue backlog reads identically everywhere.
    expect(
      rows.filter((r) => qualityBucket(r) === UNASSESSED).length,
    ).toBe(kpis.unassessed);
    expect(kpis.unassessed).toBe(3);
  });
});

describe("trimOperationsWindow", () => {
  const tz = "UTC";
  const today = new Date().toISOString();
  const daysAgo = (n: number) =>
    new Date(Date.now() - n * 86_400_000).toISOString();

  it("keeps only rows inside the trailing window", () => {
    const recent = fix({ completed_at: today });
    const older = fix({ completed_at: daysAgo(10) });
    const ancient = fix({ completed_at: daysAgo(20) });
    expect(trimOperationsWindow([recent, older, ancient], 7, tz)).toEqual([
      recent,
    ]);
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
