import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRef } from "react";
import { api } from "../api";
import { useWorkspaceId } from "../hooks";
import { issueKeys } from "../issues/queries";
import type {
  StartWorkflowInput,
  CreateWorkflowInput,
  RunWorkflowInput,
  WorkflowDefinition,
  WorkflowInstanceDetail,
  WorkflowNodeDetail,
  WorkflowDetail,
  WorkflowSubmission,
} from "./types";
import { workflowKeys } from "./queries";

function mutationKey() {
  return crypto.randomUUID();
}

function useRetrySafeMutationKey() {
  const pending = useRef(new Map<string, string>());
  const fingerprint = (payload: unknown) => JSON.stringify(payload ?? null);
  return {
    forPayload(payload: unknown) {
      const key = fingerprint(payload);
      const existing = pending.current.get(key);
      if (existing) {
        return existing;
      }
      const next = mutationKey();
      pending.current.set(key, next);
      return next;
    },
    clear(payload: unknown) {
      pending.current.delete(fingerprint(payload));
    },
  };
}

export function useStartIssueWorkflow(issueId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: StartWorkflowInput) => {
      const { idempotency_key: _callerKey, ...payload } = input;
      return api.startIssueWorkflow(issueId, {
        ...input,
        idempotency_key: idempotency.forPayload(payload),
      });
    },
    onSuccess: (detail, input) => {
      const { idempotency_key: _callerKey, ...payload } = input;
      idempotency.clear(payload);
      qc.setQueryData(workflowKeys.issueInstance(wsId, issueId), detail);
      qc.setQueryData(
        workflowKeys.instance(wsId, detail.instance.id),
        detail,
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) });
      qc.invalidateQueries({ queryKey: issueKeys.detail(wsId, issueId) });
    },
  });
}

export function useCreateWorkflowRun() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: CreateWorkflowInput) => {
      const { idempotency_key: _callerKey, ...payload } = input;
      return api.createWorkflowRun({
        ...input,
        idempotency_key: idempotency.forPayload(payload),
      });
    },
    onSuccess: (detail, input) => {
      const { idempotency_key: _callerKey, ...payload } = input;
      idempotency.clear(payload);
      qc.setQueryData(
        workflowKeys.instance(wsId, detail.instance.id),
        detail,
      );
    },
    onSettled: () =>
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) }),
  });
}

export function useRunWorkflow(templateId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: RunWorkflowInput) => {
      const { idempotency_key: _callerKey, ...payload } = input;
      return api.runWorkflow(templateId, {
        ...input,
        idempotency_key: idempotency.forPayload(payload),
      });
    },
    onSuccess: (detail, input) => {
      const { idempotency_key: _callerKey, ...payload } = input;
      idempotency.clear(payload);
      qc.setQueryData(
        workflowKeys.instance(wsId, detail.instance.id),
        detail,
      );
    },
    onSettled: () =>
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) }),
  });
}

export function useUpdateWorkflowInstanceRoles(instanceId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (
      roleAssignments: StartWorkflowInput["role_assignments"],
    ) => api.updateWorkflowInstanceRoles(instanceId, {
      role_assignments: roleAssignments,
      idempotency_key: idempotency.forPayload(roleAssignments),
    }),
    onSuccess: (detail, roleAssignments) => {
      idempotency.clear(roleAssignments);
      qc.setQueryData(
        workflowKeys.instance(wsId, instanceId),
        detail,
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) });
    },
  });
}

export function useReconcileWorkflowInstance(instanceId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: () =>
      api.reconcileWorkflowInstance(instanceId, idempotency.forPayload(null)),
    onSuccess: (detail) => {
      idempotency.clear(null);
      qc.setQueryData(workflowKeys.instance(wsId, instanceId), detail);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instanceIssues(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.acceptances(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) });
    },
  });
}

export function usePauseWorkflowInstance(instanceId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: () =>
      api.pauseWorkflowInstance(instanceId, idempotency.forPayload(null)),
    onSuccess: (detail) => {
      idempotency.clear(null);
      qc.setQueryData(workflowKeys.instance(wsId, instanceId), detail);
    },
    onSettled: () =>
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) }),
  });
}

export function useResumeWorkflowInstance(instanceId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: () =>
      api.resumeWorkflowInstance(instanceId, idempotency.forPayload(null)),
    onSuccess: (detail) => {
      idempotency.clear(null);
      qc.setQueryData(workflowKeys.instance(wsId, instanceId), detail);
    },
    onSettled: () =>
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) }),
  });
}

export function useCancelWorkflowInstance(instanceId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (reason: string) =>
      api.cancelWorkflowInstance(
        instanceId,
        reason,
        idempotency.forPayload(reason),
      ),
    onSuccess: (detail, reason) => {
      idempotency.clear(reason);
      qc.setQueryData(workflowKeys.instance(wsId, instanceId), detail);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instanceIssues(wsId, instanceId) });
    },
  });
}

