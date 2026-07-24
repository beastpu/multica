/**
 * Mobile-owned Workflow queries.
 *
 * The endpoint and cache identities mirror
 * `packages/core/workflows/queries.ts`, but mobile keeps its own query
 * functions so cancellation, reconnect invalidation, and cache ownership stay
 * within the native app boundary.
 */
import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const WORKFLOWS_ACTIVITY_ENGINE_FLAG = "workflows_activity_engine";

export const workflowFeatureOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: ["public-config", "workflow-feature", wsId] as const,
    queryFn: ({ signal }) => api.getPublicConfig({ signal }),
    enabled: Boolean(wsId),
    staleTime: Number.POSITIVE_INFINITY,
  });

export const workflowKeys = {
  all: (wsId: string | null) => ["workflows", wsId] as const,
  instance: (wsId: string | null, instanceId: string) =>
    [...workflowKeys.all(wsId), "instances", "detail", instanceId] as const,
  issueInstance: (wsId: string | null, issueId: string) =>
    [...workflowKeys.all(wsId), "instances", "issue", issueId] as const,
  instanceIssues: (wsId: string | null, instanceId: string) =>
    [...workflowKeys.instance(wsId, instanceId), "issues"] as const,
  node: (wsId: string | null, nodeInstanceId: string) =>
    [...workflowKeys.all(wsId), "nodes", nodeInstanceId] as const,
  template: (wsId: string | null, templateId: string) =>
    [...workflowKeys.all(wsId), "templates", "detail", templateId] as const,
};

export const issueWorkflowOptions = (
  wsId: string | null,
  issueId: string,
) =>
  queryOptions({
    queryKey: workflowKeys.issueInstance(wsId, issueId),
    queryFn: ({ signal }) => api.getIssueWorkflow(issueId, { signal }),
    enabled: !!wsId && !!issueId,
    retry: false,
  });

export const workflowInstanceOptions = (
  wsId: string | null,
  instanceId: string,
) =>
  queryOptions({
    queryKey: workflowKeys.instance(wsId, instanceId),
    queryFn: ({ signal }) => api.getWorkflowInstance(instanceId, { signal }),
    enabled: !!wsId && !!instanceId,
  });

export const workflowNodeOptions = (
  wsId: string | null,
  nodeInstanceId: string,
) =>
  queryOptions({
    queryKey: workflowKeys.node(wsId, nodeInstanceId),
    queryFn: ({ signal }) => api.getWorkflowNode(nodeInstanceId, { signal }),
    enabled: !!wsId && !!nodeInstanceId,
  });

export const workflowInstanceIssuesOptions = (
  wsId: string | null,
  instanceId: string,
) =>
  queryOptions({
    queryKey: workflowKeys.instanceIssues(wsId, instanceId),
    queryFn: ({ signal }) =>
      api.listWorkflowInstanceIssues(instanceId, { signal }),
    enabled: !!wsId && !!instanceId,
  });

export const workflowTemplateOptions = (
  wsId: string | null,
  templateId: string,
) =>
  queryOptions({
    queryKey: workflowKeys.template(wsId, templateId),
    queryFn: ({ signal }) => api.getWorkflowTemplate(templateId, { signal }),
    enabled: !!wsId && !!templateId,
  });
