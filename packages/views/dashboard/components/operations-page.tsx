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
import {
  ChevronLeft,
  ChevronRight,
  ExternalLink,
  List,
  Play,
  Radar,
  RefreshCw,
  Search,
  TriangleAlert,
  X,
} from "lucide-react";
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
import { perforceConnectionOptions } from "@multica/core/perforce/queries";
import { feishuProjectIssueStatusesOptions } from "@multica/core/feishu-project/queries";
import {
  operationsFixesOptions,
  useTriggerAgentFixP4Assessment,
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
  ToneBadge,
  agentFixEnumLabel,
  agentFixEnumTone,
  type Tone,
  type UsageT,
} from "./agent-fix-review";
import { OperationsSummary } from "./operations-summary";
import {
  OperationsSheet,
  type OperationsDrillFilter,
  type OperationsSheetState,
} from "./operations-drawers";
import { Segmented } from "./segmented";
import {
  attributionBucket,
  computeOperationsKpis,
  deriveAttribution,
  derivedEvidence,
  firstSwarmReviewUrl,
  hasP4Signal,
  fixDayIso,
  isAiParticipated,
  isPendingJudgement,
  qualityBucket,
  trimOperationsWindow,
  swarmChangeUrl,
  swarmReviewUrl,
} from "../operations-metrics";

const ALL_AGENTS = "__all__";
const ALL_WORKSTREAMS = "__all__";
const ALL_ATTRIBUTIONS = "__all__";
const ALL_QUALITIES = "__all__";
type SelectOption = { value: string; label: string };

const FILTER_SELECT_TRIGGER_CLASS =
  "h-8 rounded-md border-0 bg-transparent px-2.5 text-xs text-muted-foreground shadow-none hover:bg-background/70 hover:text-foreground focus-visible:ring-2";
const SEARCH_INPUT_CLASS =
  "h-8 w-full rounded-lg border-border/70 bg-muted/25 pl-8 pr-7 text-base shadow-none transition-colors hover:bg-muted/35 focus-visible:border-ring focus-visible:bg-background sm:w-[260px] md:text-sm [&::-webkit-search-cancel-button]:appearance-none";
const SEARCH_CLEAR_CLASS =
  "absolute right-1.5 top-1/2 -translate-y-1/2 rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50";

// Detail-table page size. The full window is fetched once (a few thousand rows
// at most) and paged purely in the UI so the client-side filters keep covering
// the whole window.
const PAGE_SIZE = 50;

// Trailing window. `1d` is the last 24h; the rest mirror the Usage dashboard's
// daily-dimension options. 30d default matches the dashboard.
const RANGES = [
  { label: "1d", days: 1 },
  { label: "7d", days: 7 },
  { label: "30d", days: 30 },
  { label: "90d", days: 90 },
] as const;
type OpsRange = (typeof RANGES)[number]["days"];

// Mirrors the SQL LIMIT in ListWorkspaceAgentFixes. A fetch that fills it may
// have dropped the window's oldest rows, so the page flags possibly-partial
// coverage instead of presenting the stats as complete.
const FETCH_LIMIT = 10000;

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
// Column order: Issue, external state, agent, submitted CL record, delivery
// attribution, AI quality analysis, date. The external status column stays
// fixed; the rest are user-resizable. The legacy `status` width slot backs the
// submitted-CL column so stored preferences remain scoped to this page.
const EXTERNAL_PX = 138;
const COLUMN_GAP_PX = 12; // matches gap-3
const CARD_PADDING_X_PX = 32; // px-4 on the header + each row (16 × 2)

const COLUMN_VAR: Record<OperationsColumnKey, string> = {
  agent: "--ops-col-agent",
  issue: "--ops-col-issue",
  status: "--ops-col-status",
  attribution: "--ops-col-attribution",
  quality: "--ops-col-quality",
  time: "--ops-col-time",
};

const GRID_TEMPLATE = `var(${COLUMN_VAR.issue}) ${EXTERNAL_PX}px var(${COLUMN_VAR.agent}) var(${COLUMN_VAR.status}) var(${COLUMN_VAR.attribution}) var(${COLUMN_VAR.quality}) var(${COLUMN_VAR.time})`;

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
    w.time +
    EXTERNAL_PX +
    COLUMN_GAP_PX * 6 +
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
    [COLUMN_VAR.time]: `${w.time}px`,
    minWidth: `${operationsMinWidth(w)}px`,
  } as CSSProperties;
}

// Debounce delay before a typed search term hits the server. Long enough to
// coalesce a burst of keystrokes, short enough to feel responsive.
const SEARCH_DEBOUNCE_MS = 300;
const FEISHU_TEST_PASSED_STATUS_NAME = "测试通过";

