"use client";

import { useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  Copy,
  GitBranch,
  LayoutTemplate,
  Loader2,
  Play,
  Plus,
  Users,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  useCreateWorkflow,
  useCreateWorkflowTemplateFromBuiltin,
  useCopyWorkflow,
  useCreateWorkflowRun,
  useRunWorkflow,
  useSaveWorkflowDefinition,
  workflowBuiltinTemplateListOptions,
  workflowListOptions,
  workflowOptions,
  type WorkflowDefinition,
  type WorkflowRoleDefinition,
  type Workflow,
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
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Tabs,
  TabsList,
  TabsTrigger,
} from "@multica/ui/components/ui/tabs";
import { AppLink, useNavigation } from "../navigation";
import {
  CollectionPageHeader,
  CollectionPageState,
} from "../layout/collection-page";
import { cn } from "@multica/ui/lib/utils";
import { useT, useTimeAgo } from "../i18n";
import { WorkflowStatusBadge } from "./workflow-status";
import { canManageWorkflows } from "./workflow-list";
import { WorkflowRoleEditor } from "./workflow-definition-inspector";
import { workflowPreviewActivities } from "./workflow-preview";

function defaultWorkflowDefinition(): WorkflowDefinition {
  return {
    schema_version: 1,
    name: "New workflow",
    applies_to: { kind: "issue" },
    roles: [{
      key: "owner",
      name: "Owner",
      required: true,
      allowed_actor_types: ["member"],
    }],
    nodes: [
      { key: "start", kind: "start", name: "Start" },
      {
        key: "work",
        kind: "activity",
        name: "Work",
        owner_role: "owner",
        executor: {
          kind: "role",
          role: "owner",
          fallback: { kind: "manual" },
        },
        issue_templates: [{
          key: "work_item",
          title: "Complete {{host.title}}",
          required: true,
          initial_status: "todo",
        }],
        completion: { mode: "automatic", required_issue_outcome: "done" },
      },
      { key: "end", kind: "end", name: "End" },
    ],
    edges: [
      { from: "start", to: "work" },
      { from: "work", to: "end" },
    ],
    acceptance: {
      policy: "member",
      approver_role: "owner",
    },
  };
}

type WorkflowActorType = "member" | "agent" | "squad";

interface WorkflowActorOption {
  type: WorkflowActorType;
  id: string;
  name: string;
}

function workflowAssignmentKey(type: WorkflowActorType, id: string) {
  return `${type}:${id}`;
}

