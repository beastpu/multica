"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2, Play, Users } from "lucide-react";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  useRunWorkflow,
  workflowOptions,
  type Workflow,
  type WorkflowRoleDefinition,
  type WorkflowVersion,
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
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../i18n";
import { useNavigation } from "../navigation";
import { workflowPreviewActivities } from "./workflow-preview";

export type WorkflowActorType = "member" | "agent" | "squad";

export interface WorkflowActorOption {
  type: WorkflowActorType;
  id: string;
  name: string;
}

export function workflowAssignmentKey(type: WorkflowActorType, id: string) {
  return `${type}:${id}`;
}

export function parseWorkflowAssignment(value: string): {
  actorType: WorkflowActorType;
  actorId: string;
} | null {
  const separator = value.indexOf(":");
  if (separator < 1) return null;
  const actorType = value.slice(0, separator);
  if (actorType !== "member" && actorType !== "agent" &&
    actorType !== "squad") {
    return null;
  }
  const actorId = value.slice(separator + 1);
  return actorId ? { actorType, actorId } : null;
}

// Every run opened with the workflow's own name, so a workflow's history was a
// column of identical titles — nothing distinguished run three from run one
// without opening both. Numbering off the run count gives each run an identity
// on sight. It stays a starting value: the author types over it when a run
// deserves a real name, and the number is not an identifier the system reads
// back, so a stale count costs a duplicate label and nothing more.
export function defaultRunTitle(name: string, runCount: number) {
  return `${name} #${runCount + 1}`;
}

export function defaultNewWorkflowAssignments(
  roles: WorkflowRoleDefinition[],
  userId: string | undefined,
): Record<string, string> {
  if (!userId) return {};
  const owner = roles.find((role) =>
    role.key === "owner" && role.allowed_actor_types.includes("member")
  );
  return owner
    ? { owner: workflowAssignmentKey("member", userId) }
    : {};
}

