"use client";

import { useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  Archive,
  ArrowRight,
  Copy,
  GitBranch,
  LayoutTemplate,
  Loader2,
  Plus,
  Users,
  Workflow,
} from "lucide-react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { projectListOptions } from "@multica/core/projects";
import {
  useCreateWorkflowTemplate,
  useCreateWorkflowTemplateFromBuiltin,
  useCopyWorkflowTemplate,
  useCreateWorkflow,
  workflowBuiltinTemplateListOptions,
  workflowInstanceInfiniteListOptions,
  workflowTemplateListOptions,
  workflowTemplateOptions,
  type WorkflowDefinition,
  type WorkflowInstanceFilters,
  type WorkflowInstance,
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
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";
import {
  Card,
  CardAction,
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
import { useT, useTimeAgo } from "../i18n";
import { WorkflowStatusBadge } from "./workflow-status";
import { WorkflowStartDialog } from "./workflow-start-dialog";
import {
  canManageWorkflowTemplates,
  isWorkflowRunActionable,
  partitionWorkflowRuns,
  workflowStatusForTab,
  type WorkflowTab,
} from "./workflow-list";
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

function RunCard({
  run,
  actorName,
  projectName,
}: {
  run: WorkflowInstance;
  actorName: (actorType: string, actorId: string) => string;
  projectName: (projectId: string) => string;
}) {
  const { t } = useT("workflows");
  const p = useWorkspacePaths();
  const timeAgo = useTimeAgo();
  const actionable = isWorkflowRunActionable(run);

  return (
    <Card
      size="sm"
      className={actionable ? "border-amber-500/35 bg-amber-500/[0.025]" : ""}
    >
      <CardHeader>
        <CardTitle className="flex min-w-0 items-center gap-2">
          <span className="truncate">
            {run.host_issue_title || run.host_issue_identifier ||
              run.id.slice(0, 8)}
          </span>
          <WorkflowStatusBadge status={run.status} />
        </CardTitle>
        <CardDescription className="flex min-w-0 items-center gap-1.5">
          <span className="truncate font-mono text-xs">
            {run.host_issue_identifier || run.host_issue_id.slice(0, 8)}
          </span>
          <span aria-hidden="true">·</span>
          <span className="truncate">
            {run.template_name || run.template_id.slice(0, 8)}
            {run.template_version > 0 ? ` v${run.template_version}` : ""}
          </span>
        </CardDescription>
        <CardAction>
          <Button
            size="sm"
            variant={actionable ? "brandSubtle" : "ghost"}
            render={<AppLink href={p.workflowDetail(run.id)} />}
          >
            {t(($) => $.actions.open)}
            <ArrowRight />
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="space-y-3 text-xs text-muted-foreground">
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
          <span>
            {t(($) => $.runs.current_activity)} ·{" "}
            {run.current_activities.length > 0
              ? run.current_activities.map((activity) => activity.name).join(", ")
              : t(($) => $.runs.none)}
          </span>
          <span>
            {t(($) => $.runs.progress)} · {run.activity_completed}/
            {run.activity_total}
          </span>
          {run.current_owners.length > 0 && (
            <span>
              {t(($) => $.runs.current_owner)} ·{" "}
              {run.current_owners.map((owner) =>
                actorName(owner.actor_type, owner.actor_id)
              ).join(", ")}
            </span>
          )}
          {run.project_id && (
            <span>
              {t(($) => $.runs.project)} · {projectName(run.project_id)}
            </span>
          )}
          <span>
            {t(($) => $.runs.updated)} · {timeAgo(run.updated_at)}
          </span>
        </div>
        <div
          className={actionable
            ? "flex items-center gap-2 text-amber-700 dark:text-amber-300"
            : "flex items-center gap-2"}
        >
          <span className="font-medium">{t(($) => $.runs.next_action)}</span>
          <span>
            {actionable
              ? run.intervention_reason
              : t(($) => $.runs.healthy)}
          </span>
        </div>
      </CardContent>
    </Card>
  );
}

function RunList({
  runs,
  isLoading,
  actorName,
  projectName,
}: {
  runs: WorkflowInstance[];
  isLoading: boolean;
  actorName: (actorType: string, actorId: string) => string;
  projectName: (projectId: string) => string;
}) {
  const { t } = useT("workflows");
  if (isLoading) {
    return (
      <div className="grid gap-3">
        {Array.from({ length: 4 }, (_, index) => (
          <Skeleton key={index} className="h-28 rounded-xl" />
        ))}
      </div>
    );
  }
  if (runs.length === 0) {
    return (
      <CollectionPageState
        icon={Workflow}
        title={t(($) => $.runs.empty_title)}
        description={t(($) => $.runs.empty_description)}
      />
    );
  }

  const { actionable, healthy } = partitionWorkflowRuns(runs);

  return (
    <div className="space-y-7">
      {actionable.length > 0 && (
        <section className="space-y-3" aria-labelledby="workflow-interventions">
          <div>
            <h2
              id="workflow-interventions"
              className="flex items-center gap-2 text-sm font-medium"
            >
              <AlertTriangle className="size-4 text-amber-500" />
              {t(($) => $.runs.intervention_title)}
            </h2>
            <p className="mt-1 text-xs text-muted-foreground">
              {t(($) => $.runs.intervention_description)}
            </p>
          </div>
          <div className="grid gap-3">
            {actionable.map((run) => (
              <RunCard
                key={run.id}
                run={run}
                actorName={actorName}
                projectName={projectName}
              />
            ))}
          </div>
        </section>
      )}
      {healthy.length > 0 && (
        <section className="space-y-3" aria-labelledby="workflow-runs">
          <h2 id="workflow-runs" className="text-sm font-medium">
            {t(($) => $.runs.all_title)}
          </h2>
          <div className="grid gap-3">
            {healthy.map((run) => (
              <RunCard
                key={run.id}
                run={run}
                actorName={actorName}
                projectName={projectName}
              />
            ))}
          </div>
        </section>
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
    workflowTemplateListOptions(wsId),
  );
  // Archived templates are hidden by default — archiving is how this product
  // deletes, and a retired template sitting in the same grid as live ones is
  // what made "archive the old one, create a new one with the same name" look
  // like the system permitting duplicates. Retired is not gone, though, so the
  // count stays visible and one click brings them back.
  const [showArchived, setShowArchived] = useState(false);
  const allTemplates = data?.templates ?? [];
  const archivedCount = allTemplates.filter(
    (template) => template.status === "archived",
  ).length;
  const visibleTemplates = showArchived
    ? allTemplates
    : allTemplates.filter((template) => template.status !== "archived");
  const createTemplate = useCreateWorkflowTemplate();
  const copyTemplate = useCopyWorkflowTemplate();
  const createFromBuiltin = useCreateWorkflowTemplateFromBuiltin();
  const [builtinOpen, setBuiltinOpen] = useState(false);
  const builtinTemplates = useQuery({
    ...workflowBuiltinTemplateListOptions(wsId),
    enabled: builtinOpen,
  });

  const createBuiltin = (key: string) => {
    createFromBuiltin.mutate(key, {
      onSuccess: ({ template }) => {
        setBuiltinOpen(false);
        if (template.id) navigation.push(p.workflowTemplate(template.id));
      },
    });
  };

  const create = () => {
    const definition = defaultWorkflowDefinition();
    createTemplate.mutate({
      name: definition.name,
      description: "",
      definition,
    }, {
      onSuccess: ({ template }) =>
        navigation.push(p.workflowTemplate(template.id)),
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
            {/* Shown only when there is something to reveal, so the control
                does not advertise an empty state. */}
            {archivedCount > 0 && (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => setShowArchived((shown) => !shown)}
                aria-pressed={showArchived}
              >
                <Archive />
                {showArchived
                  ? t(($) => $.templates.hide_archived)
                  : t(($) => $.templates.show_archived, { count: archivedCount })}
              </Button>
            )}
            <Button
              size="sm"
              variant="outline"
              onClick={() => setBuiltinOpen(true)}
            >
              <LayoutTemplate />
              {t(($) => $.templates.use_builtin)}
            </Button>
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
      <Dialog open={builtinOpen} onOpenChange={setBuiltinOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.templates.builtin_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.templates.builtin_description)}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            {builtinTemplates.isLoading ? (
              <>
                <Skeleton className="h-16 rounded-lg" />
                <Skeleton className="h-16 rounded-lg" />
              </>
            ) : (
              (builtinTemplates.data?.templates ?? []).map((builtin) => (
                <div
                  key={builtin.key}
                  className="flex items-start justify-between gap-3 rounded-lg border p-3"
                >
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">
                      {builtin.name}
                    </p>
                    <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">
                      {builtin.description}
                    </p>
                  </div>
                  <Button
                    size="sm"
                    disabled={createFromBuiltin.isPending}
                    onClick={() => createBuiltin(builtin.key)}
                  >
                    {t(($) => $.templates.builtin_create)}
                  </Button>
                </div>
              ))
            )}
            {createFromBuiltin.isError && (
              <p className="text-xs text-destructive" role="alert">
                {createFromBuiltin.error instanceof Error
                  ? createFromBuiltin.error.message
                  : t(($) => $.errors.load)}
              </p>
            )}
          </div>
        </DialogContent>
      </Dialog>
      {isLoading ? (
        <div className="grid gap-3 md:grid-cols-2">
          <Skeleton className="h-32 rounded-xl" />
          <Skeleton className="h-32 rounded-xl" />
        </div>
      ) : visibleTemplates.length ? (
        <div className="grid gap-3 md:grid-cols-2">
          {visibleTemplates.map((template) => (
            <Card key={template.id} size="sm">
              <CardHeader>
                <CardTitle className="truncate">{template.name}</CardTitle>
                <CardDescription className="line-clamp-2 min-h-8">
                  {template.description || t(($) => $.editor.description)}
                </CardDescription>
                <CardAction>
                  <WorkflowStatusBadge status={template.status} />
                </CardAction>
              </CardHeader>
              <CardContent className="space-y-3">
                <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
                  <div>
                    <dt className="text-muted-foreground">
                      {t(($) => $.templates.applies_to)}
                    </dt>
                    <dd>{template.applies_to_type_key ||
                      template.applies_to_kind}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">
                      {t(($) => $.templates.latest_version)}
                    </dt>
                    <dd>
                      {template.latest_published_version > 0
                        ? `v${template.latest_published_version}`
                        : t(($) => $.runs.none)}
                      {template.has_draft
                        ? ` · ${t(($) => $.templates.draft)} v${template.draft_version}`
                        : ""}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">
                      {t(($) => $.templates.activities)}
                    </dt>
                    <dd>{template.activity_count}</dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">
                      {t(($) => $.templates.runs)}
                    </dt>
                    <dd>{template.run_count}</dd>
                  </div>
                </dl>
                {(template.latest_change_summary ||
                  template.last_published_at) && (
                  <p className="line-clamp-2 text-xs text-muted-foreground">
                    {template.latest_change_summary}
                    {template.last_published_by && (
                      <>
                        <span aria-hidden="true"> · </span>
                        {actorName("member", template.last_published_by)}
                      </>
                    )}
                    {template.last_published_at && (
                      <>
                        <span aria-hidden="true"> · </span>
                        {timeAgo(template.last_published_at)}
                      </>
                    )}
                  </p>
                )}
                <div className="flex flex-wrap gap-2">
                  <Button
                    size="sm"
                    variant="outline"
                    render={<AppLink href={p.workflowTemplate(template.id)} />}
                  >
                    {t(($) => $.actions.open)}
                    <ArrowRight />
                  </Button>
                  {canManage && (
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={copyTemplate.isPending}
                      onClick={() => copyTemplate.mutate({
                        templateId: template.id,
                        name: `${template.name} ${t(($) => $.templates.copy_suffix)}`,
                      }, {
                        onSuccess: ({ template: copied }) =>
                          navigation.push(p.workflowTemplate(copied.id)),
                      })}
                    >
                      <Copy />
                      {t(($) => $.actions.copy)}
                    </Button>
                  )}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      ) : (
        <CollectionPageState
          icon={LayoutTemplate}
          title={t(($) => $.templates.empty_title)}
          description={t(($) => $.templates.empty_description)}
          actions={canManage ? (
            <div className="flex items-center gap-2">
              <Button onClick={() => setBuiltinOpen(true)}>
                <LayoutTemplate />
                {t(($) => $.templates.use_builtin)}
              </Button>
              <Button
                variant="outline"
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
    workflowTemplateListOptions(wsId, { status: "published" }),
  );
  const templateQuery = useQuery({
    ...workflowTemplateOptions(wsId, templateId),
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
  const create = useCreateWorkflow();
  const templates = templatesQuery.data?.templates.filter(
    (template) => template.status === "published",
  ) ?? [];
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
      template_id: templateId,
      template_version_id: selectedVersion.id,
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
        navigation.push(p.workflowDetail(detail.instance.id));
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

export function WorkflowsPage() {
  const { t } = useT("workflows");
  const [tab, setTab] = useState<WorkflowTab>("active");
  const [statusFilter, setStatusFilter] = useState("");
  const [projectId, setProjectId] = useState("");
  const [templateId, setTemplateId] = useState("");
  const [currentNodeKey, setCurrentNodeKey] = useState("");
  const [ownerValue, setOwnerValue] = useState("");
  const wsId = useWorkspaceId();
  const userId = useAuthStore((state) => state.user?.id);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const { data: templateData } = useQuery(workflowTemplateListOptions(wsId));
  const currentMember = members.find((member) => member.user_id === userId);
  const canManage = canManageWorkflowTemplates(currentMember?.role);
  useEffect(() => {
    if (tab === "templates" && currentMember && !canManage) {
      setTab("active");
    }
  }, [canManage, currentMember, tab]);
  const actorNames = useMemo(() => {
    const names = new Map<string, string>();
    for (const member of members) {
      names.set(`member:${member.user_id}`, member.name);
    }
    for (const agent of agents) {
      names.set(`agent:${agent.id}`, agent.name);
    }
    for (const squad of squads) {
      names.set(`squad:${squad.id}`, squad.name);
    }
    return names;
  }, [agents, members, squads]);
  const actorName = (actorType: string, actorId: string) =>
    actorNames.get(`${actorType}:${actorId}`) ?? actorId.slice(0, 8);
  const projectNames = useMemo(
    () => new Map(projects.map((project) => [project.id, project.title])),
    [projects],
  );
  const projectName = (id: string) =>
    projectNames.get(id) ?? id.slice(0, 8);
  const filters = useMemo<WorkflowInstanceFilters>(() => {
    const [ownerType, ownerId] = ownerValue.split(":", 2) as [
      "member" | "agent" | "squad" | "",
      string | undefined,
    ];
    return {
      status: workflowStatusForTab(tab, statusFilter),
      related_to_me: tab === "mine" || undefined,
      project_id: projectId || undefined,
      template_id: templateId || undefined,
      current_node_key: currentNodeKey.trim() || undefined,
      owner_type: ownerType || undefined,
      owner_id: ownerId || undefined,
      limit: 100,
    };
  }, [
    currentNodeKey,
    ownerValue,
    projectId,
    statusFilter,
    tab,
    templateId,
  ]);
  const runsQuery = useInfiniteQuery({
    ...workflowInstanceInfiniteListOptions(wsId, filters),
    enabled: tab !== "templates",
  });
  const visibleRuns = useMemo(() => {
    const runs = runsQuery.data?.pages.flatMap((page) => page.instances) ?? [];
    if (tab === "active" || tab === "mine") {
      return runs.filter((run) =>
        run.status !== "completed" && run.status !== "cancelled"
      );
    }
    return runs;
  }, [runsQuery.data?.pages, tab]);
  const runCount = runsQuery.data?.pages[0]?.total ?? visibleRuns.length;

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <CollectionPageHeader
        icon={GitBranch}
        title={t(($) => $.title)}
        count={tab === "templates" ? undefined : runCount}
        description={t(($) => $.description)}
        actions={tab !== "templates" ? (
          <>
            <WorkflowStartDialog />
            <NewWorkflowDialog />
          </>
        ) : undefined}
      />
      <div className="border-b px-5">
        <Tabs
          value={tab}
          onValueChange={(value) => {
            setTab(value as WorkflowTab);
            setStatusFilter("");
          }}
        >
          <TabsList variant="line" className="h-10">
            <TabsTrigger value="active">{t(($) => $.tabs.active)}</TabsTrigger>
            <TabsTrigger value="mine">{t(($) => $.tabs.mine)}</TabsTrigger>
            <TabsTrigger value="completed">{t(($) => $.tabs.completed)}</TabsTrigger>
            {canManage && (
              <TabsTrigger value="templates">
                {t(($) => $.tabs.templates)}
              </TabsTrigger>
            )}
          </TabsList>
        </Tabs>
      </div>
      <main
        data-tab-scroll-root="workflows"
        className="min-h-0 flex-1 overflow-y-auto px-5 py-5"
      >
        <div className="mx-auto w-full max-w-5xl">
          {tab === "templates" && canManage ? (
            <TemplatesPanel canManage={canManage} actorName={actorName} />
          ) : runsQuery.isError ? (
            <CollectionPageState
              icon={AlertTriangle}
              title={t(($) => $.errors.load)}
              tone="destructive"
              role="alert"
            />
          ) : (
            <div className="space-y-5">
              <div
                className="grid gap-2 rounded-xl border bg-muted/15 p-3 sm:grid-cols-2 lg:grid-cols-5"
                aria-label={t(($) => $.filters.title)}
              >
                <select
                  aria-label={t(($) => $.filters.status)}
                  value={statusFilter}
                  onChange={(event) => setStatusFilter(event.target.value)}
                  className="min-h-11 rounded-lg border border-input bg-background px-3 text-sm"
                >
                  <option value="">{t(($) => $.filters.all_statuses)}</option>
                  {tab === "completed" ? (
                    <>
                      <option value="completed">
                        {t(($) => $.status.completed)}
                      </option>
                      <option value="cancelled">
                        {t(($) => $.status.cancelled)}
                      </option>
                    </>
                  ) : (
                    <>
                      <option value="running">{t(($) => $.status.running)}</option>
                      <option value="needs_setup">
                        {t(($) => $.status.needs_setup)}
                      </option>
                      <option value="paused">{t(($) => $.status.paused)}</option>
                      <option value="failed">{t(($) => $.status.failed)}</option>
                    </>
                  )}
                </select>
                <select
                  aria-label={t(($) => $.filters.project)}
                  value={projectId}
                  onChange={(event) => setProjectId(event.target.value)}
                  className="min-h-11 rounded-lg border border-input bg-background px-3 text-sm"
                >
                  <option value="">{t(($) => $.filters.all_projects)}</option>
                  {projects.map((project) => (
                    <option key={project.id} value={project.id}>{project.title}</option>
                  ))}
                </select>
                <select
                  aria-label={t(($) => $.filters.template)}
                  value={templateId}
                  onChange={(event) => setTemplateId(event.target.value)}
                  className="min-h-11 rounded-lg border border-input bg-background px-3 text-sm"
                >
                  <option value="">{t(($) => $.filters.all_templates)}</option>
                  {(templateData?.templates ?? []).map((template) => (
                    <option key={template.id} value={template.id}>
                      {template.name}
                    </option>
                  ))}
                </select>
                <select
                  aria-label={t(($) => $.filters.owner)}
                  value={ownerValue}
                  onChange={(event) => setOwnerValue(event.target.value)}
                  className="min-h-11 rounded-lg border border-input bg-background px-3 text-sm"
                >
                  <option value="">{t(($) => $.filters.all_owners)}</option>
                  {members.map((member) => (
                    <option
                      key={`member:${member.user_id}`}
                      value={`member:${member.user_id}`}
                    >
                      {member.name}
                    </option>
                  ))}
                  {agents.filter((agent) => !agent.archived_at).map((agent) => (
                    <option key={`agent:${agent.id}`} value={`agent:${agent.id}`}>
                      {agent.name}
                    </option>
                  ))}
                  {squads.filter((squad) => !squad.archived_at).map((squad) => (
                    <option key={`squad:${squad.id}`} value={`squad:${squad.id}`}>
                      {squad.name}
                    </option>
                  ))}
                </select>
                <Input
                  aria-label={t(($) => $.filters.current_node)}
                  placeholder={t(($) => $.filters.current_node)}
                  value={currentNodeKey}
                  onChange={(event) => setCurrentNodeKey(event.target.value)}
                  className="min-h-11"
                />
              </div>
              <RunList
                runs={visibleRuns}
                isLoading={runsQuery.isLoading}
                actorName={actorName}
                projectName={projectName}
              />
              {runsQuery.hasNextPage && (
                <div className="flex justify-center pt-1">
                  <Button
                    variant="outline"
                    onClick={() => runsQuery.fetchNextPage()}
                    disabled={runsQuery.isFetchingNextPage}
                  >
                    {runsQuery.isFetchingNextPage
                      ? t(($) => $.actions.loading_more)
                      : t(($) => $.actions.load_more)}
                  </Button>
                </div>
              )}
            </div>
          )}
        </div>
      </main>
    </div>
  );
}