export function useTransitionWorkflowNode(
  instanceId: string,
  nodeInstanceId: string,
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: {
      action: "complete" | "skip" | "rollback";
      reason: string;
    }) => api.transitionWorkflowNode(
      nodeInstanceId,
      input.action,
      input.reason,
      idempotency.forPayload(input),
    ),
    onSuccess: (detail, input) => {
      idempotency.clear(input);
      qc.setQueryData(workflowKeys.instance(wsId, instanceId), detail);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.node(wsId, nodeInstanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instanceIssues(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) });
    },
  });
}

export function useCreateWorkflowNodeIssue(
  instanceId: string,
  nodeInstanceId: string,
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: {
      title: string;
      description?: string;
      assignee_type?: "member" | "agent" | "squad";
      assignee_id?: string;
      required?: boolean;
    }) => api.createWorkflowNodeIssue(nodeInstanceId, {
      ...input,
      idempotency_key: idempotency.forPayload(input),
    }),
    onSuccess: (_detail, input) => idempotency.clear(input),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.node(wsId, nodeInstanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instanceIssues(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) });
    },
  });
}

export function useResolveWorkflowNodeExecutor(
  instanceId: string,
  nodeInstanceId: string,
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: {
      task_id: string;
      actor_type: "member" | "agent" | "squad";
      actor_id: string;
      reason?: string;
    }) => api.resolveWorkflowNodeExecutor(nodeInstanceId, {
      ...input,
      idempotency_key: idempotency.forPayload(input),
    }),
    onSuccess: (_detail, input) => idempotency.clear(input),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.node(wsId, nodeInstanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instanceIssues(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) });
    },
  });
}

export function useChangeWorkflowNodeTask(
  instanceId: string,
  nodeInstanceId: string,
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: {
      taskId: string;
      action: "retry" | "detach";
      reason: string;
    }) => api.changeWorkflowNodeTask(
      input.taskId,
      input.action,
      input.reason,
      idempotency.forPayload(input),
    ),
    onSuccess: (_detail, input) => idempotency.clear(input),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.node(wsId, nodeInstanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instanceIssues(wsId, instanceId) });
    },
  });
}

export interface CreateWorkflowSubmissionInput {
  /** Structured output field values; validated against the node's declaration. */
  payload: Record<string, unknown>;
  summary?: string;
  evidence?: unknown[];
  source_issue_id?: string;
  source_agent_run_id?: string;
}

// Reviewing an artifact changes whether the node may complete, so the node
// detail and the artifact list both go stale. Invalidate rather than patch: the
// server may have superseded the row between read and write, and a patched
// cache would show a decision that never landed.
export function useReviewWorkflowArtifact(
  instanceId: string,
  nodeInstanceId: string,
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (input: {
      artifactId: string;
      status: "approved" | "rejected";
      comment?: string;
    }) =>
      api.reviewWorkflowArtifact(nodeInstanceId, input.artifactId, {
        status: input.status,
        comment: input.comment,
      }),
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: [...workflowKeys.node(wsId, nodeInstanceId), "artifacts"],
      });
      qc.invalidateQueries({ queryKey: workflowKeys.node(wsId, nodeInstanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
    },
  });
}

// Adopting an attachment already on the issue as this node's artifact. The
// file is in the system; this records that it is the node's deliverable rather
// than uploading a second copy of it.
export function useSubmitWorkflowArtifact(
  instanceId: string,
  nodeInstanceId: string,
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (input: {
      artifactKey: string;
      attachmentId?: string;
      content?: string;
      url?: string;
      issueId?: string;
    }) =>
      api.submitWorkflowArtifact(nodeInstanceId, {
        artifact_key: input.artifactKey,
        attachment_id: input.attachmentId,
        content: input.content,
        url: input.url,
        issue_id: input.issueId,
      }),
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: [...workflowKeys.node(wsId, nodeInstanceId), "artifacts"],
      });
      qc.invalidateQueries({ queryKey: workflowKeys.node(wsId, nodeInstanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
    },
  });
}

export function useCreateWorkflowSubmission(
  instanceId: string,
  nodeInstanceId: string,
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const nodeKey = workflowKeys.node(wsId, nodeInstanceId);
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: CreateWorkflowSubmissionInput) =>
      api.createWorkflowSubmission(nodeInstanceId, {
        ...input,
        idempotency_key: idempotency.forPayload(input),
      }),
    onMutate: async (input) => {
      await qc.cancelQueries({ queryKey: nodeKey });
      const previous = qc.getQueryData<WorkflowNodeDetail>(nodeKey);
      const optimistic: WorkflowSubmission = {
        id: `optimistic:${mutationKey()}`,
        workflow_node_instance_id: nodeInstanceId,
        revision: (previous?.submissions[0]?.revision ?? 0) + 1,
        status: "submitting",
        payload: input.payload,
        summary: input.summary ?? "",
        evidence: input.evidence ?? [],
        submitted_by_type: "member",
        submitted_by_id: null,
        source_issue_id: input.source_issue_id ?? null,
        source_agent_run_id: input.source_agent_run_id ?? null,
        created_at: new Date().toISOString(),
      };
      qc.setQueryData<WorkflowNodeDetail>(nodeKey, (old) =>
        old ? { ...old, submissions: [optimistic, ...old.submissions] } : old,
      );
      return { previous };
    },
    onError: (_error, _input, context) => {
      if (context?.previous) qc.setQueryData(nodeKey, context.previous);
    },
    onSuccess: (_detail, input) => idempotency.clear(input),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: nodeKey });
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) });
    },
  });
}

