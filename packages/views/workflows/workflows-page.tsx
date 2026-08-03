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
  workflowBuiltinTemplateListOptions,
  workflowListOptions,
  workflowOptions,
  type WorkflowDefinition,
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
  CollectionPageHeaderAction,
  CollectionPageHeader,
  CollectionPageState,
} from "../layout/collection-page";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { WorkflowStatusBadge } from "./workflow-status";
import { canManageWorkflows } from "./workflow-list";
import { workflowPreviewActivities } from "./workflow-preview";
import {
  defaultNewWorkflowAssignments,
  parseWorkflowAssignment,
  WorkflowRunDialog,
  workflowAssignmentKey,
  type WorkflowActorOption,
} from "./workflow-run-dialog";

function defaultWorkflowDefinition(): WorkflowDefinition {
  return {
    schema_version: 1,
    name: "New workflow",
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
    // Workflow-level acceptance is no longer something the editor exposes:
    // reviewing is a per-node concern, and a starter that shipped with a
    // member sign-off gate would be an approval step nobody could see or
    // turn off. Definitions that already declare one keep running.
    acceptance: {},
  };
}

type TemplateStatusFilter = "all" | "published" | "archived";

function recentRunMarkerClass(status: string): string {
  switch (status) {
    case "completed":
      return "bg-emerald-500";
    case "failed":
      return "bg-destructive";
    case "running":
    case "needs_setup":
    case "paused":
      return "bg-amber-500";
    case "cancelled":
      return "bg-muted-foreground/40";
    default:
      return "bg-sky-500";
  }
}

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

function CreateWorkflowButton() {
  const { t } = useT("workflows");
  const navigation = useNavigation();
  const p = useWorkspacePaths();
  const createTemplate = useCreateWorkflow();
  const create = () => {
    const definition = defaultWorkflowDefinition();
    createTemplate.mutate({
      name: definition.name,
      description: "",
      definition,
    }, {
      onSuccess: ({ workflow }) => navigation.push(p.workflow(workflow.id)),
    });
  };

  return (
    <CollectionPageHeaderAction
      icon={Plus}
      label={t(($) => $.actions.new_template)}
      onClick={create}
      disabled={createTemplate.isPending}
    />
  );
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
}: {
  canManage: boolean;
}) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const navigation = useNavigation();
  const { data, isLoading, isError } = useQuery(
    workflowListOptions(wsId),
  );
  const [statusFilter, setStatusFilter] = useState<TemplateStatusFilter>("all");
  const allTemplates = data?.workflows ?? [];
  const visibleTemplates = allTemplates.filter((template) =>
    matchesTemplateStatus(template, statusFilter)
  );
  const copyTemplate = useCopyWorkflow();
  const [runTemplate, setRunTemplate] = useState<Workflow | null>(null);

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
    <div>
      {isLoading ? (
        <div className="space-y-2 px-3 py-3">
          <Skeleton className="h-12 rounded-lg" />
          <Skeleton className="h-12 rounded-lg" />
        </div>
      ) : visibleTemplates.length ? (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[44rem] border-collapse text-sm">
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
                  {t(($) => $.templates.run_history)}
                </th>
                <th scope="col" className="px-3 py-2 text-right font-medium">
                  {t(($) => $.templates.column_actions)}
                </th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {/* The filters read as part of the list, not a control bar
                  hovering above it — same alignment, same rules. */}
              <tr>
                <td colSpan={5} className="px-3 py-2">
                  <div
                    aria-label={t(($) => $.filters.status)}
                    className="flex flex-wrap items-center gap-1"
                  >
                    {([
                      ["all", t(($) => $.filters.all_statuses)],
                      ["published", t(($) => $.templates.published)],
                      ["archived", t(($) => $.templates.archived)],
                    ] as Array<[TemplateStatusFilter, string]>).map(
                      ([value, label]) => (
                        <button
                          key={value}
                          type="button"
                          aria-pressed={statusFilter === value}
                          onClick={() => setStatusFilter(value)}
                          className={cn(
                            "min-h-8 rounded-md px-2.5 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring",
                            statusFilter === value
                              ? "bg-muted text-foreground"
                              : "text-muted-foreground hover:bg-muted/60",
                          )}
                        >
                          {label}
                        </button>
                      ),
                    )}
                  </div>
                </td>
              </tr>
              {visibleTemplates.map((template) => (
                <tr
                  key={template.id}
                  className="h-12 transition-colors hover:bg-muted/30"
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
                      </div>
                    </div>
                  </td>
                  <td className="px-3 py-2">
                    <WorkflowStatusBadge status={template.status} />
                  </td>
                  <td className="px-3 py-2 text-right tabular-nums">
                    {template.activity_count}
                  </td>
                  <td className="px-3 py-2">
                    <div className="flex items-center justify-end gap-2">
                      <span className="text-xs tabular-nums text-muted-foreground">
                        {template.run_count}
                      </span>
                      <div className="flex min-w-20 items-center justify-end gap-1">
                        {template.recent_runs.map((run) => (
                          <AppLink
                            key={run.id}
                            href={p.workflowRun(run.id)}
                            title={`${run.title} · ${run.status}`}
                            className={cn(
                              "size-2.5 rounded-full ring-offset-background transition-transform hover:scale-125 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
                              recentRunMarkerClass(run.status),
                            )}
                          >
                            <span className="sr-only">
                              {t(($) => $.templates.open_run, {
                                title: run.title,
                                status: run.status,
                              })}
                            </span>
                          </AppLink>
                        ))}
                      </div>
                    </div>
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
        />
      )}
      <WorkflowRunDialog
        workflow={runTemplate}
        open={Boolean(runTemplate)}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setRunTemplate(null);
        }}
      />
    </div>
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

type WorkflowPageTab = "workflows" | "builtin";

export function WorkflowsPage() {
  const { t } = useT("workflows");
  const [tab, setTab] = useState<WorkflowPageTab>("workflows");
  const wsId = useWorkspaceId();
  const userId = useAuthStore((state) => state.user?.id);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const currentMember = members.find((member) => member.user_id === userId);
  const canManage = canManageWorkflows(currentMember?.role);
  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <CollectionPageHeader
        icon={GitBranch}
        title={t(($) => $.title)}
        actions={tab === "workflows" && canManage
          ? <CreateWorkflowButton />
          : undefined}
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
            <TabsTrigger value="builtin">
              {t(($) => $.tabs.builtin)}
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </nav>
      <main
        data-tab-scroll-root="workflows"
        className="min-h-0 flex-1 overflow-y-auto"
      >
        {/* The workflow list runs edge to edge — it is the page, not a card
            centred in it. Everything else keeps the padded column. */}
        <div className={cn("w-full", tab !== "workflows" && "mx-auto max-w-7xl px-5 py-5")}>
          {tab === "builtin" ? (
            <BuiltinTemplatesPanel canManage={canManage} />
          ) : (
            <TemplatesPanel canManage={canManage} />
          )}
        </div>
      </main>
    </div>
  );
}
