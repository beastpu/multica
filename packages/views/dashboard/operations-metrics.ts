import type { AgentFixRecord } from "@multica/core/types";
import { addDaysIso, todayIso, weekStartIso } from "../runtimes/utils";
import { buildWeekShells, type WeekShell } from "./utils";

// ---------------------------------------------------------------------------
// Operations page metrics
//
// Pure derivations over the agent-fixes feed. Everything here is computed
// client-side from GET /api/operations/agent-fixes rows: the page fetches a
// 2× window (`days * 2`) so the trailing `days` window and the window before
// it can be compared without a second endpoint.
// ---------------------------------------------------------------------------

// Derived delivery attribution. Extends the AI prediction with one value the
// backend doesn't emit: `ai_no_output` — the agent ran but the completed P4
// assessment found no AI shelve at all (e.g. the agent hit an auth failure
// before it could submit). Two guards keep this honest:
// - Rows without a completed assessment keep the raw prediction; absence of
//   evidence is not evidence of absence.
// - An `unknown` prediction stays `unknown`: the assessment skill outputs
//   unknown + empty CL arrays when P4/Swarm lookup was unavailable, and
//   "couldn't find evidence" must not be counted as "produced nothing".
export const AI_NO_OUTPUT = "ai_no_output";

const PROVEN_NON_AI_ATTRIBUTIONS = new Set([
  "human_delivered",
  "unattributed",
  "conflict",
]);

export function deriveAttribution(fix: AgentFixRecord): string {
  const p4 = fix.p4_assessment;
  const prediction = p4?.delivery_attribution_prediction?.trim() || "unknown";
  if (p4?.assessment_status !== "completed") return prediction;
  if (!PROVEN_NON_AI_ATTRIBUTIONS.has(prediction)) return prediction;
  const shelved = (p4.ai_shelved_cls ?? []).filter(
    (cl) => String(cl).trim() !== "",
  );
  if (shelved.length === 0) return AI_NO_OUTPUT;
  return prediction;
}

export function isAiDelivered(fix: AgentFixRecord): boolean {
  const attribution = deriveAttribution(fix);
  return attribution === "ai_delivered" || attribution === "ai_assisted";
}

// AI quality predictions that count as "已判定" (the assessment reached a
// verdict). "unknown" / missing / drifting enum values stay out — they are the
// pending-judgement queue, not a quality datapoint.
const JUDGED_QUALITIES = new Set([
  "likely_correct",
  "likely_needs_changes",
  "likely_wrong",
]);

export function qualityJudgement(fix: AgentFixRecord): string {
  const quality = fix.p4_assessment?.quality_prediction?.trim() ?? "";
  return JUDGED_QUALITIES.has(quality) ? quality : "";
}

export function isPendingJudgement(fix: AgentFixRecord): boolean {
  return qualityJudgement(fix) === "";
}

// ---------------------------------------------------------------------------
// Verifiable AI output — the fix-rate denominator.
//
// The fix rate only counts tickets where the AI produced something AND the
// assessment could actually reach that evidence. Access problems are detected
// from the assessment warnings by FAMILY matching, not exact enum values:
// production agents emit drifting variants ("p4_lookup_unavailable_for_
// candidate_cls", "claimed_shelved_cl_not_found_on_reachable_p4", ...), so an
// exact match would silently let blocked rows into the denominator.
// ---------------------------------------------------------------------------

const BLOCKED_WARNING_RE = /(unavailable|not_found|unreachable|unauthorized)/i;

export type BlockedFamily = "evidence_endpoint" | "swarm" | "p4" | "other";

// Classifies one warning string: null when it is not an access-blocked
// warning; otherwise which system the block belongs to. Order matters — the
// multica evidence endpoint markers often also contain "p4".
export function blockedWarningFamily(warning: string): BlockedFamily | null {
  const w = warning.trim().toLowerCase();
  if (!w || !BLOCKED_WARNING_RE.test(w)) return null;
  if (w.includes("evidence_endpoint")) return "evidence_endpoint";
  if (w.includes("swarm")) return "swarm";
  if (w.includes("p4") || w.includes("shelve") || w.includes("cl")) return "p4";
  return "other";
}

