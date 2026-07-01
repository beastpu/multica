"use client";

import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
  type RefObject,
} from "react";
import { Download, List, Play, Radar, RefreshCw, Search, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Input } from "@multica/ui/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { useWorkspaceId } from "@multica/core/hooks";
import { paths, useWorkspaceSlug } from "@multica/core/paths";
import { agentListOptions } from "@multica/core/workspace/queries";
import { feishuProjectIssueStatusesOptions } from "@multica/core/feishu-project/queries";
import {
  operationsFixesOptions,
  useTriggerAgentFixP4Assessment,
  useUpdateAgentFixReview,
  useOperationsViewStore,
  clampOperationsColumnWidth,
  OPERATIONS_DEFAULT_WIDTHS,
  OPERATIONS_COLUMN_KEYS,
  type OperationsColumnKey,
} from "@multica/core/dashboard";
import type {
  AgentFixRecord,
  IssueStatus,
} from "@multica/core/types";
import { PageHeader } from "../../layout/page-header";
import { ActorAvatar } from "../../common/actor-avatar";
import { StatusIcon } from "../../issues/components/status-icon";
import { AppLink } from "../../navigation";
import { useViewingTimezone } from "../../common/use-viewing-timezone";
import { useT } from "../../i18n";
import {
  AgentFixReviewDialog,
  ToneBadge,
  agentFixEnumLabel,
  agentFixReviewReasonLabels,
  agentFixEnumTone,
  type Tone,
  type UsageT,
} from "./agent-fix-review";
import { Segmented } from "./segmented";

const ALL_AGENTS = "__all__";
const ALL_STATUSES = "__all__";
const ALL_WORKSTREAMS = "__all__";
const ALL_ATTRIBUTIONS = "__all__";
const ALL_QUALITIES = "__all__";
const DETAIL_TAB = "detail";
const ANALYSIS_TAB = "analysis";
type OperationsTab = typeof DETAIL_TAB | typeof ANALYSIS_TAB;
type SelectOption = { value: string; label: string };

// Trailing window. `1d` is the last 24h; the rest mirror the Usage dashboard's
// daily-dimension options (a flat record list has no weekly chart grain). 30d
// default matches the dashboard.
const RANGES = [
  { label: "1d", days: 1 },
  { label: "7d", days: 7 },
  { label: "30d", days: 30 },
  { label: "90d", days: 90 },
] as const;
type OpsRange = (typeof RANGES)[number]["days"];

// The "状态" column mirrors the issues UI: the same StatusIcon + the label
// from the `issues` i18n namespace. These are the known values; an unknown
// server-side value still renders its raw text (no icon), so enum drift
// downgrades instead of crashing.
const ISSUE_STATUSES = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
  "blocked",
  "cancelled",
] as const;
function isKnownIssueStatus(s: string): s is IssueStatus {
  return (ISSUE_STATUSES as readonly string[]).includes(s);
}

// Stable empty reference so the loading→data transition doesn't rebuild the
// filtered memo on every render.
const EMPTY: AgentFixRecord[] = [];

// --- Resizable-column layout -------------------------------------------------
// Column order: Issue, external state, agent, P4 evidence, AI attribution,
// AI quality, human review, eval, date. The external status column stays fixed;
// the rest are user-resizable. The legacy `status` width slot backs the P4
// evidence column so stored preferences remain scoped to this page.
const EXTERNAL_PX = 138;
const COLUMN_GAP_PX = 12; // matches gap-3
const CARD_PADDING_X_PX = 32; // px-4 on the header + each row (16 × 2)

const COLUMN_VAR: Record<OperationsColumnKey, string> = {
  agent: "--ops-col-agent",
  issue: "--ops-col-issue",
  status: "--ops-col-status",
  attribution: "--ops-col-attribution",
  quality: "--ops-col-quality",
  review: "--ops-col-review",
  eval: "--ops-col-eval",
  time: "--ops-col-time",
};

const GRID_TEMPLATE = `var(${COLUMN_VAR.issue}) ${EXTERNAL_PX}px var(${COLUMN_VAR.agent}) var(${COLUMN_VAR.status}) var(${COLUMN_VAR.attribution}) var(${COLUMN_VAR.quality}) var(${COLUMN_VAR.review}) var(${COLUMN_VAR.eval}) var(${COLUMN_VAR.time})`;

const GRID_STYLE: CSSProperties = { gridTemplateColumns: GRID_TEMPLATE };

// Keep interactive resize handles clickable inside any desktop drag region.
const NO_DRAG_STYLE = { WebkitAppRegion: "no-drag" } as CSSProperties;

// Total intrinsic width below which the card must scroll horizontally rather
// than crush its fixed columns — the sum of every track plus gaps and padding.
function operationsMinWidth(w: Record<OperationsColumnKey, number>): number {
  return (
    w.agent +
    w.issue +
    w.status +
    w.attribution +
    w.quality +
    w.review +
    w.eval +
    w.time +
    EXTERNAL_PX +
    COLUMN_GAP_PX * 8 +
    CARD_PADDING_X_PX
  );
}

// Inline style for the card: seed each resizable column's CSS var from the
// persisted width and set the scroll floor.
function cardStyle(w: Record<OperationsColumnKey, number>): CSSProperties {
  return {
    [COLUMN_VAR.agent]: `${w.agent}px`,
    [COLUMN_VAR.issue]: `${w.issue}px`,
    [COLUMN_VAR.status]: `${w.status}px`,
    [COLUMN_VAR.attribution]: `${w.attribution}px`,
    [COLUMN_VAR.quality]: `${w.quality}px`,
    [COLUMN_VAR.review]: `${w.review}px`,
    [COLUMN_VAR.eval]: `${w.eval}px`,
    [COLUMN_VAR.time]: `${w.time}px`,
    minWidth: `${operationsMinWidth(w)}px`,
  } as CSSProperties;
}

