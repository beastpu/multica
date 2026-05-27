import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { ReplaceFeishuProjectRoutesRequest } from "../types";

export const feishuProjectKeys = {
  all: (wsId: string) => ["feishu-project", wsId] as const,
  integration: (wsId: string) => [...feishuProjectKeys.all(wsId), "integration"] as const,
  issueStatuses: (wsId: string) => [...feishuProjectKeys.all(wsId), "issue-statuses"] as const,
  sync: (wsId: string) => [...feishuProjectKeys.all(wsId), "sync"] as const,
  fields: (wsId: string, workItemType: string) =>
    [...feishuProjectKeys.all(wsId), "fields", workItemType] as const,
  businessLines: (wsId: string) => [...feishuProjectKeys.all(wsId), "business-lines"] as const,
  routes: (wsId: string) => [...feishuProjectKeys.all(wsId), "routes"] as const,
};

export const feishuProjectIntegrationOptions = (wsId: string) =>
  queryOptions({
    queryKey: feishuProjectKeys.integration(wsId),
    queryFn: () => api.getFeishuProjectIntegration(wsId),
    enabled: !!wsId,
  });

export const feishuProjectIssueStatusesOptions = (wsId: string, enabled = true) =>
  queryOptions({
    queryKey: feishuProjectKeys.issueStatuses(wsId),
    queryFn: () => api.getFeishuProjectIssueStatuses(wsId),
    enabled: !!wsId && enabled,
  });

export const feishuProjectSyncOptions = (wsId: string, enabled = true) =>
  queryOptions({
    queryKey: feishuProjectKeys.sync(wsId),
    queryFn: () => api.getFeishuProjectSync(wsId),
    enabled: !!wsId && enabled,
  });

// Field list is only useful while the user is wiring up the integration — fetching it
// outside that flow burns plugin-token quota for no reason. Callers should pass
// `enabled: integration.enabled` so the query stays idle until the integration exists.
export const feishuProjectFieldsOptions = (wsId: string, workItemType = "issue", enabled = true) =>
  queryOptions({
    queryKey: feishuProjectKeys.fields(wsId, workItemType),
    queryFn: () => api.listFeishuProjectWorkItemFields(wsId, workItemType),
    enabled: !!wsId && enabled,
  });

// Same gating concern as feishuProjectFieldsOptions — only enable when the user has
// already chosen a business-line field. `staleTime: Infinity` because the biz-line tree
// changes rarely; explicit refetch via queryClient.invalidateQueries on save.
export const feishuProjectBusinessLinesOptions = (wsId: string, enabled = true) =>
  queryOptions({
    queryKey: feishuProjectKeys.businessLines(wsId),
    queryFn: () => api.listFeishuProjectBusinessLines(wsId),
    enabled: !!wsId && enabled,
    staleTime: Infinity,
  });

export const feishuProjectRoutesOptions = (wsId: string, enabled = true) =>
  queryOptions({
    queryKey: feishuProjectKeys.routes(wsId),
    queryFn: () => api.listFeishuProjectRoutes(wsId),
    enabled: !!wsId && enabled,
  });

export function useReplaceFeishuProjectRoutes(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: ReplaceFeishuProjectRoutesRequest) => api.replaceFeishuProjectRoutes(wsId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: feishuProjectKeys.routes(wsId) });
    },
  });
}
