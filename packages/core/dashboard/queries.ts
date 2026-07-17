import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const dashboardKeys = {
  all: (wsId: string) => ["dashboard", wsId] as const,
  daily: (
    wsId: string,
    days: number,
    projectId: string | null,
    tz: string,
  ) => [...dashboardKeys.all(wsId), "daily", days, projectId, tz] as const,
  byAgent: (
    wsId: string,
    days: number,
    projectId: string | null,
    tz: string,
  ) => [...dashboardKeys.all(wsId), "by-agent", days, projectId, tz] as const,
  agentRuntime: (
    wsId: string,
    days: number,
    projectId: string | null,
    tz: string,
  ) => [...dashboardKeys.all(wsId), "agent-runtime", days, projectId, tz] as const,
  runTimeDaily: (
    wsId: string,
    days: number,
    projectId: string | null,
    tz: string,
  ) => [...dashboardKeys.all(wsId), "runtime-daily", days, projectId, tz] as const,
  operationsFixes: (
    wsId: string,
    days: number,
    externalStatus: string,
    search: string,
  ) =>
    [
      ...dashboardKeys.all(wsId),
      "operations-fixes",
      days,
      externalStatus,
      search,
    ] as const,
};

// 5-min rollup cadence on the server, 60s background refetch on the client.
const STALE_TIME = 60 * 1000;

// Range changes should keep the previous result mounted so KPI cards and
// charts transition in place instead of falling back to a full-page skeleton.
// Scope changes are deliberately excluded: carrying data across workspaces,
// projects, report kinds, or timezones would briefly display the wrong data.
function isSameDashboardScope(
  previousKey: readonly unknown[] | undefined,
  nextKey: readonly unknown[],
): boolean {
  if (!previousKey || previousKey.length !== nextKey.length) return false;
  return previousKey.every(
    (part, index) => index === 3 || Object.is(part, nextKey[index]),
  );
}

// `tz` participates in every dashboard key so a Preferences change
// repoints the cache. All four series — token rollups and the
// atq.completed_at-based run-time series — slice their day boundary in
// the viewer's tz, so the four dashboard tabs always agree.
export function dashboardUsageDailyOptions(
  wsId: string,
  days: number,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.daily(wsId, days, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getDashboardUsageDaily({
        days,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardUsageByAgentOptions(
  wsId: string,
  days: number,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.byAgent(wsId, days, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getDashboardUsageByAgent({
        days,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardAgentRunTimeOptions(
  wsId: string,
  days: number,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.agentRuntime(wsId, days, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getDashboardAgentRunTime({
        days,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

export function dashboardRunTimeDailyOptions(
  wsId: string,
  days: number,
  projectId: string | null,
  tz: string,
) {
  const queryKey = dashboardKeys.runTimeDaily(wsId, days, projectId, tz);
  return queryOptions({
    queryKey,
    queryFn: () =>
      api.getDashboardRunTimeDaily({
        days,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    placeholderData: (previousData, previousQuery) =>
      isSameDashboardScope(previousQuery?.queryKey, queryKey)
        ? keepPreviousData(previousData)
        : undefined,
  });
}

// Per-agent "fix record" feed for the Usage page's Operations tab. No tz /
// project axis — Agent narrowing stays client-side, but the
// comment `search` and Feishu `externalStatus` are server filters (they must run
// before the row cap to cover the whole window), so both key the cache with
// `days`. A trimmed empty search is the unfiltered feed.
export function operationsFixesOptions(
  wsId: string,
  days: number,
  search = "",
  externalStatus?: string,
) {
  const term = search.trim();
  const status = externalStatus?.trim() ?? "";
  return queryOptions({
    queryKey: dashboardKeys.operationsFixes(wsId, days, status, term),
    queryFn: () =>
      api.getOperationsAgentFixes({
        days,
        search: term,
        externalStatus: status || undefined,
      }),
    // Callers that omit externalStatus keep the legacy broad feed (used by
    // issue-level assessment entry). Operations passes an explicit status and
    // stays disabled until the Feishu option has resolved.
    enabled: !!wsId && (externalStatus === undefined || !!status),
    staleTime: STALE_TIME,
    // Keep the prior rows on screen while a new term/window refetches, so
    // typing in the search box doesn't flash the skeleton on every keystroke.
    placeholderData: keepPreviousData,
  });
}