function parseWorkflowAssignment(value: string): {
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

function defaultNewWorkflowAssignments(
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

function RunTemplateDialog({
  template,
  open,
  onOpenChange,
}: {
  template: Workflow | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const userId = useAuthStore((state) => state.user?.id);
  const p = useWorkspacePaths();
  const navigation = useNavigation();
  const templateId = template?.id ?? "";
  const [title, setTitle] = useState("");
  const [instructions, setInstructions] = useState("");
  const [versionId, setVersionId] = useState("");
  const [assignments, setAssignments] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const templateQuery = useQuery({
    ...workflowOptions(wsId, templateId),
    enabled: open && Boolean(templateId),
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
  const run = useRunWorkflow(templateId);
  const publishedVersions = useMemo(
    () => (templateQuery.data?.versions ?? [])
      .filter((version) => version.status === "published")
      .sort((left, right) => right.version - left.version),
    [templateQuery.data?.versions],
  );
  const selectedVersion = publishedVersions.find(
    (version) => version.id === versionId,
  ) ?? publishedVersions[0];
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

  useEffect(() => {
    if (!open) return;
    setTitle(template?.name ?? "");
    setInstructions("");
    setVersionId("");
    setAssignments({});
    setError("");
  }, [open, template?.id, template?.name]);

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
    if (!template || !selectedVersion || !title.trim() || missingRequiredRole) {
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
        navigation.push(p.workflowRun(detail.instance.id));
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
          {templateQuery.isLoading && <Skeleton className="h-28 rounded-lg" />}
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
                  {publishedVersions.map((version) => (
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

type TemplateStatusFilter = "all" | "published" | "draft" | "archived";

function matchesTemplateStatus(
  template: Workflow,
  filter: TemplateStatusFilter,
): boolean {
  switch (filter) {
    case "all":
      return true;
    default:
      return template.status === filter;
  }
}

// The starter library: definitions shipped with the server that a workspace
// copies to get going. Its own tab rather than a band above the list, because
// you visit it once and then never again.
function BuiltinTemplatesPanel({ canManage }: { canManage: boolean }) {
  const { t } = useT("workflows");
  const navigation = useNavigation();
  const p = useWorkspacePaths();
  const createFromBuiltin = useCreateWorkflowTemplateFromBuiltin();
  const wsId = useWorkspaceId();
  const builtinTemplates = useQuery(workflowBuiltinTemplateListOptions(wsId));

  if (builtinTemplates.isLoading) {
    return (
      <div className="grid gap-3 md:grid-cols-2">
        <Skeleton className="h-28 rounded-xl" />
        <Skeleton className="h-28 rounded-xl" />
      </div>
    );
  }
  const templates = builtinTemplates.data?.templates ?? [];
  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        {t(($) => $.templates.builtin_description)}
      </p>
      <div className="grid gap-3 md:grid-cols-2">
        {templates.map((builtin) => (
          <Card key={builtin.key} size="sm">
            <CardHeader>
              <CardTitle className="truncate">{builtin.name}</CardTitle>
              <CardDescription className="line-clamp-3">
                {builtin.description}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <Button
                size="sm"
                disabled={!canManage || createFromBuiltin.isPending}
                onClick={() => createFromBuiltin.mutate(builtin.key, {
                  onSuccess: ({ workflow }) => {
                    if (workflow.id) navigation.push(p.workflow(workflow.id));
                  },
                })}
              >
                <Plus />
                {t(($) => $.templates.builtin_create)}
              </Button>
            </CardContent>
          </Card>
        ))}
      </div>
      {createFromBuiltin.isError && (
        <p role="alert" className="text-sm text-destructive">
          {createFromBuiltin.error instanceof Error
            ? createFromBuiltin.error.message
            : t(($) => $.errors.load)}
        </p>
      )}
    </div>
  );
}

function TemplatesPanel({
  canManage,
  actorName,
}: {
  canManage: boolean;
  actorName: (actorType: string, actorId: string) => string;
}) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const navigation = useNavigation();
  const timeAgo = useTimeAgo();
  const { data, isLoading, isError } = useQuery(
    workflowListOptions(wsId),
  );
  const [statusFilter, setStatusFilter] = useState<TemplateStatusFilter>("all");
  const allTemplates = data?.workflows ?? [];
  const countFor = (filter: TemplateStatusFilter) =>
    allTemplates.filter((template) => matchesTemplateStatus(template, filter))
      .length;
  const visibleTemplates = allTemplates.filter((template) =>
    matchesTemplateStatus(template, statusFilter)
  );
  const createTemplate = useCreateWorkflow();
  const copyTemplate = useCopyWorkflow();
  const [runTemplate, setRunTemplate] = useState<Workflow | null>(null);

  const create = () => {
    const definition = defaultWorkflowDefinition();
    createTemplate.mutate({
      name: definition.name,
      description: "",
      definition,
    }, {
      onSuccess: ({ workflow }) =>
        navigation.push(p.workflow(workflow.id)),
    });
  };

  if (isError) {
    return (
      <CollectionPageState
        icon={AlertTriangle}
        title={t(($) => $.errors.load)}
        tone="destructive"
        role="alert"
      />
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-medium">{t(($) => $.templates.title)}</h2>
          {!canManage && (
            <p className="mt-1 text-xs text-muted-foreground">
              {t(($) => $.templates.admin_only)}
            </p>
          )}
        </div>
        {canManage && (
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              onClick={create}
              disabled={createTemplate.isPending}
            >
              <Plus />
              {t(($) => $.actions.new_template)}
            </Button>
          </div>
        )}
      </div>
      <div
        aria-label={t(($) => $.filters.status)}
        className="flex flex-wrap items-center gap-1"
      >
        {([
          ["all", t(($) => $.filters.all_statuses)],
          ["published", t(($) => $.templates.published)],
          ["draft", t(($) => $.templates.draft)],
          ["archived", t(($) => $.templates.archived)],
        ] as Array<[TemplateStatusFilter, string]>).map(([value, label]) => (
          <button
            key={value}
            type="button"
            aria-pressed={statusFilter === value}
            onClick={() => setStatusFilter(value)}
            className={cn(
              "min-h-9 rounded-md px-3 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring",
              statusFilter === value
                ? "bg-muted text-foreground"
                : "text-muted-foreground hover:bg-muted/60",
            )}
          >
            {label}
            <span className="ml-1.5 tabular-nums opacity-60">
              {countFor(value)}
            </span>
          </button>
        ))}
      </div>
      {isLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-12 rounded-lg" />
          <Skeleton className="h-12 rounded-lg" />
        </div>
      ) : visibleTemplates.length ? (
        <div className="overflow-x-auto rounded-lg border bg-background">
          <table className="w-full min-w-[44rem] text-sm">
            <caption className="sr-only">{t(($) => $.templates.title)}</caption>
            <thead>
              <tr className="border-b text-xs text-muted-foreground">
                <th scope="col" className="px-3 py-2 text-left font-medium">
                  {t(($) => $.templates.column_name)}
                </th>
                <th scope="col" className="px-3 py-2 text-left font-medium">
                  {t(($) => $.templates.column_status)}
                </th>
                <th scope="col" className="px-3 py-2 text-right font-medium">
                  {t(($) => $.templates.activities)}
                </th>
                <th scope="col" className="px-3 py-2 text-right font-medium">
                  {t(($) => $.templates.runs)}
                </th>
                <th scope="col" className="px-3 py-2 text-left font-medium">
                  {t(($) => $.templates.column_updated)}
                </th>
                <th scope="col" className="px-3 py-2 text-right font-medium">
                  {t(($) => $.templates.column_actions)}
                </th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {visibleTemplates.map((template) => (
                <tr
                  key={template.id}
                  className="h-16 transition-colors hover:bg-muted/30"
                >
                  <td className="max-w-[26rem] px-3 py-2">
                    <div className="flex min-w-0 items-center gap-3">
                      <GitBranch
                        aria-hidden="true"
                        className="size-4 shrink-0 text-muted-foreground"
                      />
                      <div className="min-w-0">
                        <AppLink
                          href={p.workflow(template.id)}
                          className="block truncate font-medium outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
                        >
                          {template.name}
                        </AppLink>
                        {template.description && (
                          <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                            {template.description}
                          </span>
                        )}
                      </div>
                    </div>
                  </td>
                  <td className="px-3 py-2">
                    <WorkflowStatusBadge status={template.status} />
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums">
                    {template.activity_count}
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums">
                    {template.run_count}
                  </td>
                  {/* Who is still tending this one — the fact the card grid
                      carried and the reference table has no room for. */}
                  <td className="px-3 py-2 text-xs text-muted-foreground">
                    {template.last_published_at
                      ? (
                        <span className="block truncate">
                          {timeAgo(template.last_published_at)}
                          {template.last_published_by && (
                            <>
                              <span aria-hidden="true"> · </span>
                              {actorName("member", template.last_published_by)}
                            </>
                          )}
                        </span>
                      )
                      : "—"}
                  </td>
                  <td className="px-3 py-2">
                    <div className="flex items-center justify-end gap-1">
                      {template.status === "published" && (
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={t(($) => $.actions.run)}
                          onClick={() => setRunTemplate(template)}
                        >
                          <Play />
                        </Button>
                      )}
                      {canManage && (
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={t(($) => $.actions.copy)}
                          disabled={copyTemplate.isPending}
                          onClick={() => copyTemplate.mutate({
                            templateId: template.id,
                            name: `${template.name} ${
                              t(($) => $.templates.copy_suffix)
                            }`,
                          }, {
                            onSuccess: ({ workflow: copied }) =>
                              navigation.push(p.workflow(copied.id)),
                          })}
                        >
                          <Copy />
                        </Button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <CollectionPageState
          icon={LayoutTemplate}
          title={t(($) => $.templates.empty_title)}
          description={t(($) => $.templates.empty_description)}
          actions={canManage ? (
            <div className="flex items-center gap-2">
              <Button
                onClick={create}
                disabled={createTemplate.isPending}
              >
                <Plus />
                {t(($) => $.actions.new_template)}
              </Button>
            </div>
          ) : undefined}
        />
      )}
      <RunTemplateDialog
        template={runTemplate}
        open={Boolean(runTemplate)}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setRunTemplate(null);
        }}
      />
    </div>
  );
}

function RolesPanel({ canManage }: { canManage: boolean }) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const workflowsQuery = useQuery(workflowListOptions(wsId));
  const workflows = useMemo(
    () => (workflowsQuery.data?.workflows ?? []).filter(
      (workflow) => workflow.status !== "archived",
    ),
    [workflowsQuery.data?.workflows],
  );
  const [workflowId, setWorkflowId] = useState("");
  const detailQuery = useQuery({
    ...workflowOptions(wsId, workflowId),
    enabled: Boolean(workflowId),
  });
  const versions = useMemo(
    () => [...(detailQuery.data?.versions ?? [])].sort(
      (left, right) => right.version - left.version,
    ),
    [detailQuery.data?.versions],
  );
  const latestVersion = versions.find((version) => version.status === "draft") ??
    versions.find((version) => version.status === "published") ??
    versions[0];
  const [definition, setDefinition] = useState<WorkflowDefinition | null>(null);
  const [loadedVersionId, setLoadedVersionId] = useState("");
  const [dirty, setDirty] = useState(false);
  const [saveError, setSaveError] = useState("");
  const saveDefinition = useSaveWorkflowDefinition(workflowId);
  const readOnly = !canManage ||
    detailQuery.data?.workflow.status === "archived";

  useEffect(() => {
    if (workflowId && workflows.some((workflow) => workflow.id === workflowId)) {
      return;
    }
    setWorkflowId(workflows[0]?.id ?? "");
    setLoadedVersionId("");
    setDefinition(null);
    setDirty(false);
    setSaveError("");
  }, [workflowId, workflows]);

  useEffect(() => {
    if (!latestVersion || latestVersion.id === loadedVersionId) return;
    setDefinition(latestVersion.definition);
    setLoadedVersionId(latestVersion.id);
    setDirty(false);
    setSaveError("");
  }, [latestVersion, loadedVersionId]);

  const save = () => {
    if (!definition || !latestVersion || readOnly || !dirty) return;
    setSaveError("");
    saveDefinition.mutate({
      definition,
      revision: latestVersion.revision,
    }, {
      onSuccess: (result) => {
        setDefinition(result.version.definition);
        setLoadedVersionId(result.version.id);
        setDirty(false);
        setSaveError(result.validation_error);
      },
      onError: (cause) => setSaveError(
        cause instanceof Error ? cause.message : t(($) => $.errors.load),
      ),
    });
  };

  if (workflowsQuery.isError || detailQuery.isError) {
    return (
      <CollectionPageState
        icon={AlertTriangle}
        title={t(($) => $.errors.load)}
        tone="destructive"
        role="alert"
      />
    );
  }

  if (!workflowsQuery.isLoading && workflows.length === 0) {
    return (
      <CollectionPageState
        icon={Users}
        title={t(($) => $.templates.empty_title)}
        description={t(($) => $.templates.empty_description)}
      />
    );
  }

  return (
    <section aria-labelledby="workflow-roles-title" className="space-y-5">
      <div className="flex flex-col gap-3 border-b pb-4 sm:flex-row sm:items-end sm:justify-between">
        <div className="min-w-0 flex-1 space-y-1.5">
          <h2 id="workflow-roles-title" className="text-base font-semibold">
            {t(($) => $.editor.workflow_roles)}
          </h2>
          <Label htmlFor="workflow-role-workflow" className="sr-only">
            {t(($) => $.templates.title)}
          </Label>
          {workflowsQuery.isLoading ? (
            <Skeleton className="h-10 w-full max-w-md rounded-lg" />
          ) : (
            <select
              id="workflow-role-workflow"
              value={workflowId}
              onChange={(event) => {
                setWorkflowId(event.target.value);
                setLoadedVersionId("");
                setDefinition(null);
                setDirty(false);
                setSaveError("");
              }}
              className="min-h-10 w-full max-w-md rounded-lg border border-input bg-background px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              {workflows.map((workflow) => (
                <option key={workflow.id} value={workflow.id}>
                  {workflow.name}
                </option>
              ))}
            </select>
          )}
        </div>
        {canManage && (
          <Button
            size="sm"
            onClick={save}
            disabled={!definition || !dirty || readOnly || saveDefinition.isPending}
          >
            {saveDefinition.isPending && (
              <Loader2 className="animate-spin motion-reduce:animate-none" />
            )}
            {t(($) => $.actions.save)}
          </Button>
        )}
      </div>

      {detailQuery.isLoading || !definition ? (
        <div className="grid gap-3 md:grid-cols-2">
          <Skeleton className="h-40 rounded-lg" />
          <Skeleton className="h-40 rounded-lg" />
        </div>
      ) : (
        <div className="max-w-3xl">
          <WorkflowRoleEditor
            roles={definition.roles}
            readOnly={readOnly}
            onChange={(roles) => {
              if (readOnly) return;
              setDefinition({ ...definition, roles });
              setDirty(true);
              setSaveError("");
            }}
          />
          {dirty && (
            <p className="mt-3 text-xs text-muted-foreground">
              {t(($) => $.editor.unsaved)}
            </p>
          )}
          {saveError && (
            <p role="alert" className="mt-3 text-xs text-destructive">
              {saveError}
            </p>
          )}
        </div>
      )}
    </section>
  );
}

export function NewWorkflowDialog() {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const userId = useAuthStore((state) => state.user?.id);
  const p = useWorkspacePaths();
  const navigation = useNavigation();
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [templateId, setTemplateId] = useState("");
  const [versionId, setVersionId] = useState("");
  const [hostStatusMode, setHostStatusMode] = useState<
    "managed" | "independent"
  >("managed");
  const [assignments, setAssignments] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const templatesQuery = useQuery(
    workflowListOptions(wsId, { status: "published" }),
  );
  const templateQuery = useQuery({
    ...workflowOptions(wsId, templateId),
    enabled: open && Boolean(templateId),
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
  const create = useCreateWorkflowRun();
  const templates = useMemo(
    () => (templatesQuery.data?.workflows ?? []).filter(
      (template) => template.status === "published",
    ),
    [templatesQuery.data?.workflows],
  );
  const publishedVersions = useMemo(
    () => (templateQuery.data?.versions ?? [])
      .filter((version) => version.status === "published")
      .sort((left, right) => right.version - left.version),
    [templateQuery.data?.versions],
  );
  const selectedVersion = publishedVersions.find(
    (version) => version.id === versionId,
  ) ?? publishedVersions[0];
  const roles = selectedVersion?.definition.roles ?? [];
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

  useEffect(() => {
    if (!open || templateId || templates.length === 0) return;
    setTemplateId(templates[0]!.id);
  }, [open, templateId, templates]);

  useEffect(() => {
    if (!selectedVersion) return;
    setVersionId(selectedVersion.id);
    setAssignments(defaultNewWorkflowAssignments(
      selectedVersion.definition.roles,
      userId,
    ));
  }, [selectedVersion, userId]);

  const missingRequiredRole = roles.some(
    (role) =>
      role.required &&
      !parseWorkflowAssignment(assignments[role.key] ?? ""),
  );

  const close = () => {
    setOpen(false);
    setTitle("");
    setDescription("");
    setTemplateId("");
    setVersionId("");
    setHostStatusMode("managed");
    setAssignments({});
    setError("");
  };

  const submit = () => {
    if (!title.trim() || !templateId || !selectedVersion ||
      missingRequiredRole) return;
    setError("");
    create.mutate({
      title: title.trim(),
      description: description.trim(),
      workflow_id: templateId,
      workflow_version_id: selectedVersion.id,
      host_status_mode: hostStatusMode,
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
        close();
        navigation.push(p.workflowRun(detail.instance.id));
      },
      onError: (cause) =>
        setError(cause instanceof Error ? cause.message : t(($) => $.errors.load)),
    });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) {
          setOpen(true);
          setError("");
        } else if (!create.isPending) {
          close();
        }
      }}
    >
      <DialogTrigger
        render={<Button size="sm" />}
      >
        <Plus />
        <span className="hidden md:inline">{t(($) => $.actions.new_workflow)}</span>
      </DialogTrigger>
      <DialogContent className="max-h-[min(90vh,48rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t(($) => $.create.title)}</DialogTitle>
          <DialogDescription>{t(($) => $.create.description)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-1">
          <div className="space-y-1.5">
            <Label htmlFor="new-workflow-title">
              {t(($) => $.create.issue_title)}
            </Label>
            <Input
              id="new-workflow-title"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              autoFocus
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="new-workflow-description">
              {t(($) => $.create.issue_description)}
            </Label>
            <Textarea
              id="new-workflow-description"
              value={description}
              rows={4}
              placeholder={t(($) => $.create.issue_description_placeholder)}
              onChange={(event) => setDescription(event.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="new-workflow-template">
              {t(($) => $.create.template)}
            </Label>
            <select
              id="new-workflow-template"
              value={templateId}
              onChange={(event) => {
                setTemplateId(event.target.value);
                setVersionId("");
                setAssignments({});
                setError("");
              }}
              disabled={templates.length === 0}
              className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 sm:text-sm"
            >
              <option value="">{t(($) => $.create.choose_template)}</option>
              {templates.map((template) => (
                <option key={template.id} value={template.id}>{template.name}</option>
              ))}
            </select>
            {!templatesQuery.isLoading && templates.length === 0 && (
              <p className="text-xs text-amber-700 dark:text-amber-300">
                {t(($) => $.create.no_templates)}
              </p>
            )}
          </div>
          {templateQuery.isLoading && (
            <div
              aria-label={t(($) => $.start.loading_template)}
              className="space-y-2"
            >
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
                <Label htmlFor="new-workflow-version">
                  {t(($) => $.start.version)}
                </Label>
                <select
                  id="new-workflow-version"
                  value={selectedVersion.id}
                  onChange={(event) => setVersionId(event.target.value)}
                  className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 sm:text-sm"
                >
                  {publishedVersions.map((version) => (
                    <option key={version.id} value={version.id}>
                      {t(($) => $.templates.version, {
                        version: version.version,
                      })}
                      {version.change_summary
                        ? ` · ${version.change_summary}`
                        : ""}
                    </option>
                  ))}
                </select>
              </div>

              <section
                aria-labelledby="new-workflow-preview"
                className="space-y-2 rounded-lg border bg-muted/30 p-3"
              >
                <h3 id="new-workflow-preview" className="text-sm font-medium">
                  {t(($) => $.start.activity_preview)}
                </h3>
                <ol className="flex flex-wrap items-center gap-1.5">
                  {workflowPreviewActivities(selectedVersion.definition)
                    .map((node, index) => (
                      <li
                        key={node.key}
                        className="flex items-center gap-1.5 text-sm"
                      >
                        {index > 0 && (
                          <span aria-hidden className="text-muted-foreground">
                            →
                          </span>
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
                        <Label htmlFor={`new-workflow-role-${role.key}`}>
                          {role.name}
                          {role.required && (
                            <span
                              className="ml-1 text-destructive"
                              aria-hidden
                            >
                              *
                            </span>
                          )}
                        </Label>
                        <select
                          id={`new-workflow-role-${role.key}`}
                          value={assignments[role.key] ?? ""}
                          required={role.required}
                          aria-required={role.required}
                          onChange={(event) =>
                            setAssignments((current) => ({
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
                              {actor.name} ·{" "}
                              {t(($) => $.start.actor_type[actor.type])}
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
                {(["managed", "independent"] as const).map((mode) => (
                  <label
                    key={mode}
                    className="flex min-h-11 cursor-pointer items-start gap-3 rounded-lg border p-3 has-[:checked]:border-primary"
                  >
                    <input
                      type="radio"
                      name="new-workflow-host-mode"
                      value={mode}
                      checked={hostStatusMode === mode}
                      onChange={() => setHostStatusMode(mode)}
                      className="mt-1"
                    />
                    <span>
                      <span className="block text-sm font-medium">
                        {t(($) => $.start[mode])}
                      </span>
                      <span className="block text-sm text-muted-foreground">
                        {t(($) => $.start[
                          mode === "managed"
                            ? "managed_help"
                            : "independent_help"
                        ])}
                      </span>
                    </span>
                  </label>
                ))}
              </fieldset>
            </>
          )}
          {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button
            onClick={submit}
            disabled={!title.trim() || !templateId || !selectedVersion ||
              missingRequiredRole || create.isPending}
          >
            {create.isPending
              ? <Loader2 className="animate-spin motion-reduce:animate-none" />
              : <GitBranch />}
            {t(($) => $.actions.new_workflow)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

type WorkflowPageTab = "workflows" | "roles" | "builtin";

export function WorkflowsPage() {
  const { t } = useT("workflows");
  const [tab, setTab] = useState<WorkflowPageTab>("workflows");
  const wsId = useWorkspaceId();
  const userId = useAuthStore((state) => state.user?.id);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const currentMember = members.find((member) => member.user_id === userId);
  const canManage = canManageWorkflows(currentMember?.role);
  const memberNames = useMemo(
    () => new Map(members.map((member) => [member.user_id, member.name])),
    [members],
  );
  const actorName = (_actorType: string, actorId: string) =>
    memberNames.get(actorId) ?? actorId.slice(0, 8);

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <CollectionPageHeader
        icon={GitBranch}
        title={t(($) => $.title)}
        description={t(($) => $.description)}
      />
      <nav aria-label={t(($) => $.title)} className="border-b px-5">
        <Tabs
          value={tab}
          onValueChange={(value) => setTab(value as WorkflowPageTab)}
        >
          <TabsList variant="line" className="h-10">
            <TabsTrigger value="workflows">
              {t(($) => $.tabs.templates)}
            </TabsTrigger>
            <TabsTrigger value="roles">
              {t(($) => $.tabs.roles)}
            </TabsTrigger>
            <TabsTrigger value="builtin">
              {t(($) => $.tabs.builtin)}
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </nav>
      <main
        data-tab-scroll-root="workflows"
        className="min-h-0 flex-1 overflow-y-auto px-5 py-5"
      >
        <div className="mx-auto w-full max-w-7xl">
          {tab === "builtin" ? (
            <BuiltinTemplatesPanel canManage={canManage} />
          ) : tab === "roles" ? (
            <RolesPanel canManage={canManage} />
          ) : (
            <TemplatesPanel canManage={canManage} actorName={actorName} />
          )}
        </div>
      </main>
    </div>
  );
}
