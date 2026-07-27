"use client";

import {
  AlertCircle,
  Archive,
  CheckCircle2,
  GitBranch,
  GitFork,
  Pencil,
  Plus,
  Save,
  Send,
  Trash2,
  Waypoints,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  useCreateWorkflowTemplateDraft,
  useArchiveWorkflowTemplate,
  usePublishWorkflowTemplate,
  useUpdateWorkflowTemplate,
  useUpdateWorkflowTemplateDraft,
  useValidateWorkflowTemplateDefinition,
  workflowTemplateOptions,
  type WorkflowDefinition,
  type WorkflowNodeDefinition,
  type WorkflowTemplate,
} from "@multica/core/workflows";
import {
  agentListOptions,
  memberListOptions,
  squadListOptions,
} from "@multica/core/workspace/queries";
import { Alert, AlertTitle } from "@multica/ui/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
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
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { CollectionPageHeader, CollectionPageState } from "../layout/collection-page";
import { useT } from "../i18n";
import { WorkflowCanvas } from "./workflow-canvas";
import {
  addWorkflowBranch,
  connectWorkflowNodes,
  type WorkflowCanvasBranchKind,
  type WorkflowCanvasEdgeTarget,
  type WorkflowCanvasInsertKind,
  insertWorkflowNodeOnEdge,
  nextWorkflowNodeKey,
  removeWorkflowEdge,
  removeWorkflowNode,
} from "./workflow-graph-editor";
import {
  WorkflowDefinitionInspector,
  WorkflowNodeDefinitionInspector,
} from "./workflow-definition-inspector";

