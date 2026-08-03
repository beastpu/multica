"use client";

import {
  AlertCircle,
  Archive,
  ArrowLeft,
  GitBranch,
  MoreHorizontal,
  Pencil,
  Play,
  Save,
  Trash2,
  Waypoints,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  useArchiveWorkflow,
  useUpdateWorkflow,
  useSaveWorkflowDefinition,
  workflowOptions,
  type WorkflowDefinition,
  type WorkflowNodeDefinition,
  type Workflow,
  type WorkflowVersion,
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
import {
  Tabs,
  TabsList,
  TabsTrigger,
} from "@multica/ui/components/ui/tabs";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { CollectionPageState } from "../layout/collection-page";
import { useT } from "../i18n";
import { useNavigation } from "../navigation";
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
  workflowNodeIsBoundary,
} from "./workflow-graph-editor";
import {
  WorkflowNodeDefinitionInspector,
  WorkflowRoleEditor,
} from "./workflow-definition-inspector";
import { WorkflowRunDialog } from "./workflow-run-dialog";

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
  // Give a blank activity an initial executor when the template has roles.
  // Review is an explicit quality gate: defaulting it to the executor's role
  // would silently make the worker review their own output.
  const initialRole = definition.roles.find((role) => role.key === "owner")?.key ??
    definition.roles[0]?.key ?? "";
  return {
    key,
    kind: "activity",
    name: "New activity",
    // A new activity is a process step first. Producing issues is an explicit
    // opt-in, so building a flow does not fill the issue list with steps the
    // author has not decided to track as work items yet.
    issue_policy: "none",
    ...(initialRole
      ? {
        executor: {
          kind: "role" as const,
          role: initialRole,
          fallback: { kind: "manual" as const },
        },
      }
      : { executor: { kind: "manual" as const } }),
    completion: { mode: "automatic", required_issue_outcome: "none" },
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

// The editor is a canvas tool, so its sections are top-level tabs rather than
// panels stacked down a scrolling page: the graph needs the whole viewport,
// and roles describe the workflow, not the selected node.
type WorkflowEditorTab = "graph" | "roles";

export function WorkflowPage({ templateId }: { templateId: string }) {
  const { t } = useT("workflows");
  const { t: commonT } = useT("common");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
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
  const [definition, setDefinition] = useState<WorkflowDefinition | null>(null);
  const [tab, setTab] = useState<WorkflowEditorTab>("graph");
  const [selectedKey, setSelectedKey] = useState("");
  const [selectedVersionId, setSelectedVersionId] = useState("");
  const [loadedVersionId, setLoadedVersionId] = useState("");
  const [dirty, setDirty] = useState(false);
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [removeOpen, setRemoveOpen] = useState(false);
  const [metadataOpen, setMetadataOpen] = useState(false);
  const [leaveOpen, setLeaveOpen] = useState(false);
  const [runOpen, setRunOpen] = useState(false);
  const [runVersion, setRunVersion] = useState<WorkflowVersion | null>(null);
  const [saveError, setSaveError] = useState("");
  const [changeSummary, setChangeSummary] = useState("");
  const saveDefinition = useSaveWorkflowDefinition(templateId);
  const archive = useArchiveWorkflow(templateId);

  const selectedVersion = versions.find(
    (version) => version.id === selectedVersionId,
  );
  // Editing the newest version is how you author the next one — saving writes
  // a new version rather than mutating the live one. Older versions stay
  // read-only history: a run that started on one is still executing it.
  const latestVersion = [...versions]
    .sort((left, right) => right.version - left.version)[0];
  const canEdit = canManage && !isMobile &&
    selectedVersion?.id === latestVersion?.id &&
    detailQuery.data?.workflow.status !== "archived";

  useEffect(() => {
    if (versions.length === 0) return;
    if (versions.some((version) => version.id === selectedVersionId)) return;
    setSelectedVersionId(versions[0]!.id);
  }, [selectedVersionId, versions]);

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
  };
  const changeNode = (next: WorkflowNodeDefinition) => {
    if (!definition || !selectedNode || workflowNodeIsBoundary(selectedNode)) {
      return;
    }
    changeDefinition({
      ...definition,
      nodes: definition.nodes.map((node) =>
        node.key === next.key ? next : node
      ),
    });
  };
  const save = (onSaved?: (version: WorkflowVersion) => void) => {
    if (!definition || !canEdit) return;
    saveDefinition.mutate({
      // The server canonicalizes legacy owner/manual/acceptance gates before
      // writing the immutable version and returns that canonical definition.
      definition,
      change_summary: changeSummary.trim(),
    }, {
      onSuccess: (result) => {
        setLoadedVersionId(result.version.id);
        setSelectedVersionId(result.version.id);
        setDefinition(result.version.definition);
        setChangeSummary(result.version.change_summary);
        setDirty(false);
        setSaveError("");
        onSaved?.(result.version);
      },
      // A definition that does not validate is refused, so the edits are still
      // in the editor and the message names what to fix.
      onError: (error) => {
        setSaveError(
          error instanceof ApiError && error.status === 409
            ? t(($) => $.editor.conflict)
            : error instanceof Error ? error.message : t(($) => $.errors.load),
        );
      },
    });
  };
  const returnToList = () => {
    if (dirty) {
      setLeaveOpen(true);
      return;
    }
    navigation.push(paths.workflows());
  };
  const openRun = () => {
    if (!selectedVersion || detailQuery.data?.workflow.status !== "published") {
      return;
    }
    if (dirty && canEdit) {
      save((version) => {
        setRunVersion(version);
        setRunOpen(true);
      });
      return;
    }
    setRunVersion(selectedVersion);
    setRunOpen(true);
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
    setRemoveOpen(false);
    if (!definition || !selectedNode || workflowNodeIsBoundary(selectedNode)) {
      return;
    }
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
  const readOnlyNotice = !canManage
    ? t(($) => $.templates.admin_only)
    : isMobile
    ? t(($) => $.editor.mobile_read_only)
    : template.status === "archived"
    ? t(($) => $.templates.archived_help)
    : "";
  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      {/*
        One 48px bar carries what used to be three stacked rows: the page
        header, the version row, and the graph card's own heading. The canvas
        is the page here, so everything else has to earn its vertical space.
      */}
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="-ml-2"
          aria-label={t(($) => $.editor.back_to_workflows)}
          onClick={returnToList}
        >
          <ArrowLeft aria-hidden="true" />
        </Button>
        <GitBranch
          aria-hidden="true"
          className="size-4 shrink-0 text-muted-foreground"
        />
        <h1 className="max-w-[18rem] truncate text-sm font-semibold">
          {template.name}
        </h1>
        {/*
          The version is a label you glance at, not a control you reach for —
          most sessions edit the newest one. It reads as small muted text and
          only behaves as a picker when you go looking for history.
        */}
        {selectedVersion && versions.length > 0 && (
          <select
            aria-label={t(($) => $.editor.version_selector)}
            value={selectedVersion.id}
            onChange={(event) => setSelectedVersionId(event.target.value)}
            className="min-h-7 shrink-0 rounded-md border-none bg-transparent px-1 text-xs text-muted-foreground outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
          >
            {versions.map((version) => (
              <option key={version.id} value={version.id}>
                {t(($) => $.editor.version_short, { version: version.version })}
              </option>
            ))}
          </select>
        )}
        <nav aria-label={t(($) => $.editor.description)} className="mx-auto">
          <Tabs
            value={tab}
            onValueChange={(value) => setTab(value as WorkflowEditorTab)}
          >
            <TabsList variant="line" className="h-12">
              <TabsTrigger value="graph">
                {t(($) => $.editor.tab_graph)}
              </TabsTrigger>
              <TabsTrigger value="roles">
                {t(($) => $.editor.tab_roles)}
              </TabsTrigger>
            </TabsList>
          </Tabs>
        </nav>
        <div className="flex shrink-0 items-center gap-2">
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
                onClick={() => save()}
                disabled={!dirty || saveDefinition.isPending}
              >
                <Save />
                {t(($) => $.actions.save)}
              </Button>
            </>
          )}
          {template.status === "published" && selectedVersion && (
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={openRun}
              disabled={saveDefinition.isPending}
            >
              <Play aria-hidden="true" />
              {dirty
                ? t(($) => $.actions.save_and_run)
                : t(($) => $.actions.run)}
            </Button>
          )}
          {/*
            Editing the name and archiving are rare and never urgent, so
            they sit behind the overflow rather than competing with save.
          */}
          {canManage && !isMobile && template.status !== "archived" && (
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
        </div>
      </header>
      {readOnlyNotice && (
        <Alert className="mx-3 mt-3">
          {template.status === "archived" ? <Archive /> : <AlertCircle />}
          <AlertTitle>{readOnlyNotice}</AlertTitle>
        </Alert>
      )}
      {/*
        Saving validates and refuses, so this is where a bad definition
        surfaces — with the edits still in the editor. The node name in the
        message is a link: the error names a node key, and the point of
        reading it is to go fix that node.
      */}
      {saveError && (
        <Alert variant="destructive" className="mx-3 mt-3">
          <AlertCircle />
          <AlertTitle>{t(($) => $.editor.save_rejected)}</AlertTitle>
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
                      onClick={() => {
                        setTab("graph");
                        setSelectedKey(node.key);
                      }}
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
      {definition && selectedVersion && (
        tab === "graph" ? (
          <div className="flex min-h-0 flex-1">
            {/*
              The canvas takes every pixel left over instead of sitting in a
              fixed-height card inside a scrolling page. Nothing here is
              centred in a max-width column: the graph is the page.
            */}
            <section className="relative min-h-0 min-w-0 flex-1">
              <WorkflowCanvas
                definition={definition}
                nodes={[]}
                selectedKey={selectedKey}
                onSelectKey={setSelectedKey}
                onInsertNode={canEdit ? insertNode : undefined}
                onRemoveEdge={canEdit ? removeEdge : undefined}
                onAddBranch={canEdit ? addBranch : undefined}
                onConnectNode={canEdit ? connectNode : undefined}
                fill
              />
            </section>
            {/*
              The inspector belongs to the selection, the way every canvas
              editor's right panel does. Roles describe the workflow rather
              than the node, so they are a tab now — which is also what frees
              this panel of the segmented switch that used to sit on top of
              the node's own tab row looking like a second one.
            */}
            <aside
              data-tab-scroll-root="workflow-editor"
              className="w-90 shrink-0 overflow-y-auto border-l bg-background p-4"
            >
              {selectedNode ? (
                <div className="space-y-5">
                  <WorkflowNodeDefinitionInspector
                    node={selectedNode}
                    definition={definition}
                    actorOptions={actorOptions}
                    readOnly={!canEdit}
                    onChange={changeNode}
                    onRemove={canEdit && !workflowNodeIsBoundary(selectedNode)
                      ? () => setRemoveOpen(true)
                      : undefined}
                  />
                  <WorkflowEdgeInspector
                    definition={definition}
                    nodeKey={selectedNode.key}
                    readOnly={!canEdit}
                    onChange={changeDefinition}
                  />
                </div>
              ) : (
                <div className="flex h-full flex-col items-center justify-center gap-2 px-4 text-center">
                  <Waypoints
                    aria-hidden="true"
                    className="size-5 text-muted-foreground/60"
                  />
                  <p className="text-xs text-muted-foreground">
                    {t(($) => $.editor.select_node)}
                  </p>
                </div>
              )}
            </aside>
          </div>
        ) : (
          <main className="min-h-0 flex-1 overflow-y-auto p-5">
            <div className="mx-auto max-w-3xl">
              <section className="space-y-4">
                <div>
                  <h2 className="text-sm font-medium">
                    {t(($) => $.editor.workflow_roles)}
                  </h2>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {t(($) => $.editor.roles_help)}
                  </p>
                </div>
                <WorkflowRoleEditor
                  roles={definition.roles}
                  readOnly={!canEdit}
                  onChange={(roles) =>
                    changeDefinition({ ...definition, roles })}
                />
              </section>
            </div>
          </main>
        )
      )}

      {template && template.status !== "archived" && (
        <TemplateMetadataDialog
          template={template}
          open={metadataOpen}
          onOpenChange={setMetadataOpen}
        />
      )}
      <WorkflowRunDialog
        workflow={template}
        preferredVersion={runVersion}
        open={runOpen}
        onOpenChange={setRunOpen}
      />
      <AlertDialog open={leaveOpen} onOpenChange={setLeaveOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.editor.leave_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.editor.leave_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>
              {t(($) => $.editor.keep_editing)}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={() => navigation.push(paths.workflows())}
            >
              {t(($) => $.editor.discard_and_leave)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {/*
        Deleting a node also rewires the edges around it, and the editor has
        no undo — the only way back is discarding every unsaved edit. One
        confirmation is what makes the icon in the panel header safe to sit
        next to the name it destroys.
      */}
      <AlertDialog open={removeOpen} onOpenChange={setRemoveOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t(($) => $.editor.remove_node_title)}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.editor.remove_node_description, {
                node: selectedNode?.name || selectedNode?.key || "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{commonT(($) => $.cancel)}</AlertDialogCancel>
            <AlertDialogAction onClick={removeSelected}>
              {t(($) => $.actions.remove)}
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
