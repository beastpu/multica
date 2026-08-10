"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { GitBranch, Loader2, Users } from "lucide-react";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  issueDetailOptions,
  issueListOptions,
} from "@multica/core/issues/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  useStartIssueWorkflow,
  workflowListOptions,
  workflowOptions,
  type WorkflowDefinition,
  type WorkflowRoleDefinition,
} from "@multica/core/workflows";
import {
  agentListOptions,
  memberListOptions,
  squadListOptions,
} from "@multica/core/workspace/queries";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@multica/ui/components/ui/dialog";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../i18n";
import { useNavigation } from "../navigation";
import {
  workflowPreviewActivities,
  workflowPreviewBranches,
} from "./workflow-preview";

type WorkflowActorType = "member" | "agent" | "squad";

interface WorkflowActorOption {
  type: WorkflowActorType;
  id: string;
  name: string;
}

function assignmentKey(type: WorkflowActorType, id: string): string {
  return `${type}:${id}`;
}

function parseAssignment(value: string): {
  actorType: WorkflowActorType;
  actorId: string;
} | null {
  const separator = value.indexOf(":");
  if (separator < 1) return null;
  const actorType = value.slice(0, separator);
  if (actorType !== "member" && actorType !== "agent" && actorType !== "squad") {
    return null;
  }
  const actorId = value.slice(separator + 1);
  return actorId ? { actorType, actorId } : null;
}

export function defaultAssignments(
  roles: WorkflowRoleDefinition[],
  userId: string | undefined,
): Record<string, string> {
  const assignments: Record<string, string> = {};
  if (!userId) return assignments;
  const owner = roles.find((role) =>
    role.key === "owner" && role.allowed_actor_types.includes("member")
  );
  if (owner && !assignments.owner) {
    assignments.owner = assignmentKey("member", userId);
  }
  return assignments;
}

// The host issue's assignee is who the work was already given to, and the
// role that executes the most activities is the run's deliverer. When the two
// are compatible, that role is pre-filled with the assignee; an ambiguous
// definition (no executor roles, or a tie) pre-fills nothing.
export function hostAssigneeDefault(
  definition: Pick<WorkflowDefinition, "roles" | "nodes">,
  assignee: { type: WorkflowActorType; id: string } | null,
): { roleKey: string; value: string } | null {
  if (!assignee) return null;
  const executorCounts = new Map<string, number>();
  for (const node of definition.nodes) {
    if (node.kind !== "activity") continue;
    const executor = node.executor;
    if (executor?.kind === "role" && executor.role) {
      executorCounts.set(
        executor.role,
        (executorCounts.get(executor.role) ?? 0) + 1,
      );
    }
  }
  let topRole: string | null = null;
  let topCount = 0;
  let tied = false;
  for (const [roleKey, count] of executorCounts) {
    if (count > topCount) {
      topRole = roleKey;
      topCount = count;
      tied = false;
    } else if (count === topCount) {
      tied = true;
    }
  }
  if (!topRole || tied) return null;
  const role = definition.roles.find((item) => item.key === topRole);
  if (!role || !role.allowed_actor_types.includes(assignee.type)) return null;
  return {
    roleKey: role.key,
    value: assignmentKey(assignee.type, assignee.id),
  };
}

