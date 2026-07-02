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

// Delivery funnel counts. Stages are nested: passed ⊆ judged ⊆ aiDelivered;
// externalDone / p4Covered are the two upstream gates. Judgement is the AI
// quality analysis — there is no human review in this flow.
export interface OperationsFunnel {
  total: number;
  externalDone: number;
  p4Covered: number;
  aiDelivered: number;
  judged: number;
  passed: number;
}

export interface OperationsKpis {
  funnel: OperationsFunnel;
  // 大概率正确 / AI 交付且已判定
  passRate: OperationsRate;
  // AI 交付（提交+辅助）/ 外部完成
  deliveryShare: OperationsRate;
  // AI 无产出 / 全部参与记录
  noOutputRate: OperationsRate;
}

export function computeOperationsKpis(rows: AgentFixRecord[]): OperationsKpis {
  let externalDone = 0;
  let p4Covered = 0;
  let aiDelivered = 0;
  let judged = 0;
  let passed = 0;
  let noOutput = 0;
  for (const fix of rows) {
    if (fix.external?.done === true) externalDone += 1;
    if (fix.p4_assessment) p4Covered += 1;
    const attribution = deriveAttribution(fix);
    if (attribution === AI_NO_OUTPUT) noOutput += 1;
    const delivered =
      attribution === "ai_delivered" || attribution === "ai_assisted";
    if (!delivered) continue;
    aiDelivered += 1;
    const quality = qualityJudgement(fix);
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
      aiDelivered,
      judged,
      passed,
    },
    passRate: rate(passed, judged),
    deliveryShare: rate(aiDelivered, externalDone),
    noOutputRate: rate(noOutput, rows.length),
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