// Day-granularity label for a fix's time axis, in the viewer's timezone — the
// same calendar the usage dashboard slices on, so "按天" lines up across both
// pages. en-CA yields a locale-neutral YYYY-MM-DD; a bad tz falls back to the
// raw ISO day.
function formatDay(iso: string | null, tz: string): string {
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

// Debounce delay before a typed search term hits the server. Long enough to
// coalesce a burst of keystrokes, short enough to feel responsive.
const SEARCH_DEBOUNCE_MS = 300;

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

function derivedEvidence(fix: AgentFixRecord) {
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
    ]);
  const finalCl =
    compactList(p4?.external_committed_cls) ||
    compactList(p4?.swarm_committed_cls) ||
    extractToken(comment, [
      /\bfinal\s+CL[:#\s]+(\d+)\b/i,
      /\bsubmitted\s+as\s+CL[:#\s]+(\d+)\b/i,
      /\bCL[:#\s]+(\d+)\b/i,
    ]);
  return {
    workstream: p4?.workstream ?? "",
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

function hasP4Signal(fix: AgentFixRecord): boolean {
  if (hasP4Assessment(fix)) return true;
  const evidence = derivedEvidence(fix);
  return Boolean(evidence.swarm || evidence.shelve || evidence.finalCl);
}

function confidenceLabel(confidence: number | null | undefined): string {
  if (typeof confidence !== "number" || Number.isNaN(confidence)) return "";
  return `${Math.round(confidence * 100)}%`;
}

const MISMATCH_EVALS = new Set([
  "overestimated",
  "underestimated",
  "wrong_attribution",
  "mismatch",
]);

function isMismatchEval(fix: AgentFixRecord): boolean {
  return MISMATCH_EVALS.has((fix.ai_judgement_eval ?? "").trim());
}

function compactKey(value: string | undefined | null, fallback: string): string {
  const key = value?.trim();
  return key && key.length > 0 ? key : fallback;
}

function sortedUniqueOptions(
  rows: AgentFixRecord[],
  getValue: (row: AgentFixRecord) => string | undefined,
): string[] {
  return Array.from(
    new Set(rows.map((row) => getValue(row)?.trim()).filter(Boolean) as string[]),
  ).sort((a, b) => a.localeCompare(b));
}

export const OPERATIONS_P4_CSV_HEADERS = [
  "Issue",
  "Issue Title",
  "External Work Item ID",
  "External URL",
  "External Status",
  "External Done",
  "Project",
  "Version",
  "Workstream",
  "Agent",
  "AI Assessment Status",
  "AI Attribution Prediction",
  "AI Quality Prediction",
  "Confidence",
  "Swarm Review",
  "Swarm Changes",
  "Swarm Commits",
  "Swarm Branch",
  "Swarm Event Type",
  "Swarm Sent At",
  "AI Shelve CL",
  "Swarm Change CL",
  "Final CL",
  "Human Outcome",
  "Reasons",
  "Note",
  "Judgement Eval",
  "Summary",
  "Warnings",
] as const;

function csvCell(value: string | number | boolean | null | undefined): string {
  const text = value == null ? "" : String(value);
  if (!/[",\r\n]/.test(text)) return text;
  return `"${text.replace(/"/g, '""')}"`;
}

function csvList(values: Array<string | number> | undefined): string {
  return (values ?? [])
    .map((value) => String(value).trim())
    .filter(Boolean)
    .join("; ");
}

function csvSwarmReviews(fix: AgentFixRecord): string {
  return (fix.p4_assessment?.swarm_reviews ?? [])
    .map((review) => String(review.review_id ?? review.id ?? "").trim())
    .filter(Boolean)
    .join("; ");
}

export function buildOperationsP4AssessmentCsv(rows: AgentFixRecord[]): string {
  const lines = [
    OPERATIONS_P4_CSV_HEADERS.map(csvCell).join(","),
    ...rows.map((fix) => {
      const p4 = fix.p4_assessment;
      const evidence = derivedEvidence(fix);
      const values = [
        fix.issue_identifier,
        fix.issue_title,
        fix.external?.work_item_id,
        fix.external?.url,
        fix.external?.status,
        fix.external?.done,
        fix.external?.project,
        fix.external?.version,
        evidence.workstream,
        fix.agent_name,
        p4?.assessment_status,
        p4?.delivery_attribution_prediction,
        p4?.quality_prediction,
        confidenceLabel(p4?.confidence),
        csvSwarmReviews(fix) || evidence.swarm,
        csvList(p4?.swarm_reviews?.flatMap((review) => review.changes ?? [])) ||
          csvList(p4?.swarm_change_cls),
        csvList(p4?.swarm_reviews?.flatMap((review) => review.commits ?? [])) ||
          csvList(p4?.swarm_committed_cls),
        evidence.swarmBranch,
        evidence.eventType,
        evidence.sentAt,
        csvList(p4?.ai_shelved_cls) || evidence.shelve,
        csvList(p4?.swarm_change_cls),
        evidence.finalCl,
        fix.human_review?.outcome,
        csvList(fix.human_review?.reasons),
        fix.human_review?.note,
        fix.ai_judgement_eval || fix.display_result_status,
        p4?.summary,
        csvList(p4?.warnings),
      ];
      return values.map(csvCell).join(",");
    }),
  ];
  return `\uFEFF${lines.join("\r\n")}\r\n`;
}

function downloadTextFile(filename: string, text: string): void {
  const blob = new Blob([text], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

function exportFilename(): string {
  const day = new Date().toISOString().slice(0, 10);
  return `multica-p4-assessment-${day}.csv`;
}

// One run of a highlighted snippet: `match` segments are the keyword hits.
interface HighlightPart {
  text: string;
  match: boolean;
}

/**
 * Split `text` into alternating plain / matched parts on every case-insensitive
 * occurrence of `keyword`, for rendering with <mark> highlights. Uses literal
 * substring matching (lowercased indexOf), mirroring the server's `position()`
 * filter — so what the backend matched is exactly what we highlight, and a
 * user-typed regex/`%` char is treated literally. An empty keyword (or no hit)
 * returns the whole text as a single plain part.
 */
export function splitHighlight(text: string, keyword: string): HighlightPart[] {
  const term = keyword.trim();
  if (!term) return [{ text, match: false }];
  const hay = text.toLowerCase();
  const needle = term.toLowerCase();
  const parts: HighlightPart[] = [];
  let from = 0;
  let hit = hay.indexOf(needle, from);
  if (hit < 0) return [{ text, match: false }];
  while (hit >= 0) {
    if (hit > from) parts.push({ text: text.slice(from, hit), match: false });
    parts.push({ text: text.slice(hit, hit + needle.length), match: true });
    from = hit + needle.length;
    hit = hay.indexOf(needle, from);
  }
  if (from < text.length) parts.push({ text: text.slice(from), match: false });
  return parts;
}

/**
 * Operations page — a left-sidebar section sitting under Usage. One row per
 * issue an agent has worked on (the latest run only), showing the agent, the
 * issue, the run day, the issue's workflow status, and the agent's most recent
 * comment. A search box filters by that comment's text (server-side, on the
 * agent's latest comment) and highlights the match. Lives at
 * `/{slug}/operations`; backed by GET /api/operations/agent-fixes.
 */
export function OperationsPage() {
  const { t } = useT("usage");
  // Issue workflow-status labels are owned by the `issues` namespace — reuse
  // them so the "状态" column matches what users see elsewhere on issues.
  const { t: tIssues } = useT("issues");
  const wsId = useWorkspaceId();
  const slug = useWorkspaceSlug();
  const viewTZ = useViewingTimezone();
  // Persisted, user-draggable column widths. The card carries them as CSS vars
  // so a drag updates the variable imperatively (no re-render of every row);
  // the store is written only on drag end. `widthsModified` toggles the reset
  // affordance once the user has tuned the layout away from the defaults.
  const columnWidths = useOperationsViewStore((s) => s.columnWidths);
  const resetColumnWidths = useOperationsViewStore((s) => s.resetColumnWidths);
  const cardRef = useRef<HTMLDivElement>(null);
  const widthsModified = OPERATIONS_COLUMN_KEYS.some(
    (k) => columnWidths[k] !== OPERATIONS_DEFAULT_WIDTHS[k],
  );
  const [days, setDays] = useState<OpsRange>(30);
  const [agentFilter, setAgentFilter] = useState<string>(ALL_AGENTS);
  const [statusFilter, setStatusFilter] = useState<string>(ALL_STATUSES);
  const [workstreamFilter, setWorkstreamFilter] = useState<string>(ALL_WORKSTREAMS);
  const [attributionFilter, setAttributionFilter] =
    useState<string>(ALL_ATTRIBUTIONS);
  const [qualityFilter, setQualityFilter] = useState<string>(ALL_QUALITIES);
  const [mismatchOnly, setMismatchOnly] = useState(false);
  const [activeTab, setActiveTab] = useState<OperationsTab>(DETAIL_TAB);
  // `searchInput` is what the user types; `search` is the debounced term that
  // actually keys the query (so we don't refetch on every keystroke).
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [reviewFix, setReviewFix] = useState<AgentFixRecord | null>(null);
  const [triggeringBindingId, setTriggeringBindingId] = useState<string | null>(
    null,
  );
  const updateReview = useUpdateAgentFixReview();
  const triggerAssessment = useTriggerAgentFixP4Assessment();

  useEffect(() => {
    const id = setTimeout(
      () => setSearch(searchInput.trim()),
      SEARCH_DEBOUNCE_MS,
    );
    return () => clearTimeout(id);
  }, [searchInput]);

  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const fixesQuery = useQuery(operationsFixesOptions(wsId, days, search));
  const fixes = fixesQuery.data ?? EMPTY;
  const hasExternalStatuses = fixes.some((fix) => !!fix.external?.status);
  const { data: feishuStatusData } = useQuery(
    feishuProjectIssueStatusesOptions(wsId, hasExternalStatuses),
  );
  const feishuStatusNames = useMemo(() => {
    const out = new Map<string, string>();
    for (const status of feishuStatusData?.statuses ?? []) {
      if (status.key && status.name) out.set(status.key, status.name);
    }
    return out;
  }, [feishuStatusData]);
  const tx = t as unknown as UsageT;

  // Validate the picked agent against the current workspace's list so a stale
  // id (deleted agent, or a leftover after a workspace switch) doesn't silently
  // filter everything to empty while the dropdown still reads a name.
  const effectiveAgent = useMemo(() => {
    if (agentFilter === ALL_AGENTS) return ALL_AGENTS;
    return agents.some((a) => a.id === agentFilter) ? agentFilter : ALL_AGENTS;
  }, [agentFilter, agents]);

  const workstreamOptions = useMemo<SelectOption[]>(() => {
    return sortedUniqueOptions(fixes, (f) => derivedEvidence(f).workstream).map(
      (value) => ({ value, label: value }),
    );
  }, [fixes]);

  const attributionOptions = useMemo<SelectOption[]>(() => {
    return sortedUniqueOptions(fixes, (f) =>
      compactKey(f.p4_assessment?.delivery_attribution_prediction, "unknown"),
    ).map((value) => ({
      value,
      label: agentFixEnumLabel(tx, "attribution", value),
    }));
  }, [fixes, tx]);

  const qualityOptions = useMemo<SelectOption[]>(() => {
    return sortedUniqueOptions(fixes, (f) =>
      compactKey(f.p4_assessment?.quality_prediction, "unknown"),
    ).map((value) => ({
      value,
      label: agentFixEnumLabel(tx, "quality", value),
    }));
  }, [fixes, tx]);

  const rows = useMemo(() => {
    return fixes.filter((f) => {
      if (effectiveAgent !== ALL_AGENTS && f.agent_id !== effectiveAgent) {
        return false;
      }
      if (statusFilter !== ALL_STATUSES && f.issue_status !== statusFilter) {
        return false;
      }
      if (
        workstreamFilter !== ALL_WORKSTREAMS &&
        derivedEvidence(f).workstream !== workstreamFilter
      ) {
        return false;
      }
      if (
        attributionFilter !== ALL_ATTRIBUTIONS &&
        compactKey(
          f.p4_assessment?.delivery_attribution_prediction,
          "unknown",
        ) !== attributionFilter
      ) {
        return false;
      }
      if (
        qualityFilter !== ALL_QUALITIES &&
        compactKey(f.p4_assessment?.quality_prediction, "unknown") !==
          qualityFilter
      ) {
        return false;
      }
      if (mismatchOnly && !isMismatchEval(f)) {
        return false;
      }
      return true;
    });
  }, [
    fixes,
    effectiveAgent,
    statusFilter,
    workstreamFilter,
    attributionFilter,
    qualityFilter,
    mismatchOnly,
  ]);

  const assessmentSummary = useMemo(() => {
    const p4Rows = rows.filter(hasP4Signal);
    const reviewed = rows.filter((f) => {
      const outcome = f.human_review?.outcome ?? "";
      return outcome !== "" && outcome !== "unreviewed";
    });
    const mismatches = rows.filter((f) => {
      return isMismatchEval(f);
    });
    return {
      total: rows.length,
      p4Rows: p4Rows.length,
      reviewed: reviewed.length,
      mismatches: mismatches.length,
      externalDone: rows.filter((f) => f.external?.done === true).length,
      aiDelivered: rows.filter(
        (f) =>
          f.p4_assessment?.delivery_attribution_prediction === "ai_delivered" ||
          f.p4_assessment?.delivery_attribution_prediction === "ai_assisted",
      ).length,
      accepted: rows.filter((f) => f.human_review?.outcome === "accepted")
        .length,
    };
  }, [rows]);

  // "状态" column = issue workflow status (reused from the issues namespace);
  // unknown server values render raw so enum drift downgrades, not crashes.
  const issueStatusLabel = (s: string) =>
    isKnownIssueStatus(s) ? tIssues(($) => $.status[s]) : s;

  return (
    <div className="flex h-full flex-col">
      <PageHeader className="h-auto min-h-12 flex-wrap justify-between gap-y-1.5 px-5 py-1.5 sm:py-0">
        <div className="flex min-w-0 items-center gap-2">
          <Radar className="h-4 w-4 shrink-0 text-muted-foreground" />
          <h1 className="truncate text-sm font-medium">{t(($) => $.operations.title)}</h1>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <SearchBox value={searchInput} onChange={setSearchInput} />
          <AgentFilter
            agents={agents}
            value={agentFilter}
            onChange={setAgentFilter}
          />
          <StatusFilter value={statusFilter} onChange={setStatusFilter} />
          <ValueFilter
            ariaLabel={t(($) => $.operations.filter.workstream)}
            value={workstreamFilter}
            allValue={ALL_WORKSTREAMS}
            allLabel={t(($) => $.operations.filter.workstream_all)}
            options={workstreamOptions}
            onChange={setWorkstreamFilter}
          />
          <ValueFilter
            ariaLabel={t(($) => $.operations.filter.attribution)}
            value={attributionFilter}
            allValue={ALL_ATTRIBUTIONS}
            allLabel={t(($) => $.operations.filter.attribution_all)}
            options={attributionOptions}
            onChange={setAttributionFilter}
          />
          <ValueFilter
            ariaLabel={t(($) => $.operations.filter.quality)}
            value={qualityFilter}
            allValue={ALL_QUALITIES}
            allLabel={t(($) => $.operations.filter.quality_all)}
            options={qualityOptions}
            onChange={setQualityFilter}
          />
          <Button
            type="button"
            variant={mismatchOnly ? "default" : "outline"}
            size="sm"
            onClick={() => setMismatchOnly((v) => !v)}
            className="h-8"
          >
            {t(($) => $.operations.filter.mismatch_only)}
          </Button>
          <Segmented
            value={days}
            onChange={setDays}
            options={RANGES.map((r) => ({ label: r.label, value: r.days }))}
          />
        </div>
      </PageHeader>

      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-[1600px] space-y-4 p-6">
          <div className="flex items-center justify-between gap-3">
            <div className="min-w-0">
              <p className="text-xs font-medium text-foreground">
                {t(($) => $.operations.assessment_title)}
              </p>
              <p className="mt-0.5 text-xs text-muted-foreground">
                {t(($) => $.operations.subtitle)}
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-3">
              {!fixesQuery.isLoading && rows.length > 0 ? (
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    downloadTextFile(
                      exportFilename(),
                      buildOperationsP4AssessmentCsv(rows),
                    )
                  }
                  className="h-8"
                >
                  <Download className="h-3.5 w-3.5" />
                  {t(($) => $.operations.export_csv)}
                </Button>
              ) : null}
              {widthsModified ? (
                <button
                  type="button"
                  onClick={resetColumnWidths}
                  className="text-xs text-muted-foreground transition-colors hover:text-foreground"
                >
                  {t(($) => $.operations.reset_columns)}
                </button>
              ) : null}
              {!fixesQuery.isLoading && rows.length > 0 ? (
                <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                  <span>
                    {t(($) => $.operations.caption, { count: rows.length })}
                  </span>
                  <span className="text-border">/</span>
                  <span>
                    {t(($) => $.operations.assessment_caption, assessmentSummary)}
                  </span>
                </div>
              ) : null}
            </div>
          </div>

          {!fixesQuery.isLoading && rows.length > 0 ? (
            <OperationsSummary summary={assessmentSummary} />
          ) : null}

          {!fixesQuery.isLoading && rows.length > 0 ? (
            <div className="flex items-center justify-between gap-3">
              <Segmented
                value={activeTab}
                onChange={setActiveTab}
                options={[
                  {
                    label: t(($) => $.operations.tabs.detail),
                    value: DETAIL_TAB,
                  },
                  {
                    label: t(($) => $.operations.tabs.analysis),
                    value: ANALYSIS_TAB,
                  },
                ]}
              />
              <span className="text-xs text-muted-foreground">
                {activeTab === DETAIL_TAB
                  ? t(($) => $.operations.tabs.detail_hint)
                  : t(($) => $.operations.tabs.analysis_hint)}
              </span>
            </div>
          ) : null}

          {fixesQuery.isLoading ? (
            <OperationsSkeleton />
          ) : rows.length === 0 ? (
            <OperationsEmpty search={search} />
          ) : activeTab === ANALYSIS_TAB ? (
            <OperationsAnalysis rows={rows} />
          ) : (
            // overflow-x-auto: when a dragged column outgrows the viewport the
            // table scrolls horizontally instead of crushing its fixed columns.
            <div className="overflow-x-auto">
              <div
                ref={cardRef}
                className="rounded-lg border bg-card"
                style={cardStyle(columnWidths)}
              >
                {/* Header: issue, external status, agent, evidence, predictions, review, eval, date. */}
                <div
                  className="grid items-center gap-3 border-b px-4 py-2 text-xs font-medium text-muted-foreground"
                  style={GRID_STYLE}
                >
                  <HeaderCell
                    columnKey="issue"
                    cardRef={cardRef}
                    label={t(($) => $.operations.table.issue)}
                  />
                  <span className="truncate">
                    {t(($) => $.operations.table.external_status)}
                  </span>
                  <HeaderCell
                    columnKey="agent"
                    cardRef={cardRef}
                    label={t(($) => $.operations.table.agent)}
                  />
                  <HeaderCell
                    columnKey="status"
                    cardRef={cardRef}
                    label={t(($) => $.operations.table.p4_evidence)}
                  />
                  <HeaderCell
                    columnKey="attribution"
                    cardRef={cardRef}
                    label={t(($) => $.operations.table.ai_attribution)}
                  />
                  <HeaderCell
                    columnKey="quality"
                    cardRef={cardRef}
                    label={t(($) => $.operations.table.ai_quality)}
                  />
                  <HeaderCell
                    columnKey="review"
                    cardRef={cardRef}
                    label={t(($) => $.operations.table.human_review)}
                  />
                  <HeaderCell
                    columnKey="eval"
                    cardRef={cardRef}
                    label={t(($) => $.operations.table.eval)}
                  />
                  <HeaderCell
                    columnKey="time"
                    cardRef={cardRef}
                    label={t(($) => $.operations.table.time)}
                  />
                </div>
                <div className="divide-y">
                  {rows.map((f) => {
                    const agent = agents.find((a) => a.id === f.agent_id);
                    const comment = (f.last_comment ?? "").trim();
                    // Day axis: latest run's completion day, falling back to
                    // start/created when it has no completed_at (running/queued).
                    const day = formatDay(
                      f.completed_at ?? f.started_at ?? f.created_at,
                      viewTZ,
                    );
                    return (
                      <div
                        key={f.issue_id || f.task_id}
                        className="grid items-center gap-3 px-4 py-3"
                        style={GRID_STYLE}
                      >
                        <IssueCell
                          fix={f}
                          slug={slug}
                          issueStatusLabel={issueStatusLabel(f.issue_status)}
                        />
                        <ExternalStatusCell
                          fix={f}
                          statusNames={feishuStatusNames}
                        />
                        <div className="grid min-w-0 gap-1 overflow-hidden">
                          <div className="flex min-w-0 items-center gap-2 overflow-hidden">
                            <ActorAvatar
                              actorType="agent"
                              actorId={f.agent_id}
                              size={22}
                              enableHoverCard
                            />
                            <span className="min-w-0 truncate text-sm">
                              {agent?.name ?? f.agent_name}
                            </span>
                          </div>
                          <div className="flex min-w-0 flex-wrap items-center gap-1.5 overflow-hidden">
                            <AssessmentStatusBadge
                              value={f.p4_assessment?.assessment_status}
                            />
                            <AssessmentTriggerButton
                              fix={f}
                              pending={
                                triggerAssessment.isPending &&
                                triggeringBindingId === f.external?.binding_id
                              }
                              onTrigger={(bindingId, force) => {
                                setTriggeringBindingId(bindingId);
                                triggerAssessment.mutate(
                                  { binding_id: bindingId, force },
                                  {
                                    onSuccess: () => {
                                      toast.success(
                                        force
                                          ? t(
                                              ($) =>
                                                $.operations.assessment_action
                                                  .rerun_started,
                                            )
                                          : t(
                                              ($) =>
                                                $.operations.assessment_action
                                                  .run_started,
                                            ),
                                      );
                                    },
                                    onError: (err) => {
                                      toast.error(
                                        err instanceof Error && err.message
                                          ? err.message
                                          : t(
                                              ($) =>
                                                $.operations.assessment_action
                                                  .failed,
                                            ),
                                      );
                                    },
                                    onSettled: () => {
                                      setTriggeringBindingId(null);
                                    },
                                  },
                                );
                              }}
                            />
                          </div>
                        </div>
                        <P4EvidenceCell fix={f} />
                        <PredictionCell
                          value={f.p4_assessment?.delivery_attribution_prediction}
                          kind="attribution"
                        />
                        <PredictionCell
                          value={f.p4_assessment?.quality_prediction}
                          kind="quality"
                          detail={confidenceLabel(f.p4_assessment?.confidence)}
                        />
                        <HumanReviewCell fix={f} onEdit={() => setReviewFix(f)} />
                        <EvalCell fix={f} />
                        <span className="min-w-0 overflow-hidden truncate whitespace-nowrap text-xs text-muted-foreground tabular-nums">
                          {day}
                        </span>
                        {comment ? (
                          <div className="col-span-9 -mt-1 truncate text-xs text-muted-foreground">
                            <span className="mr-1 font-medium text-foreground/80">
                              {t(($) => $.operations.table.reason)}:
                            </span>
                            <ReasonText text={comment} keyword={search} />
                          </div>
                        ) : null}
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>
          )}
        </div>
      </div>
      <AgentFixReviewDialog
        open={!!reviewFix}
        description={
          reviewFix
            ? `${reviewFix.issue_identifier} · ${reviewFix.issue_title}`
            : t(($) => $.operations.review_modal.empty_issue)
        }
        initialReview={reviewFix?.human_review}
        saving={updateReview.isPending}
        canSave={!!reviewFix}
        evidenceSlot={
          reviewFix ? <AgentFixReviewEvidence fix={reviewFix} /> : null
        }
        onOpenChange={(open) => {
          if (!open && !updateReview.isPending) setReviewFix(null);
        }}
        onSave={(data) => {
          if (!reviewFix) return;
          updateReview.mutate(
            {
              issueId: reviewFix.issue_id,
              bindingId: reviewFix.external?.binding_id,
              data,
            },
            {
              onSuccess: () => {
                setReviewFix(null);
              },
              onError: (err) => {
                toast.error(
                  err instanceof Error && err.message
                    ? err.message
                    : t(($) => $.operations.review_modal.save_failed),
                );
              },
            },
          );
        }}
      />
    </div>
  );
}

// A header label for a resizable column, with a drag handle pinned to its right
// edge. The label truncates; the cell is `relative` so the handle can overhang
// into the gap and stay grabbable.
function HeaderCell({
  columnKey,
  cardRef,
  label,
}: {
  columnKey: OperationsColumnKey;
  cardRef: RefObject<HTMLDivElement | null>;
  label: string;
}) {
  const { t } = useT("usage");
  return (
    <span className="relative flex min-w-0 items-center">
      <span className="truncate">{label}</span>
      <ColumnResizeHandle
        columnKey={columnKey}
        cardRef={cardRef}
        label={t(($) => $.operations.resize_column, { column: label })}
      />
    </span>
  );
}

function OperationsSummary({
  summary,
}: {
  summary: {
    total: number;
    externalDone: number;
    p4Rows: number;
    aiDelivered: number;
    reviewed: number;
    accepted: number;
    mismatches: number;
  };
}) {
  const { t } = useT("usage");
  const stats = [
    {
      label: t(($) => $.operations.summary.external_done),
      value: summary.externalDone,
      hint: t(($) => $.operations.summary.external_done_hint),
    },
    {
      label: t(($) => $.operations.summary.p4_coverage),
      value: `${summary.p4Rows}/${summary.total}`,
      hint: t(($) => $.operations.summary.p4_coverage_hint),
    },
    {
      label: t(($) => $.operations.summary.ai_delivery),
      value: summary.aiDelivered,
      hint: t(($) => $.operations.summary.ai_delivery_hint),
    },
    {
      label: t(($) => $.operations.summary.human_reviewed),
      value: `${summary.reviewed}/${summary.total}`,
      hint: t(($) => $.operations.summary.human_reviewed_hint),
    },
    {
      label: t(($) => $.operations.summary.accepted),
      value: summary.accepted,
      hint: t(($) => $.operations.summary.accepted_hint),
    },
    {
      label: t(($) => $.operations.summary.drift),
      value: summary.mismatches,
      hint: t(($) => $.operations.summary.drift_hint),
    },
  ];
  return (
    <section className="grid overflow-hidden rounded-lg border bg-card sm:grid-cols-[minmax(180px,1fr)_minmax(0,4fr)]">
      <div className="border-b bg-muted/30 p-4 sm:border-b-0 sm:border-r">
        <p className="text-xs font-medium text-muted-foreground">
          {t(($) => $.operations.summary.hero_label)}
        </p>
        <p className="mt-2 text-3xl font-semibold tabular-nums">
          {summary.total}
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          {t(($) => $.operations.summary.hero_hint)}
        </p>
      </div>
      <div className="grid sm:grid-cols-3 xl:grid-cols-6">
        {stats.map((s) => (
          <div key={s.label} className="border-b p-4 last:border-b-0 sm:border-r sm:last:border-r-0 xl:border-b-0">
            <p className="truncate text-xs font-medium text-muted-foreground">
              {s.label}
            </p>
            <p className="mt-2 text-xl font-semibold tabular-nums">{s.value}</p>
            <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">
              {s.hint}
            </p>
          </div>
        ))}
      </div>
    </section>
  );
}

function OperationsAnalysis({ rows }: { rows: AgentFixRecord[] }) {
  const { t } = useT("usage");
  const tx = t as unknown as UsageT;
  const attribution = countBy(rows, (f) =>
    f.p4_assessment?.delivery_attribution_prediction?.trim() || "unknown",
  );
  const review = countBy(rows, (f) =>
    f.human_review?.outcome?.trim() || "unreviewed",
  );
  const evals = countBy(rows, (f) =>
    (f.ai_judgement_eval || f.display_result_status || "pending").trim(),
  );
  const workstreams = groupWorkstreams(rows);
  const humanReasons = topReasons(
    rows,
    (f) => f.human_review?.reasons,
    (key) => agentFixEnumLabel(tx, "review_reason", key),
  );
  const predictionReasons = topReasons(
    rows,
    (f) => f.p4_assessment?.prediction_reasons,
    (key) => agentFixEnumLabel(tx, "review_reason", key),
  );
  return (
    <div className="grid gap-4 xl:grid-cols-3">
      <AnalysisCard
        title={t(($) => $.operations.analysis.attribution_title)}
        rows={Array.from(attribution.entries()).map(([key, count]) => ({
          label: agentFixEnumLabel(tx, "attribution", key),
          count,
          tone: agentFixEnumTone("attribution", key),
        }))}
      />
      <AnalysisCard
        title={t(($) => $.operations.analysis.review_title)}
        rows={Array.from(review.entries()).map(([key, count]) => ({
          label: agentFixEnumLabel(tx, "review", key),
          count,
          tone: agentFixEnumTone("review", key),
        }))}
      />
      <AnalysisCard
        title={t(($) => $.operations.analysis.eval_title)}
        rows={Array.from(evals.entries()).map(([key, count]) => ({
          label: agentFixEnumLabel(tx, "eval", key),
          count,
          tone: agentFixEnumTone("eval", key),
        }))}
      />
      <WorkstreamAnalysisCard
        title={t(($) => $.operations.analysis.workstream_title)}
        rows={workstreams}
      />
      <AnalysisCard
        title={t(($) => $.operations.analysis.human_reason_title)}
        rows={humanReasons}
        emptyLabel={t(($) => $.operations.analysis.no_reasons)}
      />
      <AnalysisCard
        title={t(($) => $.operations.analysis.prediction_reason_title)}
        rows={predictionReasons}
        emptyLabel={t(($) => $.operations.analysis.no_reasons)}
      />
    </div>
  );
}

function AnalysisCard({
  title,
  rows,
  emptyLabel,
}: {
  title: string;
  rows: { label: string; count: number; tone: Tone }[];
  emptyLabel?: string;
}) {
  const max = Math.max(1, ...rows.map((r) => r.count));
  return (
    <section className="rounded-lg border bg-card">
      <div className="border-b px-4 py-3">
        <h2 className="text-sm font-medium">{title}</h2>
      </div>
      <div className="grid gap-3 p-4">
        {rows.length === 0 ? (
          <div className="text-sm text-muted-foreground">{emptyLabel ?? "—"}</div>
        ) : rows.map((r) => (
          <div key={r.label} className="grid gap-1.5">
            <div className="flex items-center justify-between gap-3">
              <ToneBadge tone={r.tone}>{r.label}</ToneBadge>
              <span className="text-xs text-muted-foreground tabular-nums">
                {r.count}
              </span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary"
                style={{ width: `${Math.max(8, (r.count / max) * 100)}%` }}
              />
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}

function WorkstreamAnalysisCard({
  title,
  rows,
}: {
  title: string;
  rows: {
    workstream: string;
    total: number;
    reviewed: number;
    accepted: number;
    mismatches: number;
  }[];
}) {
  const { t } = useT("usage");
  const max = Math.max(1, ...rows.map((r) => r.total));
  return (
    <section className="rounded-lg border bg-card xl:col-span-2">
      <div className="border-b px-4 py-3">
        <h2 className="text-sm font-medium">{title}</h2>
      </div>
      <div className="grid gap-3 p-4">
        {rows.map((r) => (
          <div key={r.workstream} className="grid gap-1.5">
            <div className="flex min-w-0 items-center justify-between gap-3">
              <span className="truncate font-mono text-xs text-foreground">
                {r.workstream}
              </span>
              <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                {t(($) => $.operations.analysis.workstream_counts, r)}
              </span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary"
                style={{ width: `${Math.max(8, (r.total / max) * 100)}%` }}
              />
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}

function countBy<T>(items: T[], keyFn: (item: T) => string): Map<string, number> {
  const out = new Map<string, number>();
  for (const item of items) {
    const key = keyFn(item);
    out.set(key, (out.get(key) ?? 0) + 1);
  }
  return out;
}

function groupWorkstreams(rows: AgentFixRecord[]) {
  const groups = new Map<
    string,
    {
      workstream: string;
      total: number;
      reviewed: number;
      accepted: number;
      mismatches: number;
    }
  >();
  for (const row of rows) {
    const workstream = derivedEvidence(row).workstream || "unknown";
    const group =
      groups.get(workstream) ??
      { workstream, total: 0, reviewed: 0, accepted: 0, mismatches: 0 };
    group.total += 1;
    const outcome = row.human_review?.outcome ?? "";
    if (outcome && outcome !== "unreviewed") group.reviewed += 1;
    if (outcome === "accepted") group.accepted += 1;
    if (isMismatchEval(row)) group.mismatches += 1;
    groups.set(workstream, group);
  }
  return Array.from(groups.values()).sort((a, b) => {
    if (b.mismatches !== a.mismatches) return b.mismatches - a.mismatches;
    return b.total - a.total;
  });
}

function topReasons(
  rows: AgentFixRecord[],
  getReasons: (row: AgentFixRecord) => string[] | undefined,
  labelFor: (key: string) => string,
) {
  return Array.from(
    countBy(rows.flatMap((row) => getReasons(row) ?? []), (reason) =>
      compactKey(reason, "unknown"),
    ),
  )
    .sort((a, b) => b[1] - a[1])
    .slice(0, 6)
    .map(([key, count]) => ({
      label: labelFor(key),
      count,
      tone: "default" as Tone,
    }));
}

// Drag handle for one resizable column. To keep ~185 rows from re-rendering on
// every pointer move, the drag writes the column's CSS var (and the scroll
// floor) straight onto the card element and only commits the final width to the
// store on release. Double-click restores this column's default width.
function ColumnResizeHandle({
  columnKey,
  cardRef,
  label,
}: {
  columnKey: OperationsColumnKey;
  cardRef: RefObject<HTMLDivElement | null>;
  label: string;
}) {
  const drag = useRef<{ startX: number; baseW: number; lastW: number } | null>(
    null,
  );

  const applyWidth = (width: number) => {
    const card = cardRef.current;
    if (!card) return;
    card.style.setProperty(COLUMN_VAR[columnKey], `${width}px`);
    const widths = useOperationsViewStore.getState().columnWidths;
    card.style.minWidth = `${operationsMinWidth({
      ...widths,
      [columnKey]: width,
    })}px`;
  };

  const onPointerDown = (e: React.PointerEvent<HTMLSpanElement>) => {
    e.preventDefault();
    e.stopPropagation();
    const baseW = useOperationsViewStore.getState().columnWidths[columnKey];
    drag.current = { startX: e.clientX, baseW, lastW: baseW };
    e.currentTarget.setPointerCapture(e.pointerId);
  };

  const onPointerMove = (e: React.PointerEvent<HTMLSpanElement>) => {
    const d = drag.current;
    if (!d) return;
    const next = clampOperationsColumnWidth(d.baseW + (e.clientX - d.startX));
    d.lastW = next;
    applyWidth(next);
  };

  const endDrag = (e: React.PointerEvent<HTMLSpanElement>) => {
    const d = drag.current;
    if (!d) return;
    drag.current = null;
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      // Pointer capture may already be gone (e.g. pointercancel) — ignore.
    }
    useOperationsViewStore.getState().setColumnWidth(columnKey, d.lastW);
  };

  const onDoubleClick = () => {
    const def = OPERATIONS_DEFAULT_WIDTHS[columnKey];
    applyWidth(def);
    useOperationsViewStore.getState().setColumnWidth(columnKey, def);
  };

  return (
    <span
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onDoubleClick={onDoubleClick}
      className="group/handle absolute top-1/2 -right-1.5 z-10 flex h-5 w-3 -translate-y-1/2 cursor-col-resize touch-none select-none items-center justify-center"
      style={NO_DRAG_STYLE}
    >
      <span className="h-3.5 w-px bg-border transition-colors group-hover/handle:bg-primary group-active/handle:bg-primary" />
    </span>
  );
}

function IssueCell({
  fix,
  slug,
  issueStatusLabel,
}: {
  fix: AgentFixRecord;
  slug: string | null;
  issueStatusLabel: string;
}) {
  const inner = (
    <div className="grid min-w-0 gap-1 overflow-hidden">
      <div className="flex min-w-0 items-center gap-2">
        <span className="shrink-0 font-mono text-xs text-muted-foreground tabular-nums">
          {fix.issue_identifier || "—"}
        </span>
        <span className="truncate text-sm font-medium">
          {fix.issue_title || "—"}
        </span>
      </div>
      <div className="flex min-w-0 items-center gap-1.5">
        {isKnownIssueStatus(fix.issue_status) && (
          <StatusIcon status={fix.issue_status} className="h-3.5 w-3.5" />
        )}
        <span className="truncate text-xs text-muted-foreground">
          {issueStatusLabel || "—"}
        </span>
        {fix.external?.work_item_id ? (
          <>
            <span className="text-muted-foreground/50">·</span>
            <span className="truncate font-mono text-xs text-muted-foreground">
              {fix.external.work_item_id}
            </span>
          </>
        ) : null}
        {fix.external?.project ? (
          <>
            <span className="text-muted-foreground/50">·</span>
            <span className="truncate text-xs text-muted-foreground">
              {fix.external.project}
            </span>
          </>
        ) : null}
        {fix.external?.version ? (
          <>
            <span className="text-muted-foreground/50">·</span>
            <span className="truncate text-xs text-muted-foreground">
              {fix.external.version}
            </span>
          </>
        ) : null}
      </div>
    </div>
  );
  // Link to the issue when we know the workspace slug; fall back to plain text
  // so a missing slug (rendered before workspace resolves) never breaks the row.
  if (slug && fix.issue_id && fix.issue_identifier) {
    return (
      <AppLink
        href={paths.workspace(slug).issueDetail(fix.issue_identifier)}
        className="block min-w-0 overflow-hidden hover:underline"
      >
        {inner}
      </AppLink>
    );
  }
  return <div className="min-w-0 overflow-hidden">{inner}</div>;
}

function ExternalStatusCell({
  fix,
  statusNames,
}: {
  fix: AgentFixRecord;
  statusNames: Map<string, string>;
}) {
  const { t } = useT("usage");
  const done = fix.external?.done;
  const rawStatus = fix.external?.status ?? "";
  const status =
    fix.external?.status_name ||
    statusNames.get(rawStatus) ||
    rawStatus ||
    t(($) => $.operations.no_reason);
  return (
    <div className="grid min-w-0 gap-1">
      <ToneBadge
        tone={done === true ? "success" : done === false ? "warning" : "muted"}
      >
        {status}
      </ToneBadge>
      {typeof done === "boolean" ? (
        <span className="truncate text-xs text-muted-foreground">
          {done
            ? t(($) => $.operations.external.in_stats)
            : t(($) => $.operations.external.out_of_stats)}
        </span>
      ) : null}
    </div>
  );
}

function P4EvidenceCell({ fix }: { fix: AgentFixRecord }) {
  const { t } = useT("usage");
  const p4 = fix.p4_assessment;
  const evidence = derivedEvidence(fix);
  const detailRows = [
    evidence.swarmChanges
      ? [t(($) => $.operations.p4.changes), evidence.swarmChanges]
      : null,
    evidence.swarmCommits
      ? [t(($) => $.operations.p4.commits), evidence.swarmCommits]
      : null,
    evidence.swarmBranch
      ? [t(($) => $.operations.p4.branch), evidence.swarmBranch]
      : null,
    evidence.eventType
      ? [t(($) => $.operations.p4.event), evidence.eventType]
      : null,
    evidence.sentAt
      ? [t(($) => $.operations.p4.sent_at), evidence.sentAt]
      : null,
    p4?.warnings?.length
      ? [t(($) => $.operations.p4.warnings), p4.warnings.join(", ")]
      : null,
  ].filter(Boolean) as Array<[string, string]>;
  if (!hasP4Signal(fix)) {
    return (
      <span className="text-xs text-muted-foreground">
        {t(($) => $.operations.p4.no_evidence)}
      </span>
    );
  }
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5 overflow-hidden">
      <div className="flex min-w-0 flex-wrap gap-1.5 overflow-hidden">
        {evidence.workstream ? (
          <EvidenceBadge>
            {t(($) => $.operations.p4.workstream)} {evidence.workstream}
          </EvidenceBadge>
        ) : null}
        {evidence.swarm ? (
          <EvidenceBadge>
            {t(($) => $.operations.p4.swarm_value, { value: evidence.swarm })}
          </EvidenceBadge>
        ) : null}
        {evidence.shelve ? (
          <EvidenceBadge>
            {t(($) => $.operations.p4.shelve)} {evidence.shelve}
          </EvidenceBadge>
        ) : null}
        {evidence.finalCl ? (
          <EvidenceBadge>
            {t(($) => $.operations.p4.final_cl)} {evidence.finalCl}
          </EvidenceBadge>
        ) : null}
        {p4?.warnings?.length ? (
          <ToneBadge tone="warning">
            {t(($) => $.operations.p4.warning_count, {
              count: p4.warnings.length,
            })}
          </ToneBadge>
        ) : null}
      </div>
      {detailRows.length > 0 ? (
        <Popover>
          <PopoverTrigger
            render={
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-6 min-w-0 max-w-full gap-1 px-2 text-xs"
              >
                <List className="h-3.5 w-3.5 shrink-0" />
                <span className="truncate">
                  {t(($) => $.operations.p4.details)}
                </span>
              </Button>
            }
          />
          <PopoverContent align="end" className="w-80 gap-2">
            <div className="text-xs font-medium">
              {t(($) => $.operations.p4.details)}
            </div>
            <div className="grid gap-2">
              {detailRows.map(([label, value]) => (
                <div key={label} className="grid gap-0.5">
                  <div className="text-[11px] uppercase text-muted-foreground">
                    {label}
                  </div>
                  <div className="break-words text-xs">{value}</div>
                </div>
              ))}
            </div>
          </PopoverContent>
        </Popover>
      ) : null}
    </div>
  );
}

function AssessmentStatusBadge({ value }: { value?: string }) {
  const { t } = useT("usage");
  const tx = t as unknown as UsageT;
  return (
    <ToneBadge tone={assessmentTone(value)}>
      {agentFixEnumLabel(tx, "assessment", value || "missing")}
    </ToneBadge>
  );
}

function AssessmentTriggerButton({
  fix,
  pending,
  onTrigger,
}: {
  fix: AgentFixRecord;
  pending: boolean;
  onTrigger: (bindingId: string, force: boolean) => void;
}) {
  const { t } = useT("usage");
  const bindingId = fix.external?.binding_id ?? "";
  if (fix.external?.mapped_status !== "done" || !bindingId) {
    return null;
  }

  const status = fix.p4_assessment?.assessment_status ?? "";
  const assessmentActive = status === "pending" || status === "running";
  const completed = status === "completed";
  const force = completed;
  const disabled = pending || assessmentActive;
  const label = pending
    ? t(($) => $.operations.assessment_action.starting)
    : assessmentActive
      ? t(($) => $.operations.assessment_action.in_progress)
      : completed
        ? t(($) => $.operations.assessment_action.rerun)
        : t(($) => $.operations.assessment_action.run);
  const Icon = completed ? RefreshCw : Play;

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      disabled={disabled}
      onClick={() => onTrigger(bindingId, force)}
      className="h-7 min-w-0 max-w-full px-2 text-xs"
    >
      <Icon className="h-3.5 w-3.5 shrink-0" />
      <span className="min-w-0 max-w-28 truncate">{label}</span>
    </Button>
  );
}

function EvidenceBadge({ children }: { children: ReactNode }) {
  return (
    <Badge
      variant="outline"
      className="max-w-full justify-start truncate border-border bg-muted font-mono text-muted-foreground"
    >
      <span className="truncate">{children}</span>
    </Badge>
  );
}

function PredictionCell({
  value,
  kind,
  detail,
}: {
  value?: string;
  kind: "attribution" | "quality";
  detail?: string;
}) {
  const { t } = useT("usage");
  const tx = t as unknown as UsageT;
  const label = agentFixEnumLabel(tx, kind, value);
  return (
    <div className="grid min-w-0 gap-1 justify-items-start overflow-hidden">
      <ToneBadge tone={agentFixEnumTone(kind, value)}>{label}</ToneBadge>
      {detail ? (
        <span className="truncate text-xs text-muted-foreground">
          {t(($) => $.operations.p4.confidence, { value: detail })}
        </span>
      ) : null}
    </div>
  );
}

function HumanReviewCell({
  fix,
  onEdit,
}: {
  fix: AgentFixRecord;
  onEdit: () => void;
}) {
  const { t } = useT("usage");
  const tx = t as unknown as UsageT;
  const outcome = fix.human_review?.outcome;
  const reasonText = agentFixReviewReasonLabels(
    tx,
    fix.human_review?.reasons,
  ).join(", ");
  const note = fix.human_review?.note ?? "";
  const reviewedAt = fix.human_review?.reviewed_at ?? "";
  const detailRows = [
    reasonText ? [t(($) => $.operations.review_modal.reasons), reasonText] : null,
    note ? [t(($) => $.operations.review_modal.note), note] : null,
    reviewedAt ? [t(($) => $.operations.table.time), reviewedAt] : null,
  ].filter(Boolean) as Array<[string, string]>;
  return (
    <div className="grid min-w-0 gap-1 justify-items-start overflow-hidden">
      <button
        type="button"
        onClick={onEdit}
        className="inline-flex min-w-0 max-w-full rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <ToneBadge tone={agentFixEnumTone("review", outcome)}>
          {agentFixEnumLabel(tx, "review", outcome)}
        </ToneBadge>
      </button>
      {detailRows.length > 0 ? (
        <Popover>
          <PopoverTrigger
            render={
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="h-6 min-w-0 max-w-full gap-1 px-2 text-xs"
              >
                <List className="h-3.5 w-3.5 shrink-0" />
                <span className="truncate">
                  {t(($) => $.operations.p4.details)}
                </span>
              </Button>
            }
          />
          <PopoverContent align="end" className="w-80 gap-2">
            <div className="text-xs font-medium">
              {t(($) => $.operations.review_modal.title)}
            </div>
            <div className="grid gap-2">
              {detailRows.map(([label, value]) => (
                <div key={label} className="grid gap-0.5">
                  <div className="text-[11px] uppercase text-muted-foreground">
                    {label}
                  </div>
                  <div className="break-words text-xs">{value}</div>
                </div>
              ))}
            </div>
          </PopoverContent>
        </Popover>
      ) : null}
    </div>
  );
}

function AgentFixReviewEvidence({ fix }: { fix: AgentFixRecord }) {
  const { t } = useT("usage");
  const tx = t as unknown as UsageT;
  const evidence = derivedEvidence(fix);

  return (
    <div className="grid gap-3 md:grid-cols-3">
      <EvidencePanel
        label={t(($) => $.operations.review_modal.evidence_p4)}
        value={[
          fix.p4_assessment?.workstream || t(($) => $.operations.no_reason),
          evidence.swarm
            ? t(($) => $.operations.p4.swarm_value, { value: evidence.swarm })
            : t(($) => $.operations.p4.no_swarm),
          evidence.swarmChanges
            ? `${t(($) => $.operations.p4.changes)} ${evidence.swarmChanges}`
            : "",
          evidence.swarmCommits
            ? `${t(($) => $.operations.p4.commits)} ${evidence.swarmCommits}`
            : "",
          evidence.swarmBranch
            ? `${t(($) => $.operations.p4.branch)} ${evidence.swarmBranch}`
            : "",
          evidence.eventType
            ? `${t(($) => $.operations.p4.event)} ${evidence.eventType}`
            : "",
          evidence.sentAt
            ? `${t(($) => $.operations.p4.sent_at)} ${evidence.sentAt}`
            : "",
          `${t(($) => $.operations.p4.shelve)} ${
            evidence.shelve || t(($) => $.operations.no_reason)
          } / ${t(($) => $.operations.p4.final_cl)} ${
            evidence.finalCl || t(($) => $.operations.no_reason)
          }`,
        ]}
      />
      <EvidencePanel
        label={t(($) => $.operations.review_modal.evidence_attribution)}
        value={[
          agentFixEnumLabel(
            tx,
            "attribution",
            fix.p4_assessment?.delivery_attribution_prediction,
          ),
        ]}
        badgeTone={agentFixEnumTone(
          "attribution",
          fix.p4_assessment?.delivery_attribution_prediction,
        )}
      />
      <EvidencePanel
        label={t(($) => $.operations.review_modal.evidence_quality)}
        value={[
          agentFixEnumLabel(tx, "quality", fix.p4_assessment?.quality_prediction),
          fix.p4_assessment?.confidence != null
            ? t(($) => $.operations.p4.confidence, {
                value: confidenceLabel(fix.p4_assessment.confidence),
              })
            : t(($) => $.operations.review_modal.insufficient_evidence),
        ]}
        badgeTone={agentFixEnumTone("quality", fix.p4_assessment?.quality_prediction)}
      />
    </div>
  );
}

function EvidencePanel({
  label,
  value,
  badgeTone,
}: {
  label: string;
  value: string[];
  badgeTone?: Tone;
}) {
  const first = value[0] || "—";
  const rest = value.slice(1).filter(Boolean);
  return (
    <div className="rounded-lg border bg-muted/20 p-3">
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      <div className="mt-2 grid gap-1 text-sm">
        {badgeTone ? <ToneBadge tone={badgeTone}>{first}</ToneBadge> : <span>{first}</span>}
        {rest.map((line) => (
          <span key={line} className="text-xs text-muted-foreground">
            {line}
          </span>
        ))}
      </div>
    </div>
  );
}

function EvalCell({ fix }: { fix: AgentFixRecord }) {
  const { t } = useT("usage");
  const tx = t as unknown as UsageT;
  const value = fix.ai_judgement_eval || fix.display_result_status;
  return (
    <div className="min-w-0 overflow-hidden">
      <ToneBadge tone={agentFixEnumTone("eval", value)}>
        {agentFixEnumLabel(tx, "eval", value)}
      </ToneBadge>
    </div>
  );
}

function assessmentTone(value?: string): Tone {
  const key = value?.trim();
  if (!key || key === "missing") return "warning";
  if (key === "completed") return "success";
  if (key === "failed") return "danger";
  if (key === "running") return "info";
  if (key === "stale") return "warning";
  return "default";
}

// Renders the "原因/描述" comment snippet, highlighting every occurrence of the
// active search term. The backend centers the snippet on the match, so the
// keyword is always present when `keyword` is set.
function ReasonText({ text, keyword }: { text: string; keyword: string }) {
  const parts = useMemo(() => splitHighlight(text, keyword), [text, keyword]);
  return (
    <>
      {parts.map((p, i) =>
        p.match ? (
          <mark
            key={i}
            className="rounded-sm bg-primary/15 px-0.5 font-medium text-foreground"
          >
            {p.text}
          </mark>
        ) : (
          <span key={i}>{p.text}</span>
        ),
      )}
    </>
  );
}

// Free-text filter on the agent comment ("原因/描述"). Controlled; the parent
// debounces before it reaches the query. A clear button resets it in one click.
function SearchBox({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const { t } = useT("usage");
  return (
    <div className="relative">
      <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
      <Input
        type="search"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={t(($) => $.operations.search_placeholder)}
        aria-label={t(($) => $.operations.search_placeholder)}
        className="h-8 w-[200px] pl-8 pr-7 text-sm [&::-webkit-search-cancel-button]:appearance-none"
      />
      {value ? (
        <button
          type="button"
          onClick={() => onChange("")}
          aria-label={t(($) => $.operations.search_clear)}
          className="absolute right-1.5 top-1/2 -translate-y-1/2 rounded-sm p-0.5 text-muted-foreground hover:text-foreground"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      ) : null}
    </div>
  );
}

function AgentFilter({
  agents,
  value,
  onChange,
}: {
  agents: { id: string; name: string }[];
  value: string;
  onChange: (v: string) => void;
}) {
  const { t } = useT("usage");
  const allLabel = t(($) => $.operations.filter.all_agents);
  const selected = agents.find((a) => a.id === value);
  return (
    <Select value={value} onValueChange={(v) => onChange(v ?? ALL_AGENTS)}>
      <SelectTrigger size="sm" className="min-w-[160px]">
        <SelectValue>
          {() => (
            <span className="truncate">
              {value === ALL_AGENTS ? allLabel : selected?.name ?? allLabel}
            </span>
          )}
        </SelectValue>
      </SelectTrigger>
      <SelectContent align="start" alignItemWithTrigger={false} className="max-h-72">
        <SelectItem value={ALL_AGENTS}>
          <span className="truncate">{allLabel}</span>
        </SelectItem>
        {agents.map((a) => (
          <SelectItem key={a.id} value={a.id}>
            <span className="truncate">{a.name}</span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

// Filters rows by the ISSUE workflow status (the "状态" column). Labels reuse
// the issues namespace so they match the rest of the product.
function StatusFilter({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const { t } = useT("usage");
  const { t: tIssues } = useT("issues");
  const allLabel = t(($) => $.operations.filter.status_all);
  const label = (s: string) =>
    isKnownIssueStatus(s) ? tIssues(($) => $.status[s]) : s;
  return (
    <Select value={value} onValueChange={(v) => onChange(v ?? ALL_STATUSES)}>
      <SelectTrigger size="sm" className="min-w-[130px]">
        <SelectValue>
          {() => (
            <span className="truncate">
              {value === ALL_STATUSES ? allLabel : label(value)}
            </span>
          )}
        </SelectValue>
      </SelectTrigger>
      <SelectContent align="start" alignItemWithTrigger={false}>
        <SelectItem value={ALL_STATUSES}>{allLabel}</SelectItem>
        {ISSUE_STATUSES.map((s) => (
          <SelectItem key={s} value={s}>
            {label(s)}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function ValueFilter({
  ariaLabel,
  value,
  allValue,
  allLabel,
  options,
  onChange,
}: {
  ariaLabel: string;
  value: string;
  allValue: string;
  allLabel: string;
  options: SelectOption[];
  onChange: (v: string) => void;
}) {
  const selected = options.find((option) => option.value === value);
  return (
    <Select value={value} onValueChange={(v) => onChange(v ?? allValue)}>
      <SelectTrigger
        size="sm"
        aria-label={ariaLabel}
        className="min-w-[150px] max-w-[190px]"
      >
        <SelectValue>
          {() => (
            <span className="truncate">
              {value === allValue ? allLabel : selected?.label ?? value}
            </span>
          )}
        </SelectValue>
      </SelectTrigger>
      <SelectContent
        align="start"
        alignItemWithTrigger={false}
        className="max-h-72"
      >
        <SelectItem value={allValue}>
          <span className="truncate">{allLabel}</span>
        </SelectItem>
        {options.map((option) => (
          <SelectItem key={option.value} value={option.value}>
            <span className="truncate">{option.label}</span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function OperationsSkeleton() {
  return (
    <div className="space-y-2">
      <Skeleton className="h-9 rounded-lg" />
      <Skeleton className="h-48 rounded-lg" />
    </div>
  );
}

// Empty state. With an active search the copy explains "no matches" rather than
// the default "no fixes yet" — otherwise a successful-but-empty search reads as
// if the agents never did anything.
function OperationsEmpty({ search }: { search: string }) {
  const { t } = useT("usage");
  const searching = search.trim().length > 0;
  return (
    <div className="flex flex-col items-center rounded-lg border border-dashed py-12 text-center">
      {searching ? (
        <Search className="h-6 w-6 text-muted-foreground/40" />
      ) : (
        <Radar className="h-6 w-6 text-muted-foreground/40" />
      )}
      <p className="mt-3 text-sm font-medium">
        {searching
          ? t(($) => $.operations.empty.search_title)
          : t(($) => $.operations.empty.title)}
      </p>
      <p className="mt-1 max-w-md text-xs text-muted-foreground">
        {searching
          ? t(($) => $.operations.empty.search_body, { term: search.trim() })
          : t(($) => $.operations.empty.body)}
      </p>
    </div>
  );
}