export function WorkflowStartDialog({ issueId }: { issueId?: string }) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const userId = useAuthStore((state) => state.user?.id);
  const navigation = useNavigation();
  const paths = useWorkspacePaths();
  const [open, setOpen] = useState(false);
  const [selectedIssueId, setSelectedIssueId] = useState("");
  const [templateId, setTemplateId] = useState("");
  const [versionId, setVersionId] = useState("");
  const [hostStatusMode, setHostStatusMode] = useState<
    "independent" | "managed"
  >("independent");
  const [assignments, setAssignments] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const issueOptionsQuery = useQuery({
    ...issueListOptions(wsId),
    enabled: open && !issueId,
  });
  const issueOptions = useMemo(
    () => (issueOptionsQuery.data ?? []).filter(
      (issue) =>
        !issue.parent_issue_id &&
        issue.status !== "done" &&
        issue.status !== "cancelled",
    ),
    [issueOptionsQuery.data],
  );
  const resolvedIssueId = issueId ?? selectedIssueId;

  const hostIssueQuery = useQuery({
    ...issueDetailOptions(wsId, resolvedIssueId),
    enabled: open && Boolean(resolvedIssueId),
  });
  const hostAssigneeType = hostIssueQuery.data?.assignee_type;
  const hostAssigneeId = hostIssueQuery.data?.assignee_id;
  const hostAssignee = useMemo(
    () =>
      (hostAssigneeType === "member" || hostAssigneeType === "agent" ||
          hostAssigneeType === "squad") && hostAssigneeId
        ? { type: hostAssigneeType, id: hostAssigneeId }
        : null,
    [hostAssigneeType, hostAssigneeId],
  );
  // Remembered so submit can tell an untouched pre-fill (source:
  // host_assignee) from a choice the user made or changed (user_selected).
  const [hostPrefill, setHostPrefill] = useState<
    { roleKey: string; value: string } | null
  >(null);

  const templatesQuery = useQuery({
    ...workflowListOptions(wsId),
    enabled: Boolean(wsId),
  });
  const templates = useMemo(
    () => templatesQuery.data?.workflows ?? [],
    [templatesQuery.data?.workflows],
  );
  const templateQuery = useQuery({
    ...workflowOptions(wsId, templateId),
    enabled: open && Boolean(templateId),
  });
  // Every stored version is runnable, so the whole history is startable.
  const publishedVersions = useMemo(
    () => [...(templateQuery.data?.versions ?? [])]
      .sort((left, right) => right.version - left.version),
    [templateQuery.data?.versions],
  );
  const selectedVersion = publishedVersions.find(
    (version) => version.id === versionId,
  ) ?? publishedVersions[0];
  const roles = selectedVersion?.definition.roles ?? [];

  const { data: members = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: open,
  });
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: open,
  });
  const { data: squads = [] } = useQuery({
    ...squadListOptions(wsId),
    enabled: open,
  });
  const actorOptions = useMemo<WorkflowActorOption[]>(() => [
    ...members.map((member) => ({
      type: "member" as const,
      id: member.user_id,
      name: member.name,
    })),
    ...agents
      .filter((agent) => !agent.archived_at)
      .map((agent) => ({
        type: "agent" as const,
        id: agent.id,
        name: agent.name,
      })),
    ...squads
      .filter((squad) => !squad.archived_at)
      .map((squad) => ({
        type: "squad" as const,
        id: squad.id,
        name: squad.name,
      })),
  ], [agents, members, squads]);
  const start = useStartIssueWorkflow(resolvedIssueId);

  useEffect(() => {
    if (!open || templateId || templates.length === 0) return;
    setTemplateId(templates[0]!.id);
  }, [open, templateId, templates]);

  useEffect(() => {
    if (!selectedVersion) return;
    setVersionId(selectedVersion.id);
    const defaults = defaultAssignments(
      selectedVersion.definition.roles,
      userId,
    );
    const prefill = hostAssigneeDefault(selectedVersion.definition, hostAssignee);
    if (prefill) defaults[prefill.roleKey] = prefill.value;
    setHostPrefill(prefill);
    setAssignments(defaults);
  }, [selectedVersion, userId, hostAssignee]);

  const missingRequiredRole = roles.some(
    (role) => role.required && !parseAssignment(assignments[role.key] ?? ""),
  );

  const close = () => {
    setOpen(false);
    setSelectedIssueId("");
    setTemplateId("");
    setVersionId("");
    setHostStatusMode("independent");
    setAssignments({});
    setHostPrefill(null);
    setError("");
  };

  const submit = () => {
    if (
      !resolvedIssueId ||
      !templateId ||
      !selectedVersion ||
      missingRequiredRole
    ) return;
    setError("");
    start.mutate({
      workflow_id: templateId,
      workflow_version_id: selectedVersion.id,
      host_status_mode: hostStatusMode,
      role_assignments: roles.flatMap((role) => {
        const actor = parseAssignment(assignments[role.key] ?? "");
        return actor
          ? [{
            role_key: role.key,
            actor_type: actor.actorType,
            actor_id: actor.actorId,
            source: hostPrefill &&
                role.key === hostPrefill.roleKey &&
                assignments[role.key] === hostPrefill.value
              ? "host_assignee"
              : "user_selected",
          }]
          : [];
      }),
      idempotency_key: crypto.randomUUID(),
    }, {
      onSuccess: (detail) => {
        close();
        navigation.push(paths.workflowRun(detail.instance.id));
      },
      onError: (cause) => {
        setError(
          cause instanceof Error
            ? cause.message
            : t(($) => $.errors.action_failed),
        );
      },
    });
  };

  // A missing endpoint means the desktop client is connected to an older
  // server. Keep the established Issue surface clean in that case.
  if (templatesQuery.isError) return null;

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) {
          setOpen(true);
          setError("");
        } else if (!start.isPending) {
          close();
        }
      }}
    >
      <DialogTrigger
        render={
          <Button
            variant="outline"
            size="sm"
            className="min-h-11 sm:min-h-8"
            disabled={templatesQuery.isLoading}
          />
        }
      >
        {templatesQuery.isLoading
          ? <Loader2 className="animate-spin motion-reduce:animate-none" />
          : <GitBranch />}
        <span className="hidden sm:inline">
          {issueId
            ? t(($) => $.actions.start_workflow)
            : t(($) => $.actions.start_existing_workflow)}
        </span>
      </DialogTrigger>
      <DialogContent className="max-h-[min(90vh,48rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {issueId
              ? t(($) => $.start.title)
              : t(($) => $.start.existing_title)}
          </DialogTitle>
          <DialogDescription>
            {issueId
              ? t(($) => $.start.description)
              : t(($) => $.start.existing_description)}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-5 py-1">
          {!issueId && (
            <div className="space-y-1.5">
              <Label htmlFor="workflow-existing-issue">
                {t(($) => $.start.host_issue)}
              </Label>
              <select
                id="workflow-existing-issue"
                value={selectedIssueId}
                onChange={(event) => {
                  setSelectedIssueId(event.target.value);
                  setError("");
                }}
                className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 sm:text-sm"
              >
                <option value="">
                  {t(($) => $.start.choose_host_issue)}
                </option>
                {issueOptions.map((issue) => (
                  <option key={issue.id} value={issue.id}>
                    {issue.identifier} · {issue.title}
                  </option>
                ))}
              </select>
              {issueOptionsQuery.isLoading && (
                <p className="text-sm text-muted-foreground">
                  {t(($) => $.start.loading_issues)}
                </p>
              )}
              {!issueOptionsQuery.isLoading && issueOptions.length === 0 && (
                <p className="text-sm text-muted-foreground">
                  {t(($) => $.start.no_host_issues)}
                </p>
              )}
            </div>
          )}
          <div className="space-y-1.5">
            <Label htmlFor={`workflow-template-${resolvedIssueId || "existing"}`}>
              {t(($) => $.start.template)}
            </Label>
            <select
              id={`workflow-template-${resolvedIssueId || "existing"}`}
              value={templateId}
              onChange={(event) => {
                setTemplateId(event.target.value);
                setVersionId("");
                setAssignments({});
                setError("");
              }}
              className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 sm:text-sm"
            >
              <option value="">{t(($) => $.start.choose_template)}</option>
              {templates.map((template) => (
                <option key={template.id} value={template.id}>
                  {template.name}
                </option>
              ))}
            </select>
            {!templatesQuery.isLoading && templates.length === 0 && (
              <p className="text-sm text-amber-700 dark:text-amber-300">
                {t(($) => $.start.no_templates)}
              </p>
            )}
          </div>

          {templateQuery.isLoading && (
            <div aria-label={t(($) => $.start.loading_template)} className="space-y-2">
              <Skeleton className="h-11 rounded-lg" />
              <Skeleton className="h-24 rounded-lg" />
            </div>
          )}
          {templateQuery.isError && (
            <p role="alert" className="text-sm text-destructive">
              {t(($) => $.start.template_load_failed)}
            </p>
          )}

          {selectedVersion && (
            <>
              <div className="space-y-1.5">
                <Label htmlFor={`workflow-version-${resolvedIssueId || "existing"}`}>
                  {t(($) => $.start.version)}
                </Label>
                <select
                  id={`workflow-version-${resolvedIssueId || "existing"}`}
                  value={selectedVersion.id}
                  onChange={(event) => setVersionId(event.target.value)}
                  className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 sm:text-sm"
                >
                  {publishedVersions.map((version) => (
                    <option key={version.id} value={version.id}>
                      {t(($) => $.templates.version, { version: version.version })}
                      {version.change_summary ? ` · ${version.change_summary}` : ""}
                    </option>
                  ))}
                </select>
              </div>

              <section
                aria-labelledby={`workflow-preview-${resolvedIssueId || "existing"}`}
                className="space-y-2 rounded-lg border bg-muted/30 p-3"
              >
                <h3 id={`workflow-preview-${resolvedIssueId || "existing"}`} className="text-sm font-medium">
                  {t(($) => $.start.activity_preview)}
                </h3>
                <ol className="flex flex-wrap items-center gap-1.5">
                  {workflowPreviewActivities(selectedVersion.definition)
                    .map((node, index) => (
                      <li key={node.key} className="flex items-center gap-1.5 text-sm">
                        {index > 0 &&
                          !workflowPreviewBranches(selectedVersion.definition) && (
                          <span aria-hidden className="text-muted-foreground">→</span>
                        )}
                        <span className="rounded-md border bg-background px-2 py-1">
                          {node.name}
                        </span>
                      </li>
                    ))}
                </ol>
              </section>

              {roles.length > 0 && (
                <fieldset className="space-y-3">
                  <legend className="flex items-center gap-2 text-sm font-medium">
                    <Users className="size-4" />
                    {t(($) => $.start.roles)}
                  </legend>
                  {roles.map((role) => {
                    const options = actorOptions.filter((actor) =>
                      role.allowed_actor_types.includes(actor.type)
                    );
                    return (
                      <div key={role.key} className="space-y-1.5">
                        <Label htmlFor={`workflow-role-${resolvedIssueId || "existing"}-${role.key}`}>
                          {role.name}
                          {role.required && (
                            <span className="ml-1 text-destructive" aria-hidden>*</span>
                          )}
                        </Label>
                        <select
                          id={`workflow-role-${resolvedIssueId || "existing"}-${role.key}`}
                          value={assignments[role.key] ?? ""}
                          required={role.required}
                          aria-required={role.required}
                          onChange={(event) => setAssignments((current) => ({
                            ...current,
                            [role.key]: event.target.value,
                          }))}
                          className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 sm:text-sm"
                        >
                          <option value="">
                            {role.required
                              ? t(($) => $.start.choose_actor)
                              : t(($) => $.start.unassigned)}
                          </option>
                          {options.map((actor) => (
                            <option
                              key={assignmentKey(actor.type, actor.id)}
                              value={assignmentKey(actor.type, actor.id)}
                            >
                              {actor.name} · {t(($) => $.start.actor_type[actor.type])}
                            </option>
                          ))}
                        </select>
                        {role.required && options.length === 0 && (
                          <p className="text-sm text-destructive">
                            {t(($) => $.start.no_eligible_actor)}
                          </p>
                        )}
                      </div>
                    );
                  })}
                </fieldset>
              )}

              <fieldset className="space-y-2">
                <legend className="text-sm font-medium">
                  {t(($) => $.start.host_status)}
                </legend>
                <label className="flex min-h-11 cursor-pointer items-start gap-3 rounded-lg border p-3 has-[:checked]:border-primary">
                  <input
                    type="radio"
                    name={`workflow-host-mode-${resolvedIssueId || "existing"}`}
                    value="independent"
                    checked={hostStatusMode === "independent"}
                    onChange={() => setHostStatusMode("independent")}
                    className="mt-1"
                  />
                  <span>
                    <span className="block text-sm font-medium">
                      {t(($) => $.start.independent)}
                    </span>
                    <span className="block text-sm text-muted-foreground">
                      {t(($) => $.start.independent_help)}
                    </span>
                  </span>
                </label>
                <label className="flex min-h-11 cursor-pointer items-start gap-3 rounded-lg border p-3 has-[:checked]:border-primary">
                  <input
                    type="radio"
                    name={`workflow-host-mode-${resolvedIssueId || "existing"}`}
                    value="managed"
                    checked={hostStatusMode === "managed"}
                    onChange={() => setHostStatusMode("managed")}
                    className="mt-1"
                  />
                  <span>
                    <span className="block text-sm font-medium">
                      {t(($) => $.start.managed)}
                    </span>
                    <span className="block text-sm text-muted-foreground">
                      {t(($) => $.start.managed_help)}
                    </span>
                  </span>
                </label>
              </fieldset>
            </>
          )}

          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            className="min-h-11 sm:min-h-8"
            onClick={close}
            disabled={start.isPending}
          >
            {t(($) => $.start.cancel)}
          </Button>
          <Button
            type="button"
            className="min-h-11 sm:min-h-8"
            onClick={submit}
            disabled={
              start.isPending || !resolvedIssueId || !templateId || !selectedVersion ||
              missingRequiredRole
            }
          >
            {start.isPending
              ? <Loader2 className="animate-spin motion-reduce:animate-none" />
              : <GitBranch />}
            {start.isPending
              ? t(($) => $.start.starting)
              : t(($) => $.actions.start_workflow)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