export function WorkflowRunDialog({
  workflow,
  preferredVersion,
  open,
  onOpenChange,
}: {
  workflow: Workflow | null;
  preferredVersion?: WorkflowVersion | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const userId = useAuthStore((state) => state.user?.id);
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const workflowId = workflow?.id ?? "";
  const [title, setTitle] = useState("");
  const [instructions, setInstructions] = useState("");
  const [versionId, setVersionId] = useState("");
  const [assignments, setAssignments] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const workflowQuery = useQuery({
    ...workflowOptions(wsId, workflowId),
    enabled: open && Boolean(workflowId),
  });
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
  const run = useRunWorkflow(workflowId);
  // A save returns the new version before the invalidated detail query has
  // necessarily refetched. Merge it in so "save, then run" always targets
  // exactly the version the user just saved.
  const runnableVersions = useMemo(() => {
    const versions = [...(workflowQuery.data?.versions ?? [])];
    if (preferredVersion && !versions.some(
      (version) => version.id === preferredVersion.id
    )) {
      versions.push(preferredVersion);
    }
    return versions.sort((left, right) => right.version - left.version);
  }, [preferredVersion, workflowQuery.data?.versions]);
  const selectedVersion = runnableVersions.find(
    (version) => version.id === versionId,
  ) ?? runnableVersions[0];
  const roles = selectedVersion?.definition.roles ?? [];
  const actorOptions = useMemo<WorkflowActorOption[]>(() => [
    ...members.map((member) => ({
      type: "member" as const,
      id: member.user_id,
      name: member.name,
    })),
    ...agents.filter((agent) => !agent.archived_at).map((agent) => ({
      type: "agent" as const,
      id: agent.id,
      name: agent.name,
    })),
    ...squads.filter((squad) => !squad.archived_at).map((squad) => ({
      type: "squad" as const,
      id: squad.id,
      name: squad.name,
    })),
  ], [agents, members, squads]);

  const workflowName = workflow?.name ?? "";
  const workflowRunCount = workflow?.run_count ?? 0;
  useEffect(() => {
    if (!open) return;
    setTitle(workflowName ? defaultRunTitle(workflowName, workflowRunCount) : "");
    setInstructions("");
    setVersionId(preferredVersion?.id ?? "");
    setAssignments({});
    setError("");
  }, [open, preferredVersion?.id, workflowName, workflowRunCount]);

  useEffect(() => {
    if (!selectedVersion) return;
    setVersionId(selectedVersion.id);
    setAssignments(defaultNewWorkflowAssignments(
      selectedVersion.definition.roles,
      userId,
    ));
  }, [selectedVersion, userId]);

  const missingRequiredRole = roles.some(
    (role) => role.required &&
      !parseWorkflowAssignment(assignments[role.key] ?? ""),
  );
  const submit = () => {
    if (!workflow || !selectedVersion || !title.trim() || missingRequiredRole) {
      return;
    }
    setError("");
    run.mutate({
      title: title.trim(),
      workflow_version_id: selectedVersion.id,
      input: instructions.trim() ? { instructions: instructions.trim() } : {},
      role_assignments: roles.flatMap((role) => {
        const actor = parseWorkflowAssignment(assignments[role.key] ?? "");
        return actor
          ? [{
            role_key: role.key,
            actor_type: actor.actorType,
            actor_id: actor.actorId,
            source: "user_selected",
          }]
          : [];
      }),
      idempotency_key: crypto.randomUUID(),
    }, {
      onSuccess: (detail) => {
        onOpenChange(false);
        navigation.push(paths.workflowRun(detail.instance.id));
      },
      onError: (cause) => setError(
        cause instanceof Error ? cause.message : t(($) => $.errors.load),
      ),
    });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!run.isPending) onOpenChange(nextOpen);
      }}
    >
      <DialogContent className="max-h-[min(90vh,48rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t(($) => $.run.title)}</DialogTitle>
          <DialogDescription>{t(($) => $.run.description)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-1">
          <div className="space-y-1.5">
            <Label htmlFor="run-workflow-title">{t(($) => $.run.name)}</Label>
            <Input
              id="run-workflow-title"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              autoFocus
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="run-workflow-instructions">
              {t(($) => $.run.instructions)}
            </Label>
            <Textarea
              id="run-workflow-instructions"
              value={instructions}
              rows={3}
              placeholder={t(($) => $.run.instructions_placeholder)}
              onChange={(event) => setInstructions(event.target.value)}
            />
          </div>
          {workflowQuery.isLoading && <Skeleton className="h-28 rounded-lg" />}
          {selectedVersion && (
            <>
              <div className="space-y-1.5">
                <Label htmlFor="run-workflow-version">
                  {t(($) => $.start.version)}
                </Label>
                <select
                  id="run-workflow-version"
                  value={selectedVersion.id}
                  onChange={(event) => setVersionId(event.target.value)}
                  className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 sm:text-sm"
                >
                  {runnableVersions.map((version) => (
                    <option key={version.id} value={version.id}>
                      {t(($) => $.templates.version, { version: version.version })}
                    </option>
                  ))}
                </select>
              </div>
              <section className="space-y-2 rounded-lg border bg-muted/30 p-3">
                <h3 className="text-sm font-medium">
                  {t(($) => $.start.activity_preview)}
                </h3>
                <ol className="flex flex-wrap items-center gap-1.5">
                  {workflowPreviewActivities(selectedVersion.definition).map(
                    (node, index) => (
                      <li key={node.key} className="flex items-center gap-1.5 text-sm">
                        {index > 0 && <span aria-hidden className="text-muted-foreground">→</span>}
                        <span className="rounded-md border bg-background px-2 py-1">
                          {node.name}
                        </span>
                      </li>
                    ),
                  )}
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
                        <Label htmlFor={`run-workflow-role-${role.key}`}>
                          {role.name}{role.required && <span className="ml-1 text-destructive">*</span>}
                        </Label>
                        <select
                          id={`run-workflow-role-${role.key}`}
                          value={assignments[role.key] ?? ""}
                          required={role.required}
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
                              key={workflowAssignmentKey(actor.type, actor.id)}
                              value={workflowAssignmentKey(actor.type, actor.id)}
                            >
                              {actor.name} · {t(($) => $.start.actor_type[actor.type])}
                            </option>
                          ))}
                        </select>
                      </div>
                    );
                  })}
                </fieldset>
              )}
            </>
          )}
          {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button
            onClick={submit}
            disabled={!title.trim() || !selectedVersion || missingRequiredRole || run.isPending}
          >
            {run.isPending
              ? <Loader2 className="animate-spin motion-reduce:animate-none" />
              : <Play />}
            {t(($) => $.actions.run)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
