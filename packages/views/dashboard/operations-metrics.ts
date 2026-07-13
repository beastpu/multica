import type { AgentFixRecord } from "@multica/core/types";
import { addDaysIso, todayIso } from "../runtimes/utils";

// ---------------------------------------------------------------------------
// Operations page metrics
//
// Pure derivations over the agent-fixes feed. Everything here is computed
// client-side from GET /api/operations/agent-fixes rows fetched for the
// user-selected trailing window.
// ---------------------------------------------------------------------------

// Derived delivery attribution. Extends the AI prediction with two values the
// backend doesn't emit, for completed assessments where the prediction is
// non-AI and no AI shelve exists:
// - `ai_plan_no_record` — the agent left an issue comment (it completed a
//   comment task, i.e. proposed a plan) but never landed a shelve/Swarm
//   record. A process gap: the plan existed, the P4 artifact didn't.
// - `ai_no_output` — no comment either; the agent produced nothing visible
//   (e.g. it hit an auth failure before it could do anything).
// Two guards keep this honest:
// - Rows without a completed assessment keep the raw prediction; absence of
//   evidence is not evidence of absence.
// - An `unknown` prediction stays `unknown`: the assessment skill outputs
//   unknown + empty CL arrays when P4/Swarm lookup was unavailable, and
//   "couldn't find evidence" must not be counted as "produced nothing".
export const AI_NO_OUTPUT = "ai_no_output";
export const AI_PLAN_NO_RECORD = "ai_plan_no_record";

const PROVEN_NON_AI_ATTRIBUTIONS = new Set([
  "human_delivered",
  "unattributed",
  "conflict",
]);

// The agent commented a plan on the issue. Newer servers send
// agent_comment_count (every agent comment on the issue) — authoritative.
// Older servers omit it; fall back to the last_comment snippet, which is the
// agent's latest comment (the SQL filters author_type='agent').
function hasAgentPlanComment(fix: AgentFixRecord): boolean {
  if (typeof fix.agent_comment_count === "number") {
    return fix.agent_comment_count > 0;
  }
  return (
    fix.last_comment_author_type === "agent" &&
    (fix.last_comment ?? "").trim() !== ""
  );
}

