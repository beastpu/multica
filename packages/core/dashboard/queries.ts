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
  operationsFixes: (wsId: string, days: number, search: string) =>
    [...dashboardKeys.all(wsId), "operations-fixes", days, search] as const,
};

// 5-min rollup cadence on the server, 60s background refetch on the client.
const STALE_TIME = 60 * 1000;

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
  return queryOptions({
    queryKey: dashboardKeys.daily(wsId, days, projectId, tz),
    queryFn: () =>
      api.getDashboardUsageDaily({
        days,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
  });
}

export function dashboardUsageByAgentOptions(
  wsId: string,
  days: number,
  projectId: string | null,
  tz: string,
) {
  return queryOptions({
    queryKey: dashboardKeys.byAgent(wsId, days, projectId, tz),
    queryFn: () =>
      api.getDashboardUsageByAgent({
        days,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
  });
}

export function dashboardAgentRunTimeOptions(
  wsId: string,
  days: number,
  projectId: string | null,
  tz: string,
) {
  return queryOptions({
    queryKey: dashboardKeys.agentRuntime(wsId, days, projectId, tz),
    queryFn: () =>
      api.getDashboardAgentRunTime({
        days,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
  });
}

export function dashboardRunTimeDailyOptions(
  wsId: string,
  days: number,
  projectId: string | null,
  tz: string,
) {
  return queryOptions({
    queryKey: dashboardKeys.runTimeDaily(wsId, days, projectId, tz),
    queryFn: () =>
      api.getDashboardRunTimeDaily({
        days,
        project_id: projectId ?? undefined,
        tz,
      }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
  });
}

// Per-agent "fix record" feed for the Usage page's Operations tab. No tz /
// project axis — agent and issue-status narrowing stays client-side, but the
// comment `search` is a server filter (it must run before the row cap to cover
// the whole window), so both `days` and `search` key the cache. A trimmed empty
// search is the unfiltered feed.
export function operationsFixesOptions(
  wsId: string,
  days: number,
  search = "",
) {
  const term = search.trim();
  return queryOptions({
    queryKey: dashboardKeys.operationsFixes(wsId, days, term),
    queryFn: () => api.getOperationsAgentFixes({ days, search: term }),
    enabled: !!wsId,
    staleTime: STALE_TIME,
    // Keep the prior rows on screen while a new term/window refetches, so
    // typing in the search box doesn't flash the skeleton on every keystroke.
    placeholderData: keepPreviousData,
  });
}
