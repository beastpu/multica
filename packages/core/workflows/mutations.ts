import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRef } from "react";
import { api } from "../api";
import { useAuthStore } from "../auth";
import { useWorkspaceId } from "../hooks";
import { issueKeys } from "../issues/queries";
import type {
  StartWorkflowInput,
  CreateWorkflowInput,
  WorkflowDefinition,
  WorkflowInstanceDetail,
  WorkflowNodeDetail,
  WorkflowTemplateDetail,
  WorkflowIssueTemplate,
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

export function useCreateWorkflow() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: CreateWorkflowInput) => {
      const { idempotency_key: _callerKey, ...payload } = input;
      return api.createWorkflow({
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

export function useConfirmWorkflowNode(
  instanceId: string,
  nodeInstanceId: string,
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const userId = useAuthStore((state) => state.user?.id);
  const nodeKey = workflowKeys.node(wsId, nodeInstanceId);
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (input: {
      decision: "approved" | "rejected";
      comment?: string;
    }) => api.confirmWorkflowNode(nodeInstanceId, {
      ...input,
      idempotency_key: idempotency.forPayload(input),
    }),
    onMutate: async (input) => {
      await qc.cancelQueries({ queryKey: nodeKey });
      const previous = qc.getQueryData<WorkflowNodeDetail>(nodeKey);
      if (previous && userId) {
        const now = new Date().toISOString();
        const optimistic = {
          id: `optimistic:${mutationKey()}`,
          workflow_node_instance_id: nodeInstanceId,
          member_id: userId,
          decision: input.decision,
          comment: input.comment ?? "",
          decided_at: now,
          updated_at: now,
        };
        const confirmations = previous.confirmations.some(
          (item) => item.member_id === userId,
        )
          ? previous.confirmations.map((item) =>
            item.member_id === userId ? optimistic : item
          )
          : [optimistic, ...previous.confirmations];
        qc.setQueryData<WorkflowNodeDetail>(nodeKey, {
          ...previous,
          confirmations,
        });
      }
      return { previous };
    },
    onError: (_error, _input, context) => {
      if (context?.previous) qc.setQueryData(nodeKey, context.previous);
    },
    onSuccess: (_detail, input) => idempotency.clear(input),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.node(wsId, nodeInstanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
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
  payload: Record<string, unknown>;
  summary?: string;
  evidence?: unknown[];
  source_issue_id?: string;
  source_agent_run_id?: string;
  proposed_tasks?: WorkflowIssueTemplate[];
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
        proposed_tasks: input.proposed_tasks ?? [],
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

export function useConfirmWorkflowSubmissionTasks(
  instanceId: string,
  nodeInstanceId: string,
) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  const idempotency = useRetrySafeMutationKey();
  return useMutation({
    mutationFn: (submissionId: string) =>
      api.confirmWorkflowSubmissionTasks(
        nodeInstanceId,
        submissionId,
        idempotency.forPayload(submissionId),
      ),
    onSuccess: (_detail, submissionId) =>
      idempotency.clear(submissionId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.node(wsId, nodeInstanceId) });
      qc.invalidateQueries({ queryKey: workflowKeys.instance(wsId, instanceId) });
      qc.invalidateQueries({
        queryKey: workflowKeys.instanceIssues(wsId, instanceId),
      });
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

export function useCreateWorkflowTemplate() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (input: {
      name: string;
      description?: string;
      applies_to_type_key?: string;
      definition: WorkflowDefinition;
      change_summary?: string;
    }) => api.createWorkflowTemplate(input),
    onSettled: () =>
      qc.invalidateQueries({ queryKey: workflowKeys.templates(wsId) }),
  });
}

export function useUpdateWorkflowTemplate(templateId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (input: {
      name?: string;
      description?: string;
      applies_to_type_key?: string;
    }) => api.updateWorkflowTemplate(templateId, input),
    onSuccess: (template) => {
      qc.setQueryData(
        workflowKeys.template(wsId, templateId),
        (current: WorkflowTemplateDetail | undefined) =>
          current ? { ...current, template } : current,
      );
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.template(wsId, templateId) });
      qc.invalidateQueries({ queryKey: workflowKeys.templates(wsId) });
    },
  });
}

export function useCopyWorkflowTemplate() {
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
      const detail = await api.getWorkflowTemplate(templateId);
      const source = [...detail.versions]
        .sort((a, b) => b.version - a.version)
        .find((version) => version.status === "published") ??
        detail.versions.find((version) => version.status === "draft");
      if (!source) {
        throw new Error("Workflow template has no version to copy");
      }
      return api.createWorkflowTemplate({
        name,
        description: detail.template.description,
        applies_to_type_key: detail.template.applies_to_type_key,
        definition: { ...source.definition, name },
        change_summary: `Copied from ${detail.template.name} v${source.version}`,
      });
    },
    onSettled: () =>
      qc.invalidateQueries({ queryKey: workflowKeys.templates(wsId) }),
  });
}

export function useUpdateWorkflowTemplateDraft(templateId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (input: {
      definition: WorkflowDefinition;
      change_summary?: string;
      revision: number;
    }) => api.updateWorkflowTemplateDraft(templateId, input),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: workflowKeys.template(wsId, templateId) }),
  });
}

export function useCreateWorkflowTemplateDraft(templateId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.createWorkflowTemplateDraft(templateId),
    onSettled: () =>
      qc.invalidateQueries({ queryKey: workflowKeys.template(wsId, templateId) }),
  });
}

export function usePublishWorkflowTemplate(templateId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.publishWorkflowTemplate(templateId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.template(wsId, templateId) });
      qc.invalidateQueries({ queryKey: workflowKeys.templates(wsId) });
    },
  });
}

export function useArchiveWorkflowTemplate(templateId: string) {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.archiveWorkflowTemplate(templateId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: workflowKeys.template(wsId, templateId) });
      qc.invalidateQueries({ queryKey: workflowKeys.templates(wsId) });
    },
  });
}

export function useValidateWorkflowTemplateDefinition(templateId: string) {
  return useMutation({
    mutationFn: (definition: WorkflowDefinition) =>
      api.validateWorkflowTemplateDefinition(templateId, definition),
  });
}

export function setWorkflowInstanceDetail(
  current: WorkflowInstanceDetail | undefined,
  next: WorkflowInstanceDetail,
) {
  return current?.instance.id === next.instance.id ? next : current;
}