export function deriveAttribution(fix: AgentFixRecord): string {
  const p4 = fix.p4_assessment;
  const prediction = p4?.delivery_attribution_prediction?.trim() || "unknown";
  if (p4?.assessment_status !== "completed") return prediction;
  if (!PROVEN_NON_AI_ATTRIBUTIONS.has(prediction)) return prediction;
  const shelved = (p4.ai_shelved_cls ?? []).filter(
    (cl) => String(cl).trim() !== "",
  );
  if (shelved.length === 0) {
    return hasAgentPlanComment(fix) ? AI_PLAN_NO_RECORD : AI_NO_OUTPUT;
  }
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
// Distribution buckets — the reconciliation layer.
//
// The KPI drawers, detail filters, and drill-downs must agree with the KPI
// numerators, so they share these bucket functions.
// Rows without a COMPLETED assessment go into a dedicated "unassessed" bucket
// instead of polluting "unknown"/"证据不足": an unfinished assessment is a
// queue state, not a verdict. With that split, the quality card's "无法判断"
// count is exactly the undetermined-share KPI numerator.
// ---------------------------------------------------------------------------

export const UNASSESSED = "unassessed";

function isAssessmentCompleted(fix: AgentFixRecord): boolean {
  return fix.p4_assessment?.assessment_status === "completed";
}

// Quality distribution/filter bucket: unassessed | likely_correct |
// likely_needs_changes | likely_wrong | unknown (completed, no verdict) |
// <drifting raw value>.
export function qualityBucket(fix: AgentFixRecord): string {
  if (!isAssessmentCompleted(fix)) return UNASSESSED;
  const quality = fix.p4_assessment?.quality_prediction?.trim() ?? "";
  return quality === "" ? "unknown" : quality;
}

// Attribution distribution/filter bucket: unassessed for unfinished rows,
// else the derived attribution (which already only classifies completed
// assessments — see deriveAttribution).
export function attributionBucket(fix: AgentFixRecord): string {
  if (!isAssessmentCompleted(fix)) return UNASSESSED;
  return deriveAttribution(fix);
}

// ---------------------------------------------------------------------------
// Access-blocked warning classification.
//
// Used by blocker breakdowns — not by the quality-pipeline gate. Production
// agents emit drifting variants
// ("p4_lookup_unavailable_for_candidate_cls", ...), so classify by FAMILY.
// ---------------------------------------------------------------------------

const BLOCKED_WARNING_RE = /(unavailable|not_found|unreachable|unauthorized)/i;

export type BlockedFamily =
  | "auth"
  | "identification"
  | "evidence_endpoint"
  | "swarm"
  | "p4"
  | "other";

// Classifies one warning string: null when it is not an access-blocked
// warning; otherwise the actionable cause. Cause families (auth,
// identification) come before system families so a "swarm_api_unauthorized"
// reads as an auth problem, not a Swarm outage — the fix (grant credentials
// vs. check the service) is what ops routes on. Order matters — the multica
// evidence endpoint markers often also contain "p4".
export function blockedWarningFamily(warning: string): BlockedFamily | null {
  const w = warning.trim().toLowerCase();
  if (!w || !BLOCKED_WARNING_RE.test(w)) return null;
  if (w.includes("evidence_endpoint")) return "evidence_endpoint";
  if (w.includes("unauthorized")) return "auth";
  if (w.includes("not_found") && /(cl|shelve|branch)/.test(w)) {
    return "identification";
  }
  if (w.includes("swarm")) return "swarm";
  if (w.includes("p4") || w.includes("shelve") || w.includes("cl")) return "p4";
  return "other";
}

// ---------------------------------------------------------------------------
// Assessable AI output — the quality-pipeline gate (verifiable → judged →
// passed).
//
// Quality is about the PLAN: the assessment judges whether the AI's shelved
// fix / proposal is correct by reading the code — it does not need the fix to
// have shipped. So the gate is just "completed assessment with an AI plan"; a
// committed CL is NOT required. Delivery (did the plan actually ship?) is a
// separate axis, measured by contribution and delivery attribution.
// Requiring a committed CL here used to drop ~30% of already-judged plans
// (their commit evidence was unreachable), understating coverage badly.
// ---------------------------------------------------------------------------

function hasNonEmptyCl(cls: Array<string | number> | undefined): boolean {
  return (cls ?? []).some((cl) => String(cl).trim() !== "");
}

// AI produced a fix plan: a shelve CL, or a Swarm review (the proposed fix).
// quality_prediction judges whether the *fix* is correct, not whether AI
// produced it — so without this gate a human-delivered "likely_correct" would
// count as an AI win.
function aiProducedPlan(fix: AgentFixRecord): boolean {
  const p4 = fix.p4_assessment;
  if (!p4) return false;
  return hasNonEmptyCl(p4.ai_shelved_cls) || (p4.swarm_reviews ?? []).length > 0;
}

// The ticket actually shipped: a committed/submitted CL exists. Who submitted
// it doesn't matter. Shelve CLs are NOT committed CLs. Not part of the quality
// gate — used by the missing-human-CL process-gap check below.
function hasCommittedCl(fix: AgentFixRecord): boolean {
  const p4 = fix.p4_assessment;
  if (!p4) return false;
  return (
    hasNonEmptyCl(p4.swarm_committed_cls) ||
    hasNonEmptyCl(p4.external_committed_cls)
  );
}

// The "missing human CL" process gap: the assessment completed, found no
// committed CL anywhere, and flagged the canonical `missing_external_cl`
// warning (the SKILL requires it whenever the external item is done but no
// submitted CL exists in any evidence source). Ops fixes this with work-item
// hygiene — humans recording their final CL — not with agent changes.
export function hasMissingExternalClWarning(fix: AgentFixRecord): boolean {
  if (fix.p4_assessment?.assessment_status !== "completed") return false;
  if (hasCommittedCl(fix)) return false;
  return (fix.p4_assessment?.warnings ?? []).some(
    (w) => String(w).trim().toLowerCase() === "missing_external_cl",
  );
}

// A ticket enters the quality pipeline when the assessment completed and AI
// produced a plan to judge. Whether that plan shipped (committed CL) is a
// delivery question, not an assessability one — it lives in contribution.
export function isVerifiableOutput(fix: AgentFixRecord): boolean {
  return (
    fix.p4_assessment?.assessment_status === "completed" &&
    aiProducedPlan(fix)
  );
}

// AI's role in one ticket's delivery — the single source for the composition
// bar, the KPI loop, and the card-breakdown drawer, so their counts always
// reconcile. "none" = no AI involvement (no plan artifact, not attributed).
export type DeliveryRole = "direct" | "assisted" | "unconverted" | "none";

export function deliveryRole(fix: AgentFixRecord): DeliveryRole {
  const attribution = deriveAttribution(fix);
  if (attribution === "ai_delivered") return "direct";
  if (attribution === "ai_assisted") return "assisted";
  return aiProducedPlan(fix) ? "unconverted" : "none";
}

// The canonical Operations participation predicate. Contribution, delivery
// composition, rate denominators, and drawers all reuse this exact rule so
// "AI 接手" and "AI 参与" always reconcile.
export function isAiParticipated(fix: AgentFixRecord): boolean {
  return deliveryRole(fix) !== "none";
}

export function isExternalDone(fix: AgentFixRecord): boolean {
  return (
    fix.external?.done === true || fix.external?.mapped_status === "done"
  );
}

// Per-family counts of access-blocked completed assessments. A row counts once
// per family it hits (a row with both p4 and swarm
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
  const order: BlockedFamily[] = [
    "auth",
    "identification",
    "swarm",
    "p4",
    "evidence_endpoint",
    "other",
  ];
  return {
    completed,
    families: order
      .filter((f) => (counts.get(f) ?? 0) > 0)
      .map((family) => ({ family, count: counts.get(family)! })),
  };
}

