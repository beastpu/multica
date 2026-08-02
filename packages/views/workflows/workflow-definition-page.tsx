"use client";

import {
  AlertCircle,
  Archive,
  GitBranch,
  GitFork,
  MoreHorizontal,
  Pencil,
  Save,
  Trash2,
  Waypoints,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  useArchiveWorkflow,
  useUpdateWorkflow,
  useSaveWorkflowDefinition,
  workflowOptions,
  type WorkflowDefinition,
  type WorkflowNodeDefinition,
  type Workflow,
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
} from "@multica/ui/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
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
  open,
  onOpenChange,
}: {
  template: Workflow;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("workflows");
  const { t: commonT } = useT("common");
  const setOpen = onOpenChange;
  const [name, setName] = useState(template.name);
  const [description, setDescription] = useState(template.description);
  const [error, setError] = useState("");
  const update = useUpdateWorkflow(template.id);

  useEffect(() => {
    if (open) return;
    setName(template.name);
    setDescription(template.description);
    setError("");
  }, [
    open,
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
    }, {
      onSuccess: () => setOpen(false),
      onError: (cause) => setError(
        cause instanceof Error ? cause.message : t(($) => $.errors.action_failed),
      ),
    });
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
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
    name: "New activity",
    owner_role: "owner",
    // A new activity is a process step first. Producing issues is an explicit
    // opt-in, so building a flow does not fill the issue list with steps the
    // author has not decided to track as work items yet.
    issue_policy: "none",
    executor: {
      kind: "role",
      role: "owner",
      fallback: { kind: "manual" },
    },
    // Without issues there is nothing to observe, so the owner completes the
    // activity explicitly. An automatic node here would satisfy its (empty)
    // issue condition immediately and complete the moment it activates.
    completion: { mode: "manual", required_issue_outcome: "none" },
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

export function WorkflowPage({ templateId }: { templateId: string }) {
  const { t } = useT("workflows");
  const { t: commonT } = useT("common");
  const wsId = useWorkspaceId();
  const isMobile = useIsMobile();
  const userId = useAuthStore((state) => state.user?.id);
  const detailQuery = useQuery(workflowOptions(wsId, templateId));
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
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [metadataOpen, setMetadataOpen] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [changeSummary, setChangeSummary] = useState("");
  const saveDefinition = useSaveWorkflowDefinition(templateId);
  const archive = useArchiveWorkflow(templateId);

  const selectedVersion = versions.find(
    (version) => version.id === selectedVersionId,
  );
  // Editing the newest version is how you author the next one — saving writes
  // into a version rather than mutating the one that is live, so there is no
  // draft to create first. Older versions stay read-only history.
  const latestVersion = draft ?? published[0] ?? versions[0];
  const canEdit = canManage && !isMobile &&
    selectedVersion?.id === latestVersion?.id &&
    detailQuery.data?.workflow.status !== "archived";

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
  const firstNodeKey = definition?.nodes.find(
    (node) => node.kind === "activity",
  )?.key ?? definition?.nodes[0]?.key ?? "";
  const changeDefinition = (next: WorkflowDefinition) => {
    if (!canEdit) return;
    setDefinition(next);
    setDirty(true);
    setSaveError("");
  };
  const changeNode = (next: WorkflowNodeDefinition) => {
    if (!definition || !selectedNode) return;
    changeDefinition({
      ...definition,
      nodes: definition.nodes.map((node) =>
        node.key === next.key ? next : node
      ),
    });
  };
  const save = () => {
    if (!definition || !canEdit) return;
    saveDefinition.mutate({
      definition,
      change_summary: changeSummary.trim(),
      revision: selectedVersion?.revision,
    }, {
      onSuccess: (result) => {
        setLoadedVersionId(result.version.id);
        setSelectedVersionId(result.version.id);
        setDefinition(result.version.definition);
        setChangeSummary(result.version.change_summary);
        setDirty(false);
        // A definition that does not validate is still stored; it just does
        // not go live. Saying so where the errors already render beats a
        // silent save that changes nothing anyone can run.
        setSaveError(result.validation_error);
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

  const template = detailQuery.data.workflow;
  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <CollectionPageHeader
        icon={GitBranch}
        title={template.name}
        description={template.description || t(($) => $.editor.description)}
        actions={canManage && !isMobile ? (
          <>
            {canEdit && (
              <>
                <span className={cn(
                  "hidden text-xs text-muted-foreground md:inline",
                  dirty && "text-amber-700 dark:text-amber-300",
                )}>
                  {dirty ? t(($) => $.editor.unsaved) : t(($) => $.editor.saved)}
                </span>
                <Button
                  size="sm"
                  onClick={save}
                  disabled={!dirty || saveDefinition.isPending}
                >
                  <Save />
                  {t(($) => $.actions.save)}
                </Button>
              </>
            )}
            {/*
              Editing the name and archiving are rare and never urgent, so they
              sit behind the overflow rather than competing with publish. Five
              controls in a row left no visual answer to "which one ships it".
            */}
            {template.status !== "archived" && (
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-label={t(($) => $.editor.more_actions)}
                    >
                      <MoreHorizontal aria-hidden="true" />
                    </Button>
                  }
                />
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onClick={() => setMetadataOpen(true)}>
                    <Pencil />
                    {t(($) => $.actions.edit_metadata)}
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    onClick={() => setArchiveOpen(true)}
                    disabled={archive.isPending}
                  >
                    <Archive />
                    {t(($) => $.actions.archive)}
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
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
      {/*
        Saving validates, so this is where a bad definition surfaces. The node
        name in the message is a link: the error names a node key, and the
        point of reading it is to go fix that node.
      */}
      {saveError && (
        <Alert variant="destructive" className="mx-5 mt-5">
          <AlertCircle />
          <AlertTitle>{t(($) => $.editor.saved_not_live)}</AlertTitle>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-sm">
            <li>
              {(() => {
                const node = definition?.nodes.find((candidate) =>
                  saveError.includes(`"${candidate.key}"`)
                );
                return node
                  ? (
                    <button
                      type="button"
                      className="text-left underline underline-offset-2"
                      onClick={() => setSelectedKey(node.key)}
                    >
                      {saveError}
                    </button>
                  )
                  : saveError;
              })()}
            </li>
          </ul>
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
                {/*
                  The change summary is what the version list reads back, so
                  it belongs beside the version — not inside a publish dialog
                  that no longer exists.
                */}
                {canEdit && (
                  <Input
                    aria-label={t(($) => $.editor.change_summary)}
                    value={changeSummary}
                    placeholder={t(($) => $.editor.change_summary_placeholder)}
                    className="h-9 max-w-xs text-xs"
                    onChange={(event) => {
                      setChangeSummary(event.target.value);
                      setDirty(true);
                    }}
                  />
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
                {/*
                  The inspector follows the selection, the way every canvas
                  editor's does: a node when one is selected, the template's own
                  settings when none is. They used to stack, so editing a node
                  meant scrolling past two collapsed blocks of roles and
                  acceptance policy that had nothing to do with it.
                */}
                <aside className="max-h-[46rem] overflow-y-auto bg-muted/10 p-5">
                  {/*
                    A node is selected on load and the canvas has no empty
                    space to click, so the template's own settings need a door
                    of their own — otherwise moving the inspector behind the
                    selection would hide roles and acceptance for good.
                  */}
                  {/*
                    Two segments rather than one button whose label flips: a
                    single button had to be read to know what it would do, and
                    it sat directly above the node's own tab row, so it looked
                    like a selected tab in a second, unexplained tab bar. Here
                    the outer choice ("inspect what") is visibly a choice, and
                    the inner tabs stay the only tabs.
                  */}
                  <div className="mb-4 flex rounded-lg bg-muted p-0.5 text-xs font-medium">
                    <button
                      type="button"
                      onClick={() => setSelectedKey(firstNodeKey)}
                      aria-pressed={Boolean(selectedNode)}
                      className={cn(
                        "min-h-8 flex-1 rounded-md px-2 text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                        selectedNode && "bg-background text-foreground shadow-xs",
                      )}
                    >
                      {t(($) => $.editor.inspect_node)}
                    </button>
                    <button
                      type="button"
                      onClick={() => setSelectedKey("")}
                      aria-pressed={!selectedNode}
                      className={cn(
                        "min-h-8 flex-1 rounded-md px-2 text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                        !selectedNode && "bg-background text-foreground shadow-xs",
                      )}
                    >
                      {t(($) => $.editor.template_settings)}
                    </button>
                  </div>
                  {!selectedNode && (
                    <WorkflowDefinitionInspector
                      definition={definition}
                      readOnly={!canEdit}
                      onChange={changeDefinition}
                    />
                  )}
                  {selectedNode && (
                    <div className="space-y-5">
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

      {template && template.status !== "archived" && (
        <TemplateMetadataDialog
          template={template}
          open={metadataOpen}
          onOpenChange={setMetadataOpen}
        />
      )}
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