export function isAccessBlocked(fix: AgentFixRecord): boolean {
  return (fix.p4_assessment?.warnings ?? []).some(
    (w) => blockedWarningFamily(String(w)) !== null,
  );
}

function hasAiOutputEvidence(fix: AgentFixRecord): boolean {
  const p4 = fix.p4_assessment;
  if (!p4) return false;
  const shelved = (p4.ai_shelved_cls ?? []).filter(
    (cl) => String(cl).trim() !== "",
  );
  return shelved.length > 0 || (p4.swarm_reviews ?? []).length > 0;
}

// A ticket enters the fix-rate denominator only when the assessment completed,
// the AI produced evidence (shelve CL or swarm review), and nothing blocked
// access to that evidence.
export function isVerifiableOutput(fix: AgentFixRecord): boolean {
  return (
    fix.p4_assessment?.assessment_status === "completed" &&
    hasAiOutputEvidence(fix) &&
    !isAccessBlocked(fix)
  );
}

// Per-family counts of access-blocked completed assessments, for the analysis
// card. A row counts once per family it hits (a row with both p4 and swarm
// block warnings contributes to both), plus the completed total for shares.
export interface BlockedStats {
  completed: number;
  families: Array<{ family: BlockedFamily; count: number }>;
}

export function computeBlockedStats(rows: AgentFixRecord[]): BlockedStats {
  const counts = new Map<BlockedFamily, number>();
  let completed = 0;
  for (const fix of rows) {
    if (fix.p4_assessment?.assessment_status !== "completed") continue;
    completed += 1;
    const families = new Set<BlockedFamily>();
    for (const warning of fix.p4_assessment?.warnings ?? []) {
      const family = blockedWarningFamily(String(warning));
      if (family) families.add(family);
    }
    for (const family of families) {
      counts.set(family, (counts.get(family) ?? 0) + 1);
    }
  }
  const order: BlockedFamily[] = ["swarm", "p4", "evidence_endpoint", "other"];
  return {
    completed,
    families: order
      .filter((f) => (counts.get(f) ?? 0) > 0)
      .map((family) => ({ family, count: counts.get(family)! })),
  };
}

// The record's day axis in the viewer's timezone — the latest run's completion
// day, falling back to start/created for running or queued rows. en-CA gives a
// locale-neutral YYYY-MM-DD; a bad tz falls back to the raw ISO day.
export function fixDayIso(fix: AgentFixRecord, tz: string): string {
  const iso = fix.completed_at ?? fix.started_at ?? fix.created_at;
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso.slice(0, 10);
  try {
    return new Intl.DateTimeFormat("en-CA", {
      timeZone: tz,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
    }).format(d);
  } catch {
    return iso.slice(0, 10);
  }
}

// Split a 2×-window fetch into the user-selected trailing window and the
// equal-length window before it (for the KPI period-over-period delta).
export function splitOperationsWindow(
  fixes: AgentFixRecord[],
  days: number,
  tz: string,
): { current: AgentFixRecord[]; previous: AgentFixRecord[] } {
  const today = todayIso(tz);
  const currentCutoff = addDaysIso(today, -(days - 1));
  const previousCutoff = addDaysIso(today, -(days * 2 - 1));
  const current: AgentFixRecord[] = [];
  const previous: AgentFixRecord[] = [];
  for (const fix of fixes) {
    const day = fixDayIso(fix, tz);
    if (!day) continue;
    if (day >= currentCutoff) current.push(fix);
    else if (day >= previousCutoff) previous.push(fix);
  }
  return { current, previous };
}

// One ratio KPI: `value` is null when the denominator is 0 (render "—", never
// a fake 0% or 100%).
export interface OperationsRate {
  value: number | null;
  numerator: number;
  denominator: number;
}

function rate(numerator: number, denominator: number): OperationsRate {
  return {
    value: denominator > 0 ? numerator / denominator : null,
    numerator,
    denominator,
  };
}

// Delivery funnel counts. Stages are nested: passed ⊆ judged ⊆ verifiable ⊆
// p4Covered; externalDone is the upstream gate. `verifiable` is the fix-rate
// denominator pool — completed assessments with reachable AI output evidence.
// Judgement is the AI quality analysis — there is no human review in this flow.
export interface OperationsFunnel {
  total: number;
  externalDone: number;
  p4Covered: number;
  verifiable: number;
  judged: number;
  passed: number;
}

