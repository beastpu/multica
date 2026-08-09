import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export interface WorkflowInstanceFilters {
  status?: string;
  related_to_me?: boolean;
  project_id?: string;
  workflow_id?: string;
  current_node_key?: string;
  owner_type?: "member" | "agent" | "squad";
  owner_id?: string;
  intervention_type?: string;
  has_host_issue?: boolean;
  cursor?: string;
  limit?: number;
}

export interface WorkflowFilters {
  status?: string;
}

export const workflowKeys = {
  all: (wsId: string) => ["workflows", wsId] as const,
  instances: (wsId: string) =>
    [...workflowKeys.all(wsId), "instances"] as const,
  instanceList: (wsId: string, filters: WorkflowInstanceFilters = {}) =>
    [...workflowKeys.instances(wsId), "list", filters] as const,
  instance: (wsId: string, instanceId: string) =>
    [...workflowKeys.instances(wsId), "detail", instanceId] as const,
  issueInstance: (wsId: string, issueId: string) =>
    [...workflowKeys.instances(wsId), "issue", issueId] as const,
  issueNode: (wsId: string, issueId: string) =>
    [...workflowKeys.instances(wsId), "issue", issueId, "node"] as const,
  instanceIssues: (wsId: string, instanceId: string) =>
    [...workflowKeys.instance(wsId, instanceId), "issues"] as const,
  acceptances: (wsId: string, instanceId: string) =>
    [...workflowKeys.instance(wsId, instanceId), "acceptances"] as const,
  events: (wsId: string, instanceId: string) =>
    [...workflowKeys.instance(wsId, instanceId), "events"] as const,
  diagnostics: (wsId: string, instanceId: string) =>
    [...workflowKeys.instance(wsId, instanceId), "diagnostics"] as const,
  node: (wsId: string, nodeInstanceId: string) =>
    [...workflowKeys.all(wsId), "nodes", nodeInstanceId] as const,
  templates: (wsId: string) =>
    [...workflowKeys.all(wsId), "templates"] as const,
  templateList: (wsId: string, filters: WorkflowFilters = {}) =>
    [...workflowKeys.templates(wsId), "list", filters] as const,
  template: (wsId: string, templateId: string) =>
    [...workflowKeys.templates(wsId), "detail", templateId] as const,
  builtinTemplates: (wsId: string) =>
    [...workflowKeys.templates(wsId), "builtin"] as const,
};

export function workflowInstanceListOptions(
  wsId: string,
  filters: WorkflowInstanceFilters = {},
) {
  return queryOptions({
    queryKey: workflowKeys.instanceList(wsId, filters),
    queryFn: () => api.listWorkflowInstances(filters),
  });
}

export function workflowInstanceInfiniteListOptions(
  wsId: string,
  filters: WorkflowInstanceFilters = {},
) {
  return infiniteQueryOptions({
    queryKey: [
      ...workflowKeys.instances(wsId),
      "infinite-list",
      filters,
    ] as const,
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      api.listWorkflowInstances({
        ...filters,
        cursor: pageParam,
      }),
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
  });
}

export function workflowInstanceOptions(wsId: string, instanceId: string) {
  return queryOptions({
    queryKey: workflowKeys.instance(wsId, instanceId),
    queryFn: () => api.getWorkflowInstance(instanceId),
    enabled: Boolean(instanceId),
  });
}

export function issueWorkflowOptions(wsId: string, issueId: string) {
  return queryOptions({
    queryKey: workflowKeys.issueInstance(wsId, issueId),
    queryFn: () => api.getIssueWorkflow(issueId),
    enabled: Boolean(issueId),
    retry: false,
  });
}

// The node context behind a child issue. A 404 means the issue is not a node
// of anything, which is the common case and not a failure — hence retry: false.
export function issueWorkflowNodeOptions(wsId: string, issueId: string) {
  return queryOptions({
    queryKey: workflowKeys.issueNode(wsId, issueId),
    queryFn: () => api.getIssueWorkflowNode(issueId),
    enabled: Boolean(issueId),
    retry: false,
  });
}

export function workflowInstanceIssuesOptions(
  wsId: string,
  instanceId: string,
) {
  return queryOptions({
    queryKey: workflowKeys.instanceIssues(wsId, instanceId),
    queryFn: () => api.listWorkflowInstanceIssues(instanceId),
    enabled: Boolean(instanceId),
  });
}

export function workflowAcceptancesOptions(wsId: string, instanceId: string) {
  return queryOptions({
    queryKey: workflowKeys.acceptances(wsId, instanceId),
    queryFn: () => api.listWorkflowAcceptances(instanceId),
    enabled: Boolean(instanceId),
  });
}

export function workflowEventsOptions(wsId: string, instanceId: string) {
  return queryOptions({
    queryKey: workflowKeys.events(wsId, instanceId),
    queryFn: () => api.listWorkflowInstanceEvents(instanceId),
    enabled: Boolean(instanceId),
  });
}

export function workflowDiagnosticsOptions(
  wsId: string,
  instanceId: string,
  enabled = true,
) {
  return queryOptions({
    queryKey: workflowKeys.diagnostics(wsId, instanceId),
    queryFn: () => api.getWorkflowInstanceDiagnostics(instanceId),
    enabled: Boolean(instanceId) && enabled,
  });
}

export function workflowNodeOptions(wsId: string, nodeInstanceId: string) {
  return queryOptions({
    queryKey: workflowKeys.node(wsId, nodeInstanceId),
    queryFn: () => api.getWorkflowNode(nodeInstanceId),
    enabled: Boolean(nodeInstanceId),
  });
}

export function workflowNodeArtifactsOptions(wsId: string, nodeInstanceId: string) {
  return queryOptions({
    queryKey: [...workflowKeys.node(wsId, nodeInstanceId), "artifacts"],
    queryFn: () => api.listWorkflowNodeArtifacts(nodeInstanceId),
    enabled: Boolean(nodeInstanceId),
  });
}

export function workflowInstanceArtifactsOptions(wsId: string, instanceId: string) {
  return queryOptions({
    queryKey: [...workflowKeys.instance(wsId, instanceId), "artifacts"],
    queryFn: () => api.listWorkflowInstanceArtifacts(instanceId),
    enabled: Boolean(instanceId),
  });
}

export function workflowListOptions(
  wsId: string,
  filters: WorkflowFilters = {},
) {
  return queryOptions({
    queryKey: workflowKeys.templateList(wsId, filters),
    queryFn: () => api.listWorkflows(filters),
  });
}

export function workflowBuiltinTemplateListOptions(wsId: string) {
  return queryOptions({
    queryKey: workflowKeys.builtinTemplates(wsId),
    queryFn: () => api.listBuiltinWorkflowTemplates(),
  });
}

export function workflowOptions(wsId: string, templateId: string) {
  return queryOptions({
    queryKey: workflowKeys.template(wsId, templateId),
    queryFn: () => api.getWorkflow(templateId),
    enabled: Boolean(templateId),
  });
}
