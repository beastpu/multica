"use client";

import { useEffect, useMemo, useState } from "react";
import { Radar, Search, X } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
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
import { operationsFixesOptions } from "@multica/core/dashboard";
import type { AgentFixRecord, IssueStatus } from "@multica/core/types";
import { PageHeader } from "../../layout/page-header";
import { ActorAvatar } from "../../common/actor-avatar";
import { StatusIcon } from "../../issues/components/status-icon";
import { AppLink } from "../../navigation";
import { useViewingTimezone } from "../../common/use-viewing-timezone";
import { useT } from "../../i18n";
import { Segmented } from "./segmented";

const ALL_AGENTS = "__all__";
const ALL_STATUSES = "__all__";

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
  const [days, setDays] = useState<OpsRange>(30);
  const [agentFilter, setAgentFilter] = useState<string>(ALL_AGENTS);
  const [statusFilter, setStatusFilter] = useState<string>(ALL_STATUSES);
  // `searchInput` is what the user types; `search` is the debounced term that
  // actually keys the query (so we don't refetch on every keystroke).
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");

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

  // Validate the picked agent against the current workspace's list so a stale
  // id (deleted agent, or a leftover after a workspace switch) doesn't silently
  // filter everything to empty while the dropdown still reads a name.
  const effectiveAgent = useMemo(() => {
    if (agentFilter === ALL_AGENTS) return ALL_AGENTS;
    return agents.some((a) => a.id === agentFilter) ? agentFilter : ALL_AGENTS;
  }, [agentFilter, agents]);

  const rows = useMemo(() => {
    return fixes.filter((f) => {
      if (effectiveAgent !== ALL_AGENTS && f.agent_id !== effectiveAgent) {
        return false;
      }
      if (statusFilter !== ALL_STATUSES && f.issue_status !== statusFilter) {
        return false;
      }
      return true;
    });
  }, [fixes, effectiveAgent, statusFilter]);

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
          <Segmented
            value={days}
            onChange={setDays}
            options={RANGES.map((r) => ({ label: r.label, value: r.days }))}
          />
        </div>
      </PageHeader>

      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-6xl space-y-4 p-6">
          <div className="flex items-center justify-between gap-3">
            <p className="text-xs text-muted-foreground">
              {t(($) => $.operations.subtitle)}
            </p>
            {!fixesQuery.isLoading && rows.length > 0 ? (
              <span className="shrink-0 text-xs text-muted-foreground">
                {t(($) => $.operations.caption, { count: rows.length })}
              </span>
            ) : null}
          </div>

          {fixesQuery.isLoading ? (
            <OperationsSkeleton />
          ) : rows.length === 0 ? (
            <OperationsEmpty search={search} />
          ) : (
            <div className="rounded-lg border bg-card">
              {/* Header row — Agent first, then Issue / Time (by day) / Status
                  / Reason. Same grid language as the dashboard leaderboard. */}
              <div className="grid grid-cols-[minmax(0,1.4fr)_minmax(0,2fr)_6rem_7.5rem_minmax(0,2.4fr)] items-center gap-3 border-b px-4 py-2 text-xs font-medium text-muted-foreground">
                <span>{t(($) => $.operations.table.agent)}</span>
                <span>{t(($) => $.operations.table.issue)}</span>
                <span>{t(($) => $.operations.table.time)}</span>
                <span>{t(($) => $.operations.table.status)}</span>
                <span>{t(($) => $.operations.table.reason)}</span>
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
                      className="grid grid-cols-[minmax(0,1.4fr)_minmax(0,2fr)_6rem_7.5rem_minmax(0,2.4fr)] items-center gap-3 px-4 py-2.5"
                    >
                      <div className="flex min-w-0 items-center gap-2">
                        <ActorAvatar
                          actorType="agent"
                          actorId={f.agent_id}
                          size={22}
                          enableHoverCard
                        />
                        <span className="truncate text-sm">
                          {agent?.name ?? f.agent_name}
                        </span>
                      </div>
                      <IssueCell fix={f} slug={slug} />
                      <span className="text-xs text-muted-foreground tabular-nums">
                        {day}
                      </span>
                      <div className="flex min-w-0 items-center gap-1.5">
                        {isKnownIssueStatus(f.issue_status) && (
                          <StatusIcon
                            status={f.issue_status}
                            className="h-3.5 w-3.5"
                          />
                        )}
                        <span className="truncate text-sm">
                          {issueStatusLabel(f.issue_status)}
                        </span>
                      </div>
                      <span
                        className="truncate text-xs text-muted-foreground"
                        title={comment || undefined}
                      >
                        {comment ? (
                          <ReasonText text={comment} keyword={search} />
                        ) : (
                          t(($) => $.operations.no_reason)
                        )}
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function IssueCell({
  fix,
  slug,
}: {
  fix: AgentFixRecord;
  slug: string | null;
}) {
  const inner = (
    <>
      <span className="shrink-0 font-mono text-xs text-muted-foreground tabular-nums">
        {fix.issue_identifier}
      </span>
      <span className="truncate text-sm">{fix.issue_title}</span>
    </>
  );
  // Link to the issue when we know the workspace slug; fall back to plain text
  // so a missing slug (rendered before workspace resolves) never breaks the row.
  if (slug && fix.issue_id) {
    return (
      <AppLink
        href={paths.workspace(slug).issueDetail(fix.issue_identifier)}
        className="flex min-w-0 items-center gap-2 hover:underline"
      >
        {inner}
      </AppLink>
    );
  }
  return <div className="flex min-w-0 items-center gap-2">{inner}</div>;
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