export interface OperationsKpis {
  funnel: OperationsFunnel;
  // AI 修复率：通过 / 可验证产出且已判定
  passRate: OperationsRate;
  // AI 交付占比：AI 提交+辅助 / 外部完成
  deliveryShare: OperationsRate;
  // AI 无产出率：已证实无产出 / 全部参与记录
  noOutputRate: OperationsRate;
  // 无法判断占比：评估完成但没有质量结论 / 全部参与记录
  unjudgedRate: OperationsRate;
}

export function computeOperationsKpis(rows: AgentFixRecord[]): OperationsKpis {
  let externalDone = 0;
  let p4Covered = 0;
  let aiDelivered = 0;
  let verifiable = 0;
  let judged = 0;
  let passed = 0;
  let noOutput = 0;
  let unjudged = 0;
  for (const fix of rows) {
    if (fix.external?.done === true) externalDone += 1;
    if (fix.p4_assessment) p4Covered += 1;
    const attribution = deriveAttribution(fix);
    if (attribution === AI_NO_OUTPUT) noOutput += 1;
    if (attribution === "ai_delivered" || attribution === "ai_assisted") {
      aiDelivered += 1;
    }
    const completed = fix.p4_assessment?.assessment_status === "completed";
    const quality = qualityJudgement(fix);
    if (completed && quality === "") unjudged += 1;
    if (!isVerifiableOutput(fix)) continue;
    verifiable += 1;
    if (quality !== "") {
      judged += 1;
      if (quality === "likely_correct") passed += 1;
    }
  }
  return {
    funnel: {
      total: rows.length,
      externalDone,
      p4Covered,
      verifiable,
      judged,
      passed,
    },
    passRate: rate(passed, judged),
    deliveryShare: rate(aiDelivered, externalDone),
    noOutputRate: rate(noOutput, rows.length),
    unjudgedRate: rate(unjudged, rows.length),
  };
}

// Weekly trend point: the three headline rates folded per trailing calendar
// week (Mon–Sun, viewer tz). Rates are 0–100 percentages for the chart axis;
// null when that week's denominator is 0 so recharts leaves a gap instead of
// painting a fake zero.
export interface OperationsTrendPoint extends WeekShell {
  passRate: number | null;
  deliveryShare: number | null;
  noOutputRate: number | null;
}

function pct(r: OperationsRate): number | null {
  return r.value == null ? null : Math.round(r.value * 1000) / 10;
}

export function computeOperationsTrend(
  rows: AgentFixRecord[],
  tz: string,
  weekCount: number,
): OperationsTrendPoint[] {
  const shells = buildWeekShells(tz, weekCount);
  const buckets = new Map<string, AgentFixRecord[]>();
  for (const shell of shells) buckets.set(shell.weekStart, []);
  for (const fix of rows) {
    const day = fixDayIso(fix, tz);
    if (!day) continue;
    const bucket = buckets.get(weekStartIso(day));
    if (bucket) bucket.push(fix);
  }
  return shells.map((shell) => {
    const kpis = computeOperationsKpis(buckets.get(shell.weekStart) ?? []);
    return {
      ...shell,
      passRate: pct(kpis.passRate),
      deliveryShare: pct(kpis.deliveryShare),
      noOutputRate: pct(kpis.noOutputRate),
    };
  });
}

// Builds the Swarm links for evidence chips. `base` is the workspace's Helix
// Swarm URL (perforce connection); empty base → no link, the chip renders as
// plain text.
export function swarmReviewUrl(base: string, reviewId: string): string {
  const trimmed = base.trim().replace(/\/+$/, "");
  if (!trimmed || !reviewId) return "";
  return `${trimmed}/reviews/${encodeURIComponent(reviewId)}`;
}

export function swarmChangeUrl(base: string, cl: string): string {
  const trimmed = base.trim().replace(/\/+$/, "");
  if (!trimmed || !cl) return "";
  return `${trimmed}/changes/${encodeURIComponent(cl)}`;
}