// The record's day axis in the viewer's timezone. Prefers the server's
// activity_at — the same instant the feed's SQL window filters on (external
// item's last update when bound), so client-side period splitting agrees with
// the SQL window; older servers omit it and the run timestamps take over.
// en-CA gives a locale-neutral YYYY-MM-DD; a bad tz falls back to the raw ISO
// day.
export function fixDayIso(fix: AgentFixRecord, tz: string): string {
  const iso =
    (fix.activity_at?.trim() ? fix.activity_at : null) ??
    fix.completed_at ??
    fix.started_at ??
    fix.created_at;
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

// Trim a fetch to the user-selected trailing window in the viewer's timezone,
// so the client-side day axis and the SQL window agree on which rows count.
export function trimOperationsWindow(
  fixes: AgentFixRecord[],
  days: number,
  tz: string,
): AgentFixRecord[] {
  const today = todayIso(tz);
  const cutoff = addDaysIso(today, -(days - 1));
  return fixes.filter((fix) => {
    const day = fixDayIso(fix, tz);
    return !!day && day >= cutoff;
  });
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

// Delivery funnel diagnostics. aiPlanned/verifiable describe artifact and
// assessment progress; judged/passed describe explicit quality outcomes among
// AI-participated rows. The two sequences intentionally overlap but are not
// assumed to be nested because a structured attribution can exist without a
// shelve/Swarm artifact in older records.
export interface OperationsFunnel {
  total: number;
  externalDone: number;
  // The agent did something visible: commented a plan or produced P4/Swarm
  // evidence.
  aiEngaged: number;
  // A shelve CL or Swarm review exists — the plan became a P4 artifact.
  aiPlanned: number;
  verifiable: number;
  judged: number;
  passed: number;
}

// A MECE partition of the page's eligible reporting pool by AI delivery role:
//   directDelivered + assisted + unconverted + notParticipated === externalDone
// participation = directDelivered + assisted + unconverted (AI was involved).
export interface DeliveryComposition {
  directDelivered: number; // AI's CL is the final CL (ai_delivered)
  assisted: number; // human shipped an AI-equivalent CL (ai_assisted)
  unconverted: number; // AI produced a plan but it did not reach delivery
  notParticipated: number; // shipped with no AI involvement at all
}

export interface OperationsKpis {
  funnel: OperationsFunnel;
  // MECE breakdown of eligible reporting rows by AI role.
  composition: DeliveryComposition;
  // AI repair contribution: eligible rows with AI participation / all
  // eligible rows. The page defines eligibility as Feishu 测试通过 plus a
  // synced Multica issue at done. Binding-only rows stay in the denominator so
  // missing Agent assignment and missing dispatch remain visible problems.
  contributionRate: OperationsRate;
  // Shared denominator for quality, automatic, and assisted repair: AI
  // participated and produced one explicit quality result (pass / needs
  // changes / fail). Unknown and unrecognised values are excluded.
  judgedCount: number;
  // AI-marked pass / all AI-judged plans. Unknown or missing judgements stay
  // outside the denominator instead of being treated as quality failures.
  qualityRate: OperationsRate;
  automaticRate: OperationsRate;
  // AI-assisted includes both attributed assisted delivery and an AI plan that
  // participated but did not become the final delivery, per the ops definition.
  assistedRate: OperationsRate;
  // Demoted data-health counts (rendered as a muted footnote, not a headline
  // card): rows whose assessment hasn't completed (the queue backlog) and
  // done tickets missing a recorded human CL (work-item hygiene, not an AI
  // defect). Completed assessment outcomes live in the KPI drawers.
  unassessed: number;
  missingExternalCl: number;
}

export function computeOperationsKpis(rows: AgentFixRecord[]): OperationsKpis {
  let externalDone = 0;
  let aiEngaged = 0;
  let aiPlanned = 0;
  let directDelivered = 0;
  let assisted = 0;
  let unconverted = 0;
  let notParticipated = 0;
  let verifiable = 0;
  let judged = 0;
  let passed = 0;
  let unassessed = 0;
  let missingExternalCl = 0;
  let handled = 0;
  let automatic = 0;
  let assistedFixes = 0;
  for (const fix of rows) {
    const done = isExternalDone(fix);
    if (done) externalDone += 1;
    if (fix.p4_assessment?.assessment_status !== "completed") unassessed += 1;
    if (hasMissingExternalClWarning(fix)) missingExternalCl += 1;
    const planned = aiProducedPlan(fix);
    if (planned) aiPlanned += 1;
    // Engagement is looser than participation: a comment-only plan counts as
    // the agent showing up (the funnel's engaged→planned drop), but not as
    // artifact-driven participation.
    if (planned || hasAgentPlanComment(fix)) aiEngaged += 1;
    // Delivery role is also the canonical contribution/pickup predicate.
    const role = deliveryRole(fix);
    const participated = role !== "none";
    if (done && participated) handled += 1;
    if (done) {
      if (role === "direct") directDelivered += 1;
      else if (role === "assisted") assisted += 1;
      else if (role === "unconverted") unconverted += 1;
      else notParticipated += 1;
    }
    const quality = qualityJudgement(fix);
    if (isVerifiableOutput(fix)) verifiable += 1;
    if (!done || !participated || quality === "") continue;
    judged += 1;
    // Automatic repair requires both direct AI delivery and a passing quality
    // judgement. A direct submission with unknown/failed quality is not an
    // automatic repair success.
    if (role === "direct" && quality === "likely_correct") automatic += 1;
    if (role === "assisted" || role === "unconverted") assistedFixes += 1;
    if (quality === "likely_correct") passed += 1;
  }
  return {
    funnel: {
      total: rows.length,
      externalDone,
      aiEngaged,
      aiPlanned,
      verifiable,
      judged,
      passed,
    },
    composition: { directDelivered, assisted, unconverted, notParticipated },
    contributionRate: rate(handled, externalDone),
    judgedCount: judged,
    qualityRate: rate(passed, judged),
    automaticRate: rate(automatic, judged),
    assistedRate: rate(assistedFixes, judged),
    unassessed,
    missingExternalCl,
  };
}

// ---------------------------------------------------------------------------
// Evidence derivation — the normalized P4/Swarm evidence view over one row.
//
// Structured assessment fields win; legacy comment-regex extraction is the
// fallback for rows written before the assessment pipeline existed. Shared by
// the detail table cells and drawers.
// ---------------------------------------------------------------------------

function compactList(values: Array<string | number> | undefined): string {
  return (values ?? [])
    .map((v) => String(v).trim())
    .filter(Boolean)
    .join(", ");
}

function extractToken(text: string, patterns: RegExp[]): string {
  for (const pattern of patterns) {
    const match = text.match(pattern);
    if (match?.[1]) return match[1];
  }
  return "";
}

export function derivedEvidence(fix: AgentFixRecord) {
  const comment = fix.last_comment ?? "";
  const p4 = fix.p4_assessment;
  const reviewChanges = compactList(
    (p4?.swarm_reviews ?? []).flatMap((review) => review.changes ?? []),
  );
  const reviewCommits = compactList(
    (p4?.swarm_reviews ?? []).flatMap((review) => review.commits ?? []),
  );
  const swarm =
    firstSwarmReview(fix) ||
    extractToken(comment, [
      /\b(SW-\d+)\b/i,
      /\bswarm(?:\s+review)?[:#\s]+(\d+)\b/i,
    ]);
  const shelve =
    compactList(p4?.ai_shelved_cls) ||
    extractToken(comment, [
      /\bshelv(?:e|ed)?(?:\s+CL)?[:#\s]+(\d+)\b/i,
      /\bpending\s+P4\s+CL[:#\s]+(\d+)\b/i,
      /\bCL[:#\s]+(\d+)\b[^\n\r]*(?:shelv(?:e|ed)|已\s*shelve|已\s*shelved)\b/i,
    ]);
  const finalCl =
    String(fix.external?.final_cl ?? "").trim() ||
    compactList(p4?.external_committed_cls) ||
    compactList(p4?.swarm_committed_cls);
  return {
    workstream:
      String(p4?.workstream ?? "").trim() ||
      String(fix.external?.workstream ?? "").trim() ||
      firstSwarmReviewField(fix, "swarm_branch"),
    swarm,
    shelve,
    swarmChanges: compactList(p4?.swarm_change_cls) || reviewChanges,
    swarmCommits: compactList(p4?.swarm_committed_cls) || reviewCommits,
    swarmBranch: firstSwarmReviewField(fix, "swarm_branch"),
    eventType: firstSwarmReviewField(fix, "event_type"),
    sentAt: firstSwarmReviewField(fix, "sent_at"),
    finalCl,
  };
}

function firstSwarmReview(fix: AgentFixRecord): string {
  const review = fix.p4_assessment?.swarm_reviews?.find(
    (r) => String(r.review_id ?? r.id ?? "").trim().length > 0,
  );
  return String(review?.review_id ?? review?.id ?? "").trim();
}

// The URL of the first swarm review, when the webhook payload carried one —
// preferred over rebuilding from the connection base.
export function firstSwarmReviewUrl(fix: AgentFixRecord): string {
  const review = fix.p4_assessment?.swarm_reviews?.find(
    (r) => String(r.url ?? "").trim().length > 0,
  );
  return String(review?.url ?? "").trim();
}

function firstSwarmReviewField(
  fix: AgentFixRecord,
  field: "swarm_branch" | "event_type" | "sent_at",
): string {
  const review = fix.p4_assessment?.swarm_reviews?.find(
    (r) => String(r[field] ?? "").trim().length > 0,
  );
  return String(review?.[field] ?? "").trim();
}

function hasP4Assessment(fix: AgentFixRecord): boolean {
  const p4 = fix.p4_assessment;
  if (!p4) return false;
  return Boolean(
      p4.assessment_status ||
      p4.delivery_attribution_prediction ||
      p4.quality_prediction ||
      p4.workstream ||
      derivedEvidence(fix).swarm ||
      derivedEvidence(fix).shelve ||
      derivedEvidence(fix).finalCl,
  );
}

export function hasP4Signal(fix: AgentFixRecord): boolean {
  if (hasP4Assessment(fix)) return true;
  const evidence = derivedEvidence(fix);
  return Boolean(evidence.swarm || evidence.shelve || evidence.finalCl);
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