function isOperationsVisibleIssue(
  fix: AgentFixRecord,
  externalStatusNames: ReadonlyMap<string, string>,
): boolean {
  const external = fix.external;
  const hasExternalBinding = (external?.binding_id ?? "").trim().length > 0;
  const rawStatus = (external?.status ?? "").trim();
  const statusName = (
    external?.status_name ||
    externalStatusNames.get(rawStatus) ||
    rawStatus
  ).trim();
  return (
    hasExternalBinding &&
    fix.issue_status === "done" &&
    statusName === FEISHU_TEST_PASSED_STATUS_NAME
  );
}

function confidenceLabel(confidence: number | null | undefined): string {
  if (typeof confidence !== "number" || Number.isNaN(confidence)) return "";
  return `${Math.round(confidence * 100)}%`;
}

function sortedUniqueOptions(
  rows: AgentFixRecord[],
  getValue: (row: AgentFixRecord) => string | undefined,
): string[] {
  return Array.from(
    new Set(rows.map((row) => getValue(row)?.trim()).filter(Boolean) as string[]),
  ).sort((a, b) => a.localeCompare(b));
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
 * Operations page — AI repair effectiveness over Feishu items at 测试通过 whose
 * synced Multica issue is also done. Rows without AI participation stay visible
 * so contribution and unhandled reasons reconcile with Meegle. Delivery
 * and quality distributions live in the relevant KPI drawers above the detail
 * table. Lives at
 * `/{slug}/operations`; backed by
 * GET /api/operations/agent-fixes.
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
  const [workstreamFilter, setWorkstreamFilter] = useState<string>(ALL_WORKSTREAMS);
  const [attributionFilter, setAttributionFilter] =
    useState<string>(ALL_ATTRIBUTIONS);
  const [qualityFilter, setQualityFilter] = useState<string>(ALL_QUALITIES);
  const [pendingOnly, setPendingOnly] = useState(false);
  const [aiParticipatedOnly, setAiParticipatedOnly] = useState(false);
  const [page, setPage] = useState(0);
  // Right-side drawer: rate-card breakdown / branch ticket list / one issue.
  const [sheet, setSheet] = useState<OperationsSheetState>(null);
  // `searchInput` is what the user types; `search` is the debounced term that
  // actually keys the query (so we don't refetch on every keystroke).
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [triggeringBindingId, setTriggeringBindingId] = useState<string | null>(
    null,
  );
  const triggerAssessment = useTriggerAgentFixP4Assessment();

  useEffect(() => {
    const id = setTimeout(
      () => setSearch(searchInput.trim()),
      SEARCH_DEBOUNCE_MS,
    );
    return () => clearTimeout(id);
  }, [searchInput]);

  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  // Resolve the business status before loading the feed. Sending its raw key
  // lets SQL apply 测试通过 + Multica done before the response cap.
  const feishuStatusQuery = useQuery(
    feishuProjectIssueStatusesOptions(wsId, true),
  );
  const testPassedStatus =
    feishuStatusQuery.data?.statuses.find(
      (status) => status.name.trim() === FEISHU_TEST_PASSED_STATUS_NAME,
    )?.key ?? "";
  // Two feeds over the same window: the stats feed (no search term) backs the
  // KPI band and drawers, while the table feed (server-side comment search)
  // backs the detail table. With no search term they are the same cached
  // query. This split is what keeps detail filters from bending the stats.
  const statsQuery = useQuery(
    operationsFixesOptions(wsId, days, "", testPassedStatus),
  );
  const tableQuery = useQuery(
    operationsFixesOptions(wsId, days, search, testPassedStatus),
  );
  const allFixes = statsQuery.data ?? EMPTY;
  // The feed carries Feishu status IDs. Resolve their display names before
  // selecting the reporting pool so generic terminal states mapped to `done`
  // cannot be mistaken for the business-specific 测试通过 state.
  const feishuStatusNames = useMemo(() => {
    const out = new Map<string, string>();
    for (const status of feishuStatusQuery.data?.statuses ?? []) {
      if (status.key && status.name) out.set(status.key, status.name);
    }
    return out;
  }, [feishuStatusQuery.data]);
  const visibleFixes = useMemo(
    () =>
      allFixes.filter((fix) =>
        isOperationsVisibleIssue(fix, feishuStatusNames),
      ),
    [allFixes, feishuStatusNames],
  );
  // Window-trimmed pools, before any filter.
  const windowFixes = useMemo(
    () => trimOperationsWindow(visibleFixes, days, viewTZ),
    [visibleFixes, days, viewTZ],
  );
  const tableWindowFixes = useMemo(
    () =>
      trimOperationsWindow(
        (tableQuery.data ?? EMPTY).filter((fix) =>
          isOperationsVisibleIssue(fix, feishuStatusNames),
        ),
        days,
        viewTZ,
      ),
    [tableQuery.data, days, viewTZ, feishuStatusNames],
  );
  const isLoading =
    statsQuery.isLoading || tableQuery.isLoading || feishuStatusQuery.isLoading;
  const isError =
    (statsQuery.isError && !statsQuery.data) ||
    (tableQuery.isError && !tableQuery.data) ||
    (feishuStatusQuery.isError && !feishuStatusQuery.data) ||
    (!feishuStatusQuery.isLoading &&
      !!feishuStatusQuery.data &&
      !testPassedStatus);
  // The workspace's Helix Swarm URL — one connection per workspace — turns
  // review IDs and CL numbers into links. Absent connection → plain text.
  const { data: perforceData } = useQuery(perforceConnectionOptions(wsId));
  const swarmBase = perforceData?.connection?.swarm_url ?? "";
  const tx = t as unknown as UsageT;

  // Validate the picked agent against the current workspace's list so a stale
  // id (deleted agent, or a leftover after a workspace switch) doesn't silently
  // filter everything to empty while the dropdown still reads a name.
  const effectiveAgent = useMemo(() => {
    if (agentFilter === ALL_AGENTS) return ALL_AGENTS;
    return agents.some((a) => a.id === agentFilter) ? agentFilter : ALL_AGENTS;
  }, [agentFilter, agents]);

  const workstreamOptions = useMemo<SelectOption[]>(() => {
    return sortedUniqueOptions(windowFixes, (f) =>
      derivedEvidence(f).workstream,
    ).map((value) => ({ value, label: value }));
  }, [windowFixes]);

  // The stats pool: page-level dimensions only (window + workstream). The KPI
  // band and drawers read this exact pool.
  const statsRows = useMemo(
    () =>
      workstreamFilter === ALL_WORKSTREAMS
        ? windowFixes
        : windowFixes.filter(
            (f) => derivedEvidence(f).workstream === workstreamFilter,
          ),
    [windowFixes, workstreamFilter],
  );

  const attributionOptions = useMemo<SelectOption[]>(() => {
    return sortedUniqueOptions(statsRows, attributionBucket).map((value) => ({
      value,
      label: agentFixEnumLabel(tx, "attribution", value),
    }));
  }, [statsRows, tx]);

  const qualityOptions = useMemo<SelectOption[]>(() => {
    return sortedUniqueOptions(statsRows, (f) =>
      qualityBucket(f),
    ).map((value) => ({
      value,
      label: agentFixEnumLabel(tx, "quality", value),
    }));
  }, [statsRows, tx]);

  // Detail-only filters (agent / attribution / quality / pending). They — and
  // the comment search — narrow ONLY the detail table, never the stats above,
  // so a narrowed table cannot masquerade as a changed rate.
  const matchesDetailFilters = useMemo(() => {
    return (f: AgentFixRecord): boolean => {
      if (effectiveAgent !== ALL_AGENTS && f.agent_id !== effectiveAgent) {
        return false;
      }
      if (
        attributionFilter !== ALL_ATTRIBUTIONS &&
        attributionBucket(f) !== attributionFilter
      ) {
        return false;
      }
      if (
        qualityFilter !== ALL_QUALITIES &&
        qualityBucket(f) !== qualityFilter
      ) {
        return false;
      }
      if (pendingOnly && !isPendingJudgement(f)) {
        return false;
      }
      if (aiParticipatedOnly && !isAiParticipated(f)) {
        return false;
      }
      return true;
    };
  }, [
    effectiveAgent,
    attributionFilter,
    qualityFilter,
    pendingOnly,
    aiParticipatedOnly,
  ]);

  const tableRows = useMemo(
    () =>
      tableWindowFixes.filter(
        (f) =>
          (workstreamFilter === ALL_WORKSTREAMS ||
            derivedEvidence(f).workstream === workstreamFilter) &&
          matchesDetailFilters(f),
      ),
    [tableWindowFixes, workstreamFilter, matchesDetailFilters],
  );

  const detailFiltersDirty =
    search.trim() !== "" ||
    effectiveAgent !== ALL_AGENTS ||
    attributionFilter !== ALL_ATTRIBUTIONS ||
    qualityFilter !== ALL_QUALITIES ||
    pendingOnly ||
    aiParticipatedOnly;

  const resetDetailFilters = () => {
    setSearchInput("");
    setSearch("");
    setAgentFilter(ALL_AGENTS);
    setAttributionFilter(ALL_ATTRIBUTIONS);
    setQualityFilter(ALL_QUALITIES);
    setPendingOnly(false);
    setAiParticipatedOnly(false);
  };

  const kpis = useMemo(() => computeOperationsKpis(statsRows), [statsRows]);
  // UI pagination over the filtered rows. Any filter / window / search change
  // snaps back to the first page.
  useEffect(() => {
    setPage(0);
  }, [
    days,
    effectiveAgent,
    workstreamFilter,
    attributionFilter,
    qualityFilter,
    pendingOnly,
    aiParticipatedOnly,
    search,
  ]);
  const pageCount = Math.max(1, Math.ceil(tableRows.length / PAGE_SIZE));
  const safePage = Math.min(page, pageCount - 1);
  const pagedRows = useMemo(
    () => tableRows.slice(safePage * PAGE_SIZE, (safePage + 1) * PAGE_SIZE),
    [tableRows, safePage],
  );

  // "状态" column = issue workflow status (reused from the issues namespace);
  // unknown server values render raw so enum drift downgrades, not crashes.
  const issueStatusLabel = (s: string) =>
    isKnownIssueStatus(s) ? tIssues(($) => $.status[s]) : s;

  // External status resolution shared with the drawer — same fallback chain
  // the ExternalStatusCell uses.
  const externalStatusLabel = (f: AgentFixRecord): string =>
    f.external?.status_name ||
    feishuStatusNames.get(f.external?.status ?? "") ||
    f.external?.status ||
    "";

  // A drawer branch's "view all" jump applies the matching detail filter.
  const applyDrill = (filter: OperationsDrillFilter) => {
    if (filter.attribution) setAttributionFilter(filter.attribution);
    if (filter.quality) setQualityFilter(filter.quality);
    if (filter.pendingOnly) setPendingOnly(true);
    if (filter.aiParticipatedOnly) setAiParticipatedOnly(true);
    setSheet(null);
  };

  return (
    <div className="flex h-full flex-col">
      <PageHeader className="h-auto min-h-14 flex-col items-stretch gap-2 px-5 py-2 lg:flex-row lg:items-center lg:justify-between">
        <div className="flex min-w-0 items-center gap-2">
          <Radar className="h-4 w-4 shrink-0 text-muted-foreground" />
          <h1 className="truncate text-sm font-medium">{t(($) => $.operations.title)}</h1>
        </div>
        {/* Page-level dimensions only: the time window and workstream scope
            everything — stats, drawers, and table.
            All other filters live on the detail table and narrow only it. */}
        <nav
          role="toolbar"
          aria-label={t(($) => $.operations.title)}
          className="flex min-w-0 flex-1 flex-wrap items-center gap-2 lg:justify-end"
        >
          <ValueFilter
            ariaLabel={t(($) => $.operations.filter.workstream)}
            value={workstreamFilter}
            allValue={ALL_WORKSTREAMS}
            allLabel={t(($) => $.operations.filter.workstream_all)}
            options={workstreamOptions}
            onChange={setWorkstreamFilter}
          />
          <Segmented
            value={days}
            onChange={setDays}
            options={RANGES.map((r) => ({ label: r.label, value: r.days }))}
          />
        </nav>
      </PageHeader>

      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-[1600px] space-y-4 p-6">
          <div className="flex items-center justify-between gap-3">
            <div className="min-w-0">
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <p className="text-xs font-medium text-foreground">
                  {t(($) => $.operations.assessment_title)}
                </p>
                <Badge variant="outline" className="text-muted-foreground">
                  {t(($) => $.operations.range_label, { days })}
                </Badge>
                {allFixes.length >= FETCH_LIMIT ? (
                  <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                    <TriangleAlert className="h-3 w-3 shrink-0" />
                    {t(($) => $.operations.fetch_cap_notice, {
                      limit: FETCH_LIMIT,
                    })}
                  </span>
                ) : null}
              </div>
              <p className="mt-0.5 text-xs text-muted-foreground">
                {t(($) => $.operations.subtitle)}
              </p>
            </div>
            <div className="flex shrink-0 items-center gap-3">
              {widthsModified ? (
                <button
                  type="button"
                  onClick={resetColumnWidths}
                  className="text-xs text-muted-foreground transition-colors hover:text-foreground"
                >
                  {t(($) => $.operations.reset_columns)}
                </button>
              ) : null}
              {!isLoading && tableRows.length > 0 ? (
                <span className="text-xs text-muted-foreground">
                  {t(($) => $.operations.caption, { count: tableRows.length })}
                </span>
              ) : null}
            </div>
          </div>

          {!isLoading && !isError && statsRows.length > 0 ? (
            <OperationsSummary
              kpis={kpis}
              days={days}
              onCardClick={(card) => setSheet({ kind: "card", card })}
            />
          ) : null}

          {isLoading ? (
            <OperationsSkeleton />
          ) : isError ? (
            <OperationsError
              onRetry={() => {
                void Promise.all([
                  statsQuery.refetch(),
                  tableQuery.refetch(),
                  feishuStatusQuery.refetch(),
                ]);
              }}
            />
          ) : statsRows.length === 0 ? (
            <OperationsEmpty search="" />
          ) : (
            <>
              {/* Detail-only filters narrow this table, never the stats above. */}
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <SearchBox value={searchInput} onChange={setSearchInput} />
                <AgentFilter
                  agents={agents}
                  value={agentFilter}
                  onChange={setAgentFilter}
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
                  aria-pressed={pendingOnly}
                  variant="outline"
                  size="sm"
                  onClick={() => setPendingOnly((v) => !v)}
                  className={`h-8 rounded-lg px-3 text-xs shadow-none transition-colors ${
                    pendingOnly
                      ? "border-primary/30 bg-primary/10 text-primary hover:bg-primary/15"
                      : "border-border/70 bg-muted/25 text-muted-foreground hover:bg-muted/45 hover:text-foreground"
                  }`}
                >
                  {t(($) => $.operations.filter.pending_only)}
                </Button>
                {detailFiltersDirty ? (
                  <button
                    type="button"
                    onClick={resetDetailFilters}
                    className="text-xs text-muted-foreground underline underline-offset-2 transition-colors hover:text-foreground"
                  >
                    {t(($) => $.operations.filter.reset)}
                  </button>
                ) : null}
                <span className="ml-auto hidden text-xs text-muted-foreground/70 lg:inline">
                  {t(($) => $.operations.filter.scope_note)}
                </span>
              </div>
              {tableRows.length === 0 ? (
                <OperationsEmpty search={search} />
              ) : (
              <>
              {/* overflow-x-auto: when a dragged column outgrows the viewport the
                  table scrolls horizontally instead of crushing its fixed columns. */}
              <div className="overflow-x-auto">
                <div
                  ref={cardRef}
                  className="rounded-lg border bg-card"
                  style={cardStyle(columnWidths)}
                >
                  {/* Header: issue, external status, agent, evidence, predictions, review, date. */}
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
                      columnKey="time"
                      cardRef={cardRef}
                      label={t(($) => $.operations.table.time)}
                    />
                  </div>
                  <div className="divide-y">
                    {pagedRows.map((f) => {
                      const agent = agents.find((a) => a.id === f.agent_id);
                      const comment = (f.last_comment ?? "").trim();
                      // Day axis: latest run's completion day, falling back to
                      // start/created when it has no completed_at (running/queued).
                      const day = fixDayIso(f, viewTZ);
                      return (
                        <div
                          key={f.issue_id || f.task_id}
                          className="grid cursor-pointer items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/30"
                          style={GRID_STYLE}
                          // Row click opens the issue drawer; clicks on the
                          // row's own links/buttons/popovers keep their
                          // behavior.
                          onClick={(e) => {
                            const el = e.target as HTMLElement;
                            if (el.closest("a,button,[role='dialog']")) return;
                            setSheet({ kind: "issue", fix: f });
                          }}
                        >
                          <IssueCell
                            fix={f}
                            slug={slug}
                            issueStatusLabel={issueStatusLabel(f.issue_status)}
                            comment={comment}
                            search={search}
                          />
                          <ExternalStatusCell
                            fix={f}
                            statusNames={feishuStatusNames}
                          />
                          <div className="flex min-w-0 items-center gap-2 overflow-hidden">
                            {f.agent_id ? (
                              <ActorAvatar
                                actorType="agent"
                                actorId={f.agent_id}
                                size="md"
                                enableHoverCard
                              />
                            ) : null}
                            <span className="min-w-0 truncate text-sm">
                              {agent?.name ||
                                f.agent_name ||
                                t(($) => $.operations.drawer.no_agent)}
                            </span>
                          </div>
                          <P4EvidenceCell fix={f} swarmBase={swarmBase} />
                          <PredictionCell
                            value={deriveAttribution(f)}
                            kind="attribution"
                            detail={confidenceLabel(f.p4_assessment?.confidence)}
                          />
                          <AssessmentQualityCell
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
                          <span className="min-w-0 overflow-hidden truncate whitespace-nowrap text-xs text-muted-foreground tabular-nums">
                            {day}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                </div>
              </div>
              {pageCount > 1 ? (
                <div className="flex items-center justify-end gap-2">
                  <span className="text-xs text-muted-foreground tabular-nums">
                    {t(($) => $.operations.pagination.page_of, {
                      page: safePage + 1,
                      pages: pageCount,
                    })}
                  </span>
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    className="h-7 w-7"
                    aria-label={t(($) => $.operations.pagination.prev)}
                    disabled={safePage === 0}
                    onClick={() => setPage(safePage - 1)}
                  >
                    <ChevronLeft className="h-4 w-4" />
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    className="h-7 w-7"
                    aria-label={t(($) => $.operations.pagination.next)}
                    disabled={safePage >= pageCount - 1}
                    onClick={() => setPage(safePage + 1)}
                  >
                    <ChevronRight className="h-4 w-4" />
                  </Button>
                </div>
              ) : null}
              </>
              )}
            </>
          )}
        </div>
      </div>
      <OperationsSheet
        state={sheet}
        onStateChange={setSheet}
        rows={statsRows}
        slug={slug}
        swarmBase={swarmBase}
        viewTZ={viewTZ}
        issueStatusLabel={issueStatusLabel}
        externalStatusLabel={externalStatusLabel}
        onDrill={applyDrill}
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

// Drag handle for one resizable column. To keep the visible rows from
// re-rendering on every pointer move, the drag writes the column's CSS var
// (and the scroll floor) straight onto the card element and only commits the
// final width to the store on release. Double-click restores this column's
// default width.
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
  comment,
  search,
}: {
  fix: AgentFixRecord;
  slug: string | null;
  issueStatusLabel: string;
  comment: string;
  search: string;
}) {
  const inner = (
    <div className="grid min-w-0 gap-1 overflow-hidden">
      <div className="flex min-w-0 items-center gap-2">
        <span className="shrink-0 font-mono text-xs text-muted-foreground tabular-nums">
          {fix.issue_identifier || "—"}
        </span>
        <span className="truncate text-sm font-medium group-hover:underline">
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
      {comment ? (
        <p
          className="truncate text-xs text-muted-foreground"
          title={comment}
        >
          <ReasonText text={comment} keyword={search} />
        </p>
      ) : null}
    </div>
  );
  // Link to the issue when we know the workspace slug; fall back to plain text
  // so a missing slug (rendered before workspace resolves) never breaks the row.
  if (slug && fix.issue_id && fix.issue_identifier) {
    return (
      <AppLink
        href={paths.workspace(slug).issueDetail(fix.issue_identifier)}
        className="group block min-w-0 overflow-hidden"
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
  const workItemId = fix.external?.work_item_id ?? "";
  const workItemUrl = fix.external?.url ?? "";
  return (
    <div className="grid min-w-0 gap-1">
      <ToneBadge
        tone={done === true ? "success" : done === false ? "warning" : "muted"}
      >
        {status}
      </ToneBadge>
      {workItemId ? (
        workItemUrl ? (
          <a
            href={workItemUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="flex min-w-0 items-center gap-1 font-mono text-xs text-muted-foreground hover:text-foreground hover:underline"
          >
            <span className="truncate">{workItemId}</span>
            <ExternalLink className="h-3 w-3 shrink-0" />
          </a>
        ) : (
          <span className="truncate font-mono text-xs text-muted-foreground">
            {workItemId}
          </span>
        )
      ) : null}
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

function P4EvidenceCell({
  fix,
  swarmBase,
}: {
  fix: AgentFixRecord;
  swarmBase: string;
}) {
  const { t } = useT("usage");
  const p4 = fix.p4_assessment;
  const evidence = derivedEvidence(fix);
  const swarmUrl =
    firstSwarmReviewUrl(fix) || swarmReviewUrl(swarmBase, evidence.swarm);
  const primaryCl = evidence.finalCl
    ? { label: t(($) => $.operations.p4.final_cl), value: evidence.finalCl }
    : evidence.shelve
      ? { label: t(($) => $.operations.p4.shelve), value: evidence.shelve }
      : null;
  const detailRows = [
    evidence.swarm
      ? {
          label: t(($) => $.operations.p4.swarm),
          value: evidence.swarm,
          href: swarmUrl,
        }
      : null,
    evidence.swarmChanges
      ? {
          label: t(($) => $.operations.p4.changes),
          value: evidence.swarmChanges,
        }
      : null,
    evidence.swarmCommits
      ? {
          label: t(($) => $.operations.p4.commits),
          value: evidence.swarmCommits,
        }
      : null,
    evidence.swarmBranch
      ? {
          label: t(($) => $.operations.p4.branch),
          value: evidence.swarmBranch,
        }
      : null,
    evidence.eventType
      ? { label: t(($) => $.operations.p4.event), value: evidence.eventType }
      : null,
    evidence.sentAt
      ? { label: t(($) => $.operations.p4.sent_at), value: evidence.sentAt }
      : null,
    p4?.warnings?.length
      ? {
          label: t(($) => $.operations.p4.warnings),
          value: p4.warnings.join(", "),
        }
      : null,
  ].filter(Boolean) as Array<{ label: string; value: string; href?: string }>;
  const meta = [
    evidence.workstream
      ? `${t(($) => $.operations.p4.workstream)} ${evidence.workstream}`
      : "",
    p4?.warnings?.length
      ? t(($) => $.operations.p4.warning_count, {
          count: p4.warnings.length,
        })
      : "",
  ].filter(Boolean);
  if (!hasP4Signal(fix)) {
    return (
      <span className="text-xs text-muted-foreground">
        {t(($) => $.operations.p4.no_evidence)}
      </span>
    );
  }
  return (
    <div className="grid min-w-0 gap-1.5 justify-items-start overflow-hidden">
      <div className="flex min-w-0 max-w-full items-center gap-1.5 overflow-hidden">
        {primaryCl ? (
          <ClListBadge
            label={primaryCl.label}
            cls={primaryCl.value}
            swarmBase={swarmBase}
          />
        ) : evidence.swarm ? (
          <EvidenceBadge href={swarmUrl}>
            {t(($) => $.operations.p4.swarm_value, { value: evidence.swarm })}
          </EvidenceBadge>
        ) : null}
        {detailRows.length > 0 ? (
          <Popover>
            <PopoverTrigger
              render={
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-6 min-w-0 max-w-full gap-1 px-1.5 text-xs text-muted-foreground"
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
                {detailRows.map(({ label, value, href }) => (
                  <div key={label} className="grid gap-0.5">
                    <div className="text-[11px] uppercase text-muted-foreground">
                      {label}
                    </div>
                    {href ? (
                      <a
                        href={href}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="flex min-w-0 items-center gap-1 break-words text-xs hover:underline"
                      >
                        <span className="min-w-0 break-all">{value}</span>
                        <ExternalLink className="h-3 w-3 shrink-0" />
                      </a>
                    ) : (
                      <div className="break-words text-xs">{value}</div>
                    )}
                  </div>
                ))}
              </div>
            </PopoverContent>
          </Popover>
        ) : null}
      </div>
      {meta.length > 0 ? (
        <div className="flex min-w-0 max-w-full items-center gap-1.5 overflow-hidden text-xs text-muted-foreground">
          {meta.map((item, index) => (
            <span
              key={item}
              className={index === 0 ? "min-w-0 truncate" : "shrink-0"}
            >
              {index > 0 ? "· " : ""}
              {item}
            </span>
          ))}
        </div>
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

function AssessmentQualityCell({
  fix,
  pending,
  onTrigger,
}: {
  fix: AgentFixRecord;
  pending: boolean;
  onTrigger: (bindingId: string, force: boolean) => void;
}) {
  return (
    <div className="grid min-w-0 gap-1.5 justify-items-start overflow-hidden">
      <PredictionCell
        value={fix.p4_assessment?.quality_prediction}
        kind="quality"
      />
      <div className="flex min-w-0 max-w-full flex-wrap items-center gap-1.5 overflow-hidden">
        <AssessmentStatusBadge value={fix.p4_assessment?.assessment_status} />
        <AssessmentTriggerButton
          fix={fix}
          pending={pending}
          onTrigger={onTrigger}
        />
      </div>
      <AssessmentRunDetail p4={fix.p4_assessment} />
    </div>
  );
}

// Queue observability line under the status badge. Running rows show which
// agent is on it; failed/pending rows show how many attempts happened and the
// last recorded error, so "why is this stuck" is answerable from the table
// instead of psql.
function AssessmentRunDetail({ p4 }: { p4?: AgentFixRecord["p4_assessment"] }) {
  const { t } = useT("usage");
  if (!p4) return null;
  const status = p4.assessment_status ?? "";
  if (status === "running") {
    const agent = (p4.assessment_agent_name ?? "").trim();
    if (!agent) return null;
    return (
      <span className="max-w-full truncate text-xs text-muted-foreground">
        {agent}
      </span>
    );
  }
  const attempts = p4.attempt_count ?? 0;
  const lastError = (p4.last_error ?? "").trim();
  if (!lastError && attempts <= 1) return null;
  const parts = [
    attempts > 1
      ? t(($) => $.operations.assessment_obs.attempts, { count: attempts })
      : "",
    lastError,
  ].filter(Boolean);
  const text = parts.join(" · ");
  return (
    <span
      className="max-w-full truncate text-xs text-muted-foreground"
      title={text}
    >
      {text}
    </span>
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
  if (!bindingId) {
    return null;
  }
  // Assessment needs the external work item to be done first. Render the
  // button disabled with an explanation instead of hiding it, so users learn
  // what unblocks it rather than wondering where it went.
  const externalDone = fix.external?.mapped_status === "done";

  const status = fix.p4_assessment?.assessment_status ?? "";
  const assessmentActive = status === "pending" || status === "running";
  const hasAssessment = status !== "";
  const force = hasAssessment && !assessmentActive;
  const disabled = pending || assessmentActive || !externalDone;
  const label = pending
    ? t(($) => $.operations.assessment_action.starting)
    : assessmentActive
      ? t(($) => $.operations.assessment_action.in_progress)
      : hasAssessment
        ? t(($) => $.operations.assessment_action.rerun)
        : t(($) => $.operations.assessment_action.run);
  const Icon = hasAssessment ? RefreshCw : Play;
  const compact = hasAssessment;

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      disabled={disabled}
      title={
        !externalDone
          ? t(($) => $.operations.assessment_action.blocked_external)
          : compact
            ? label
            : undefined
      }
      onClick={() => onTrigger(bindingId, force)}
      className={
        compact
          ? "h-7 w-7 shrink-0 px-0 text-xs"
          : "h-7 min-w-0 max-w-full px-2 text-xs"
      }
    >
      <Icon className="h-3.5 w-3.5 shrink-0" />
      {compact ? (
        <span className="sr-only">{label}</span>
      ) : (
        <span className="min-w-0 max-w-28 truncate">{label}</span>
      )}
    </Button>
  );
}

function EvidenceBadge({
  children,
  href,
}: {
  children: ReactNode;
  href?: string;
}) {
  const badge = (
    <Badge
      variant="outline"
      className="max-w-full justify-start truncate border-border bg-muted font-mono text-muted-foreground"
    >
      <span className="truncate">{children}</span>
    </Badge>
  );
  if (href) {
    return (
      <a
        href={href}
        target="_blank"
        rel="noopener noreferrer"
        className="min-w-0 max-w-full hover:opacity-80"
      >
        {badge}
      </a>
    );
  }
  return badge;
}

// Evidence badge for one or more changelist numbers ("282941, 283006"). Each
// CL links to the Swarm change page when the workspace has a Swarm URL.
function ClListBadge({
  label,
  cls,
  swarmBase,
}: {
  label: string;
  cls: string;
  swarmBase: string;
}) {
  const items = cls
    .split(",")
    .map((v) => v.trim())
    .filter(Boolean);
  return (
    <Badge
      variant="outline"
      className="max-w-full justify-start truncate border-border bg-muted font-mono text-muted-foreground"
    >
      <span className="truncate">
        {label}{" "}
        {items.map((cl, i) => {
          const url = swarmChangeUrl(swarmBase, cl);
          return (
            <span key={cl}>
              {i > 0 ? ", " : ""}
              {url ? (
                <a
                  href={url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="underline-offset-2 hover:text-foreground hover:underline"
                >
                  {cl}
                </a>
              ) : (
                cl
              )}
            </span>
          );
        })}
      </span>
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
    <div className="relative min-w-[220px] flex-1 sm:flex-none">
      <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
      <Input
        type="search"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={t(($) => $.operations.search_placeholder)}
        aria-label={t(($) => $.operations.search_placeholder)}
        className={SEARCH_INPUT_CLASS}
      />
      {value ? (
        <button
          type="button"
          onClick={() => onChange("")}
          aria-label={t(($) => $.operations.search_clear)}
          className={SEARCH_CLEAR_CLASS}
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
  const ariaLabel = t(($) => $.operations.table.agent);
  const selected = agents.find((a) => a.id === value);
  return (
    <Select value={value} onValueChange={(v) => onChange(v ?? ALL_AGENTS)}>
      <SelectTrigger
        size="sm"
        aria-label={ariaLabel}
        className={`${FILTER_SELECT_TRIGGER_CLASS} min-w-[150px] max-w-[190px]`}
      >
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
    <div className="flex items-center gap-0.5">
      <Select value={value} onValueChange={(v) => onChange(v ?? allValue)}>
        <SelectTrigger
          size="sm"
          aria-label={ariaLabel}
          className={`${FILTER_SELECT_TRIGGER_CLASS} min-w-[138px] max-w-[180px]`}
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
      {value !== allValue ? (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={allLabel}
          title={allLabel}
          className="h-7 w-7 shrink-0 rounded-md text-muted-foreground hover:bg-background/70 hover:text-foreground"
          onClick={() => onChange(allValue)}
        >
          <X className="h-3.5 w-3.5" />
        </Button>
      ) : null}
    </div>
  );
}

function OperationsSkeleton() {
  return (
    <div className="space-y-2">
      <Skeleton className="h-24 rounded-lg" />
      <Skeleton className="h-9 rounded-lg" />
      <Skeleton className="h-48 rounded-lg" />
    </div>
  );
}

function OperationsError({ onRetry }: { onRetry: () => void }) {
  const { t } = useT("usage");
  return (
    <div className="flex flex-col items-center rounded-lg border border-dashed py-12 text-center">
      <TriangleAlert className="h-6 w-6 text-destructive" />
      <p className="mt-3 text-sm font-medium">
        {t(($) => $.operations.error.title)}
      </p>
      <p className="mt-1 max-w-md text-xs text-muted-foreground">
        {t(($) => $.operations.error.body)}
      </p>
      <Button type="button" variant="outline" className="mt-4" onClick={onRetry}>
        <RefreshCw className="h-4 w-4" />
        {t(($) => $.operations.error.retry)}
      </Button>
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