function TemplateMetadataDialog({
  template,
}: {
  template: WorkflowTemplate;
}) {
  const { t } = useT("workflows");
  const { t: commonT } = useT("common");
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(template.name);
  const [description, setDescription] = useState(template.description);
  const [appliesToTypeKey, setAppliesToTypeKey] = useState(
    template.applies_to_type_key,
  );
  const [error, setError] = useState("");
  const update = useUpdateWorkflowTemplate(template.id);

  useEffect(() => {
    if (open) return;
    setName(template.name);
    setDescription(template.description);
    setAppliesToTypeKey(template.applies_to_type_key);
    setError("");
  }, [
    open,
    template.applies_to_type_key,
    template.description,
    template.name,
  ]);

  const submit = () => {
    const normalizedName = name.trim();
    if (!normalizedName) return;
    setError("");
    update.mutate({
      name: normalizedName,
      description: description.trim(),
      applies_to_type_key: appliesToTypeKey.trim(),
    }, {
      onSuccess: () => setOpen(false),
      onError: (cause) => setError(
        cause instanceof Error ? cause.message : t(($) => $.errors.action_failed),
      ),
    });
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button size="sm" variant="outline" />}>
        <Pencil />
        {t(($) => $.actions.edit_metadata)}
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.templates.metadata_title)}</DialogTitle>
          <DialogDescription>
            {t(($) => $.templates.metadata_description)}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="grid gap-1.5">
            <Label htmlFor="workflow-template-name">
              {t(($) => $.templates.name)}
            </Label>
            <Input
              id="workflow-template-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoFocus
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="workflow-template-description">
              {t(($) => $.templates.description)}
            </Label>
            <Textarea
              id="workflow-template-description"
              value={description}
              rows={3}
              onChange={(event) => setDescription(event.target.value)}
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="workflow-template-applies-to">
              {t(($) => $.templates.applies_to_type)}
            </Label>
            <Input
              id="workflow-template-applies-to"
              value={appliesToTypeKey}
              placeholder={t(($) => $.templates.applies_to_placeholder)}
              onChange={(event) => setAppliesToTypeKey(event.target.value)}
            />
          </div>
          {error && (
            <p className="text-sm text-destructive" role="alert">{error}</p>
          )}
        </div>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => setOpen(false)}
            disabled={update.isPending}
          >
            {commonT(($) => $.cancel)}
          </Button>
          <Button
            type="button"
            onClick={submit}
            disabled={!name.trim() || update.isPending}
          >
            {t(($) => $.actions.save_metadata)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function newActivity(definition: WorkflowDefinition): WorkflowNodeDefinition {
  const key = nextWorkflowNodeKey(definition, "activity");
  return {
    key,
    kind: "activity",
    activity_mode: "work",
    name: "New activity",
    owner_role: "owner",
    issue_policy: "fixed_and_dynamic",
    executor: {
      strategies: [
        { kind: "fixed_role", role: "owner" },
        { kind: "manual" },
      ],
    },
    issue_templates: [{
      key: `${key}_issue`,
      title: "Complete {{host.title}}",
      assignee_role: "owner",
      required: true,
      initial_status: "todo",
    }],
    completion: { mode: "automatic", required_issue_outcome: "done" },
  };
}

function newControlNode(
  definition: WorkflowDefinition,
  kind: WorkflowCanvasInsertKind | "end",
): WorkflowNodeDefinition {
  const key = nextWorkflowNodeKey(definition, kind);
  const names: Record<string, string> = {
    gateway: "Decision",
    end: "End",
  };
  return {
    key,
    kind,
    name: names[kind] ?? "Control node",
  };
}

function WorkflowEdgeRow({
  edge,
  targetName,
  readOnly,
  onChange,
  onRemove,
}: {
  edge: WorkflowDefinition["edges"][number];
  targetName: string;
  readOnly: boolean;
  onChange: (edge: WorkflowDefinition["edges"][number]) => void;
  onRemove: () => void;
}) {
  const { t } = useT("workflows");
  const [conditionText, setConditionText] = useState(
    edge.condition ? JSON.stringify(edge.condition, null, 2) : "",
  );
  const [conditionError, setConditionError] = useState("");

  useEffect(() => {
    setConditionText(edge.condition ? JSON.stringify(edge.condition, null, 2) : "");
    setConditionError("");
  }, [edge.condition]);

  const applyCondition = () => {
    const value = conditionText.trim();
    if (!value) {
      onChange({ ...edge, condition: undefined });
      setConditionError("");
      return;
    }
    try {
      const parsed = JSON.parse(value) as unknown;
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
        throw new Error("condition must be an object");
      }
      onChange({ ...edge, condition: parsed, default: false });
      setConditionError("");
    } catch {
      setConditionError(t(($) => $.errors.invalid_json));
    }
  };

  return (
    <div className="space-y-3 rounded-lg border bg-background p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-sm font-medium">{targetName}</span>
        {!readOnly && (
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            aria-label={t(($) => $.actions.remove_connection)}
            onClick={onRemove}
          >
            <Trash2 />
          </Button>
        )}
      </div>
      <label className="flex min-h-11 items-center gap-2 text-xs text-muted-foreground">
        <input
          type="checkbox"
          checked={edge.default === true}
          disabled={readOnly}
          onChange={(event) => {
            const isDefault = event.target.checked;
            onChange({
              ...edge,
              default: isDefault,
              condition: isDefault ? undefined : edge.condition,
            });
            if (isDefault) setConditionText("");
          }}
        />
        {t(($) => $.editor.default_branch)}
      </label>
      {!edge.default && (
        <div className="space-y-1.5">
          <Label>{t(($) => $.editor.edge_condition)}</Label>
          <Textarea
            value={conditionText}
            disabled={readOnly}
            onChange={(event) => setConditionText(event.target.value)}
            onBlur={applyCondition}
            rows={4}
            className="font-mono text-xs"
            placeholder={'{"source":"node_submission","node":"triage","key":"approved","op":"eq","value":true}'}
          />
          {conditionError && (
            <p role="alert" className="text-xs text-destructive">
              {conditionError}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

function WorkflowEdgeInspector({
  definition,
  nodeKey,
  readOnly,
  onChange,
}: {
  definition: WorkflowDefinition;
  nodeKey: string;
  readOnly: boolean;
  onChange: (definition: WorkflowDefinition) => void;
}) {
  const { t } = useT("workflows");
  const outgoing = definition.edges.filter((edge) => edge.from === nodeKey);
  const incoming = definition.edges.filter((edge) => edge.to === nodeKey);
  const nodeName = (key: string) =>
    definition.nodes.find((node) => node.key === key)?.name || key;

  return (
    <div className="space-y-4 border-t pt-5">
      <div>
        <h2 className="flex items-center gap-2 text-sm font-medium">
          <Waypoints className="size-4" />
          {t(($) => $.editor.connections)}
        </h2>
        {incoming.length > 0 && (
          <p className="mt-1 text-xs text-muted-foreground">
            {t(($) => $.editor.incoming_from)}:{" "}
            {incoming.map((edge) => nodeName(edge.from)).join(", ")}
          </p>
        )}
        {!readOnly && (
          <p className="mt-1 text-xs text-muted-foreground">
            {t(($) => $.editor.connections_help)}
          </p>
        )}
      </div>
      <div className="space-y-2">
        {outgoing.map((edge) => (
          <WorkflowEdgeRow
            key={`${edge.from}-${edge.to}`}
            edge={edge}
            targetName={nodeName(edge.to)}
            readOnly={readOnly}
            onChange={(next) => {
              const edges = [...definition.edges];
              const actualIndex = edges.findIndex(
                (item) => item.from === edge.from && item.to === edge.to,
              );
              if (actualIndex >= 0) edges[actualIndex] = next;
              onChange({ ...definition, edges });
            }}
            onRemove={() => onChange({
              ...definition,
              edges: definition.edges.filter(
                (item) => !(item.from === edge.from && item.to === edge.to),
              ),
            })}
          />
        ))}
        {outgoing.length === 0 && (
          <p className="rounded-lg border border-dashed px-3 py-4 text-center text-xs text-muted-foreground">
            {t(($) => $.editor.no_outgoing)}
          </p>
        )}
      </div>
    </div>
  );
}

export function WorkflowTemplatePage({ templateId }: { templateId: string }) {
  const { t } = useT("workflows");
  const { t: commonT } = useT("common");
  const wsId = useWorkspaceId();
  const isMobile = useIsMobile();
  const userId = useAuthStore((state) => state.user?.id);
  const detailQuery = useQuery(workflowTemplateOptions(wsId, templateId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const actorOptions = useMemo(() => [
    ...members.map((member) => ({
      type: "member" as const,
      id: member.user_id,
      name: member.name || member.email,
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
  const currentMember = members.find((member) => member.user_id === userId);
  const canManage = currentMember?.role === "owner" ||
    currentMember?.role === "admin";
  const versions = useMemo(
    () => detailQuery.data?.versions ?? [],
    [detailQuery.data?.versions],
  );
  const draft = versions.find((version) => version.status === "draft");
  const published = detailQuery.data?.versions.filter(
    (version) => version.status === "published",
  ) ?? [];
  const [definition, setDefinition] = useState<WorkflowDefinition | null>(null);
  const [selectedKey, setSelectedKey] = useState("");
  const [selectedVersionId, setSelectedVersionId] = useState("");
  const [loadedVersionId, setLoadedVersionId] = useState("");
  const [dirty, setDirty] = useState(false);
  const [publishOpen, setPublishOpen] = useState(false);
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [changeSummary, setChangeSummary] = useState("");
  const updateDraft = useUpdateWorkflowTemplateDraft(templateId);
  const createDraft = useCreateWorkflowTemplateDraft(templateId);
  const publish = usePublishWorkflowTemplate(templateId);
  const archive = useArchiveWorkflowTemplate(templateId);
  const validate = useValidateWorkflowTemplateDefinition(templateId);

  const selectedVersion = versions.find(
    (version) => version.id === selectedVersionId,
  );
  const canEdit = canManage && !isMobile &&
    selectedVersion?.status === "draft" &&
    detailQuery.data?.template.status !== "archived";

  useEffect(() => {
    if (versions.length === 0) return;
    if (versions.some((version) => version.id === selectedVersionId)) return;
    setSelectedVersionId(draft?.id ?? versions[0]!.id);
  }, [draft?.id, selectedVersionId, versions]);

  useEffect(() => {
    if (!selectedVersion || selectedVersion.id === loadedVersionId) return;
    setDefinition(selectedVersion.definition);
    setSelectedKey(
      selectedVersion.definition.nodes.find(
        (node) => node.kind === "activity",
      )?.key ??
      selectedVersion.definition.nodes[0]?.key ??
      "",
    );
    setLoadedVersionId(selectedVersion.id);
    setChangeSummary(selectedVersion.change_summary);
    setDirty(false);
    setSaveError("");
  }, [loadedVersionId, selectedVersion]);

  const selectedNode = definition?.nodes.find((node) => node.key === selectedKey);
  const changeDefinition = (next: WorkflowDefinition) => {
    if (!canEdit) return;
    setDefinition(next);
    setDirty(true);
    setSaveError("");
    validate.reset();
  };
  const changeNode = (next: WorkflowNodeDefinition) => {
    if (!definition || !selectedNode) return;
    let acceptance = definition.acceptance;
    if (next.kind === "activity" && next.activity_mode === "acceptance") {
      acceptance = {
        ...acceptance,
        policy: "member",
        node_key: next.key,
      };
    } else if (
      selectedNode.activity_mode === "acceptance" &&
      acceptance.node_key === selectedNode.key
    ) {
      acceptance = {
        ...acceptance,
        policy: undefined,
        node_key: undefined,
      };
    }
    changeDefinition({
      ...definition,
      nodes: definition.nodes.map((node) =>
        node.key === next.key ? next : node
      ),
      acceptance,
    });
  };
  const save = (onSaved?: () => void) => {
    if (!definition || !draft || selectedVersion?.id !== draft.id) return;
    updateDraft.mutate({
      definition,
      change_summary: changeSummary.trim(),
      revision: draft.revision,
    }, {
      onSuccess: (saved) => {
        setLoadedVersionId(saved.id);
        setSelectedVersionId(saved.id);
        setDefinition(saved.definition);
        setChangeSummary(saved.change_summary);
        setDirty(false);
        setSaveError("");
        onSaved?.();
      },
      onError: (error) => {
        setSaveError(
          error instanceof ApiError && error.status === 409
            ? t(($) => $.editor.conflict)
            : error instanceof Error ? error.message : t(($) => $.errors.load),
        );
      },
    });
  };
  // Publish is the single "make it live" action: it validates first and only
  // opens the confirmation once the definition is known to be publishable.
  const startPublish = () => {
    if (!definition) return;
    validate.mutate(definition, {
      onSuccess: (result) => {
        if (result.valid) setPublishOpen(true);
      },
    });
  };
  const confirmPublish = () => {
    const publishNow = () => publish.mutate(undefined, {
      onSuccess: () => setPublishOpen(false),
    });
    if (dirty || changeSummary.trim() !== (draft?.change_summary ?? "")) {
      save(publishNow);
      return;
    }
    publishNow();
  };
  const insertNode = (
    kind: WorkflowCanvasInsertKind,
    target: WorkflowCanvasEdgeTarget,
  ) => {
    if (!definition) return;
    const node = kind === "activity"
      ? newActivity(definition)
      : newControlNode(definition, kind);
    const next = insertWorkflowNodeOnEdge(definition, node, target);
    if (!next) return;
    changeDefinition(next);
    setSelectedKey(node.key);
  };
  const removeEdge = (target: WorkflowCanvasEdgeTarget) => {
    if (!definition) return;
    const next = removeWorkflowEdge(definition, target);
    if (next) changeDefinition(next);
  };
  const addBranch = (kind: WorkflowCanvasBranchKind, from: string) => {
    if (!definition) return;
    const node = kind === "activity"
      ? newActivity(definition)
      : newControlNode(definition, kind);
    const next = addWorkflowBranch(definition, node, from);
    if (!next) return;
    changeDefinition(next);
    setSelectedKey(node.key);
  };
  const connectNode = (target: WorkflowCanvasEdgeTarget) => {
    if (!definition) return;
    const next = connectWorkflowNodes(definition, target);
    if (next) changeDefinition(next);
  };
  const removeSelected = () => {
    if (!definition || !selectedNode || selectedNode.kind === "start") return;
    const next = removeWorkflowNode(definition, selectedNode.key);
    if (!next) return;
    changeDefinition(next);
    setSelectedKey(next.nodes[0]?.key ?? "");
  };

  if (detailQuery.isLoading) {
    return (
      <div className="space-y-4 p-5">
        <Skeleton className="h-12" />
        <Skeleton className="h-[34rem] rounded-xl" />
      </div>
    );
  }
  if (detailQuery.isError || !detailQuery.data) {
    return (
      <CollectionPageState
        icon={AlertCircle}
        title={t(($) => $.errors.not_found)}
        tone="destructive"
      />
    );
  }

  const template = detailQuery.data.template;
  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <CollectionPageHeader
        icon={GitBranch}
        title={template.name}
        description={template.description || t(($) => $.editor.description)}
        actions={canManage && !isMobile ? (
          <>
            {template.status !== "archived" && (
              <TemplateMetadataDialog template={template} />
            )}
            {draft && canEdit && (
              <>
                <span className={cn(
                  "hidden text-xs text-muted-foreground md:inline",
                  dirty && "text-amber-700 dark:text-amber-300",
                )}>
                  {dirty ? t(($) => $.editor.unsaved) : t(($) => $.editor.saved)}
                </span>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => save()}
                  disabled={!dirty || updateDraft.isPending}
                >
                  <Save />
                  {t(($) => $.actions.save)}
                </Button>
                <Button
                  size="sm"
                  onClick={startPublish}
                  disabled={publish.isPending || updateDraft.isPending ||
                    validate.isPending}
                >
                  <Send />
                  {published.length === 0
                    ? t(($) => $.actions.publish_first)
                    : t(($) => $.actions.publish)}
                </Button>
              </>
            )}
            {template.status !== "archived" && (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => setArchiveOpen(true)}
                disabled={archive.isPending}
              >
                <Archive />
                {t(($) => $.actions.archive)}
              </Button>
            )}
          </>
        ) : undefined}
      />
      {!canManage && (
        <Alert className="m-5 mb-0">
          <AlertCircle />
          <AlertTitle>{t(($) => $.templates.admin_only)}</AlertTitle>
        </Alert>
      )}
      {canManage && isMobile && (
        <Alert className="m-5 mb-0">
          <AlertCircle />
          <AlertTitle>{t(($) => $.editor.mobile_read_only)}</AlertTitle>
        </Alert>
      )}
      {template.status === "archived" && (
        <Alert className="m-5 mb-0">
          <Archive />
          <AlertTitle>{t(($) => $.templates.archived_help)}</AlertTitle>
        </Alert>
      )}
      {!draft && canManage && template.status !== "archived" && (
        <div className="m-5 flex items-center justify-between gap-4 rounded-xl border bg-muted/20 p-4">
          <div>
            <p className="text-sm font-medium">{t(($) => $.templates.published)}</p>
            <p className="mt-1 text-xs text-muted-foreground">
              {t(($) => $.editor.publish_help)}
            </p>
          </div>
          <Button
            onClick={() => createDraft.mutate(undefined, {
              onSuccess: (created) => setSelectedVersionId(created.id),
            })}
            disabled={createDraft.isPending || published.length === 0}
          >
            <Plus />
            {t(($) => $.templates.draft)}
          </Button>
        </div>
      )}
      {saveError && (
        <Alert variant="destructive" className="mx-5 mt-5">
          <AlertCircle />
          <AlertTitle>{saveError}</AlertTitle>
        </Alert>
      )}
      {validate.data && !validate.data.valid && (
        <Alert variant="destructive" className="mx-5 mt-5">
          <AlertCircle />
          <AlertTitle>{t(($) => $.editor.validation_failed)}</AlertTitle>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-sm">
            {validate.data.errors.map((error) => {
              const node = definition?.nodes.find((candidate) =>
                error.includes(`"${candidate.key}"`)
              );
              return (
                <li key={error}>
                  {node ? (
                    <button
                      type="button"
                      className="text-left underline underline-offset-2"
                      onClick={() => setSelectedKey(node.key)}
                    >
                      {error}
                    </button>
                  ) : error}
                </li>
              );
            })}
          </ul>
        </Alert>
      )}
      {validate.data?.valid && (
        <Alert className="mx-5 mt-5">
          <CheckCircle2 />
          <AlertTitle>{t(($) => $.editor.validation_passed)}</AlertTitle>
        </Alert>
      )}
      <main
        data-tab-scroll-root="workflow-template"
        className="min-h-0 flex-1 overflow-y-auto p-5"
      >
        <div className="mx-auto max-w-7xl space-y-4">
          {selectedVersion && definition ? (
            <>
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex items-center gap-2">
                  <select
                    aria-label={t(($) => $.editor.version_selector)}
                    value={selectedVersion.id}
                    onChange={(event) => setSelectedVersionId(event.target.value)}
                    className="min-h-9 rounded-lg border border-input bg-background px-3 text-sm"
                  >
                    {versions.map((version) => (
                      <option key={version.id} value={version.id}>
                        {t(($) => $.editor.version_option, {
                          version: version.version,
                          status: version.status === "draft"
                            ? t(($) => $.templates.draft)
                            : t(($) => $.templates.published),
                        })}
                      </option>
                    ))}
                  </select>
                </div>
                {canEdit && (
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => definition && validate.mutate(definition)}
                      disabled={validate.isPending}
                    >
                      <CheckCircle2 />
                      {t(($) => $.actions.validate)}
                    </Button>
                  </div>
                )}
              </div>
              <div className="grid min-h-[34rem] overflow-hidden rounded-xl border bg-surface lg:grid-cols-[minmax(0,1fr)_360px]">
                <section className="min-w-0 space-y-4 border-b p-5 lg:border-r lg:border-b-0">
                  <div>
                    <h2 className="flex items-center gap-2 text-sm font-medium">
                      <GitFork className="size-4" />
                      {t(($) => $.editor.graph)}
                    </h2>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {t(($) => $.editor.graph_help)}
                    </p>
                  </div>
                  <WorkflowCanvas
                    definition={definition}
                    nodes={[]}
                    selectedKey={selectedKey}
                    onSelectKey={setSelectedKey}
                    onInsertNode={canEdit ? insertNode : undefined}
                    onRemoveEdge={canEdit ? removeEdge : undefined}
                    onAddBranch={canEdit ? addBranch : undefined}
                    onConnectNode={canEdit ? connectNode : undefined}
                  />
                  {canEdit && selectedNode && selectedNode.kind !== "start" && (
                    <div className="flex justify-end border-t pt-4">
                      <Button
                        size="sm"
                        variant="destructive"
                        onClick={removeSelected}
                      >
                        <Trash2 />
                        {t(($) => $.actions.remove)}
                      </Button>
                    </div>
                  )}
                </section>
                <aside className="max-h-[46rem] overflow-y-auto bg-muted/10 p-5">
                  <WorkflowDefinitionInspector
                    definition={definition}
                    actorOptions={actorOptions}
                    readOnly={!canEdit}
                    onChange={changeDefinition}
                  />
                  {selectedNode && (
                    <div className="mt-5 space-y-5 border-t pt-5">
                      <WorkflowNodeDefinitionInspector
                        node={selectedNode}
                        definition={definition}
                        actorOptions={actorOptions}
                        readOnly={!canEdit}
                        onChange={changeNode}
                      />
                      <WorkflowEdgeInspector
                        definition={definition}
                        nodeKey={selectedNode.key}
                        readOnly={!canEdit}
                        onChange={changeDefinition}
                      />
                    </div>
                  )}
                </aside>
              </div>
            </>
          ) : null}
        </div>
      </main>

      <AlertDialog open={publishOpen} onOpenChange={setPublishOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.actions.publish)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.editor.version_to_publish, { version: draft?.version ?? 0 })}
              {" "}
              {t(($) => $.editor.active_runs_unchanged)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div className="space-y-1.5">
            <Label htmlFor="workflow-change-summary">
              {t(($) => $.editor.change_summary)}
            </Label>
            <Textarea
              id="workflow-change-summary"
              value={changeSummary}
              rows={2}
              placeholder={t(($) => $.editor.change_summary_placeholder)}
              onChange={(event) => setChangeSummary(event.target.value)}
            />
          </div>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={publish.isPending}>
              {commonT(($) => $.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={confirmPublish}
              disabled={publish.isPending || updateDraft.isPending ||
                (published.length > 0 && !changeSummary.trim())}
            >
              {t(($) => $.actions.publish)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <AlertDialog open={archiveOpen} onOpenChange={setArchiveOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.templates.archive_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.templates.archive_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={archive.isPending}>
              {commonT(($) => $.cancel)}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={() => archive.mutate(undefined, {
                onSuccess: () => setArchiveOpen(false),
              })}
              disabled={archive.isPending}
            >
              {t(($) => $.actions.archive)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