export function useCreateWorkflowVerdict(
  instanceId: string,
  nodeInstanceId: string,
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: {
      result: "pass" | "fail" | "blocked";
      reason?: string;
      confidence?: number;
      evidence?: unknown[];
    }) => api.createWorkflowVerdict(nodeInstanceId, {
      ...input,
      idempotency_key: idempotency.forPayload(input),
    }),
    onSuccess: (_detail, input) => idempotency.clear(input),
    onSettled: () => {
      qc.invalidateQueries({
        queryKey: workflowKeys.node(wsId, nodeInstanceId),
      });
      qc.invalidateQueries({
        queryKey: workflowKeys.instance(wsId, instanceId),
      });
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) });
    },
  });
}

export interface DecideWorkflowAcceptanceInput {
  status: "approved" | "rejected" | "changes_requested";
  reason?: string;
  rework_target_node_key?: string;
  evidence?: unknown[];
}

export function useDecideWorkflowAcceptance(instanceId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: DecideWorkflowAcceptanceInput) =>
      api.decideWorkflowAcceptance(instanceId, {
        ...input,
        idempotency_key: idempotency.forPayload(input),
      }),
    onSuccess: (_detail, input) => idempotency.clear(input),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instanceIssues(wsId, instanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instances(wsId) });
    },
  });
}

export function useCreateWorkflow() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (input: {
      name: string;
      description?: string;
      definition: WorkflowDefinition;
      change_summary?: string;
    }) => api.createWorkflow(input),
    onSettled: () =>
      qc.invalidateQueries({ queryKey: workflowKeys.templates(wsId) }),
  });
}

export function useCreateWorkflowTemplateFromBuiltin() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (key: string) => api.createWorkflowTemplateFromBuiltin(key),
    onSettled: () =>
      qc.invalidateQueries({ queryKey: workflowKeys.templates(wsId) }),
  });
}

export function useUpdateWorkflow(templateId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (input: {
      name?: string;
      description?: string;
    }) => api.updateWorkflow(templateId, input),
    onSuccess: (template) => {
      qc.setQueryData(
        workflowKeys.template(wsId, templateId),
        (current: WorkflowDetail | undefined) =>
          current ? { ...current, template } : current,
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.template(wsId, templateId) });
      qc.invalidateQueries({ queryKey: workflowKeys.templates(wsId) });
    },
  });
}

export function useCopyWorkflow() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: async ({
      templateId,
      name,
    }: {
      templateId: string;
      name: string;
    }) => {
      const detail = await api.getWorkflow(templateId);
      // Every stored version is runnable, so copying means copying the
      // newest one.
      const source = [...detail.versions]
        .sort((a, b) => b.version - a.version)[0];
      if (!source) {
        throw new Error("Workflow has no version to copy");
      }
      return api.createWorkflow({
        name,
        description: detail.workflow.description,
        definition: { ...source.definition, name },
        change_summary: `Copied from ${detail.workflow.name} v${source.version}`,
      });
    },
    onSettled: () =>
      qc.invalidateQueries({ queryKey: workflowKeys.templates(wsId) }),
  });
}

// Saving is the whole editing flow: it allocates or reuses a version, stores
// the definition, and makes it live when it validates. Invalidates the
// template list too, because the live version is what the list shows.
export function useSaveWorkflowDefinition(templateId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (input: {
      definition: WorkflowDefinition;
      change_summary?: string;
      base_version_id?: string;
    }) => api.saveWorkflowDefinition(templateId, input),
    // The saved version has to be in the cache before this resolves. The
    // editor selects it the moment the mutation succeeds, and until the
    // invalidated detail query came back that id matched nothing in
    // `versions` — so the editor read as "not the latest version", went
    // read-only, reset the selection to the previous version and reloaded its
    // definition over the one just saved. The server hands back the canonical
    // version; write it in rather than wait for the round trip.
    onSuccess: (result) => {
      qc.setQueryData(
        workflowKeys.template(wsId, templateId),
        (current: WorkflowDetail | undefined) => {
          if (!current) return current;
          if (current.versions.some((v) => v.id === result.version.id)) {
            return current;
          }
          return { ...current, versions: [result.version, ...current.versions] };
        },
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.template(wsId, templateId) });
      qc.invalidateQueries({ queryKey: workflowKeys.templates(wsId) });
    },
  });
}

export function useArchiveWorkflow(templateId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.archiveWorkflow(templateId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.template(wsId, templateId) });
      qc.invalidateQueries({ queryKey: workflowKeys.templates(wsId) });
    },
  });
}

export function setWorkflowInstanceDetail(
  current: WorkflowInstanceDetail | undefined,
  next: WorkflowInstanceDetail,
) {
  return current?.instance.id === next.instance.id ? next : current;
}
