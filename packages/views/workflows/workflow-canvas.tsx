"use client";

import {
  CheckCircle2,
  Diamond,
  GitFork,
  Link2,
  Plus,
  PlayCircle,
  Signpost,
  Trash2,
} from "lucide-react";
import { useId, useMemo } from "react";
import type {
  WorkflowDefinition,
  WorkflowNodeInstance,
} from "@multica/core/workflows";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import {
  buildWorkflowCanvasLayout,
  workflowCanvasEdgeGeometry,
  workflowCanvasEdgeKey,
} from "./workflow-canvas-layout";
import {
  type WorkflowCanvasBranchKind,
  type WorkflowCanvasEdgeTarget,
  type WorkflowCanvasInsertKind,
  workflowConnectionTargets,
  workflowNodeCanAddOutgoing,
} from "./workflow-graph-editor";
import { WorkflowStatusDot } from "./workflow-status";

export type {
  WorkflowCanvasBranchKind,
  WorkflowCanvasEdgeTarget,
  WorkflowCanvasInsertKind,
} from "./workflow-graph-editor";

function fallbackDefinition(instances: WorkflowNodeInstance[]): WorkflowDefinition {
  const sorted = [...instances].sort((a, b) => a.display_order - b.display_order);
  return {
    schema_version: 1,
    name: "",
    applies_to: { kind: "issue" },
    roles: [],
    nodes: sorted.map((node) => node.definition),
    edges: sorted.slice(1).map((node, index) => ({
      from: sorted[index]!.node_key,
      to: node.node_key,
    })),
    acceptance: {},
  };
}

function nodeKindIcon(kind: string) {
  switch (kind) {
    case "start": return PlayCircle;
    case "end": return CheckCircle2;
    case "gateway": return Diamond;
    case "parallel_split":
    case "parallel_join":
      return GitFork;
    default:
      return Signpost;
  }
}

export function WorkflowCanvas({
  definition,
  nodes,
  selectedId,
  selectedKey,
  onSelect,
  onSelectKey,
  onInsertNode,
  onRemoveEdge,
  onAddBranch,
  onConnectNode,
  minHeight,
}: {
  definition?: WorkflowDefinition;
  nodes: WorkflowNodeInstance[];
  selectedId?: string;
  selectedKey?: string;
  onSelect?: (id: string) => void;
  onSelectKey?: (key: string) => void;
  onInsertNode?: (
    kind: WorkflowCanvasInsertKind,
    target: WorkflowCanvasEdgeTarget,
  ) => void;
  onRemoveEdge?: (target: WorkflowCanvasEdgeTarget) => void;
  onAddBranch?: (kind: WorkflowCanvasBranchKind, from: string) => void;
  onConnectNode?: (target: WorkflowCanvasEdgeTarget) => void;
  minHeight?: number;
}) {
  const { t } = useT("workflows");
  const markerId = `workflow-arrow-${useId().replaceAll(":", "")}`;
  const resolvedDefinition = definition ?? fallbackDefinition(nodes);
  const layout = useMemo(
    () => buildWorkflowCanvasLayout(resolvedDefinition, nodes, { minHeight }),
    [resolvedDefinition, nodes, minHeight],
  );
  const byKey = useMemo(
    () => new Map(layout.nodes.map((node) => [node.definition.key, node])),
    [layout.nodes],
  );

  if (layout.nodes.length === 0) {
    return (
      <div className="flex min-h-40 items-center justify-center rounded-xl border border-dashed bg-background text-sm text-muted-foreground">
        {t(($) => $.workbench.no_nodes)}
      </div>
    );
  }

  return (
    <div
      className="overflow-auto rounded-xl border bg-background shadow-xs"
      role="region"
      aria-label={t(($) => $.workbench.activity_map)}
      tabIndex={0}
    >
      <div
        className="relative"
        style={{ width: layout.width, height: layout.height }}
      >
        <svg
          aria-hidden="true"
          className="pointer-events-none absolute inset-0 size-full"
          viewBox={`0 0 ${layout.width} ${layout.height}`}
        >
          <defs>
            <marker
              id={markerId}
              markerWidth="8"
              markerHeight="8"
              refX="7"
              refY="4"
              orient="auto"
              markerUnits="strokeWidth"
            >
              <path d="M 0 0 L 8 4 L 0 8 z" className="fill-border" />
            </marker>
          </defs>
          {resolvedDefinition.edges.map((edge, index) => {
            const from = byKey.get(edge.from);
            const to = byKey.get(edge.to);
            if (!from || !to) return null;
            const geometry = workflowCanvasEdgeGeometry(
              from,
              to,
              layout.edgeLanes.get(
                workflowCanvasEdgeKey(edge.from, edge.to),
              ),
            );
            const traversed = from.instance?.status === "completed" ||
              from.instance?.status === "skipped";
            return (
              <path
                key={`${edge.from}-${edge.to}-${index}`}
                d={geometry.path}
                fill="none"
                markerEnd={`url(#${markerId})`}
                className={cn(
                  "stroke-border",
                  traversed && "stroke-blue-500/60",
                )}
                strokeWidth={traversed ? 2 : 1.5}
                strokeDasharray={edge.condition ? "5 4" : undefined}
              />
            );
          })}
        </svg>

        {(onInsertNode || onRemoveEdge) &&
          resolvedDefinition.edges.map((edge, index) => {
            const from = byKey.get(edge.from);
            const to = byKey.get(edge.to);
            if (!from || !to) return null;
            const geometry = workflowCanvasEdgeGeometry(
              from,
              to,
              layout.edgeLanes.get(
                workflowCanvasEdgeKey(edge.from, edge.to),
              ),
            );
            const fromName = from.definition.name || from.definition.key;
            const toName = to.definition.name || to.definition.key;
            const target = { from: edge.from, to: edge.to };
            const insertOptions: Array<{
              kind: WorkflowCanvasInsertKind;
              label: string;
              icon: typeof Signpost;
            }> = [
              {
                kind: "activity",
                label: t(($) => $.editor.node_activity),
                icon: Signpost,
              },
              {
                kind: "gateway",
                label: t(($) => $.editor.node_gateway),
                icon: Diamond,
              },
            ];

            return (
              <div
                key={`control-${edge.from}-${edge.to}-${index}`}
                className="absolute z-20 -translate-x-1/2 -translate-y-1/2"
                style={{
                  left: geometry.control.x,
                  top: geometry.control.y,
                }}
              >
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <button
                        type="button"
                        aria-label={t(($) => $.editor.insert_on_connection, {
                          from: fromName,
                          to: toName,
                        })}
                        className="group grid size-7 place-items-center rounded-full border border-border/80 bg-background text-muted-foreground shadow-sm outline-none transition-[transform,border-color,background-color,color,box-shadow] hover:scale-110 hover:border-brand hover:bg-brand hover:text-white hover:shadow-md focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 motion-reduce:transition-none"
                      >
                        <Plus
                          className="size-3.5 transition-transform group-data-popup-open:rotate-45 motion-reduce:transition-none"
                          aria-hidden="true"
                        />
                      </button>
                    }
                  />
                  <DropdownMenuContent
                    align="center"
                    side="bottom"
                    sideOffset={8}
                    className="w-48 p-1.5"
                  >
                    {onInsertNode && (
                      <DropdownMenuGroup>
                        <DropdownMenuLabel>
                          {t(($) => $.editor.insert_serial_node)}
                        </DropdownMenuLabel>
                        {insertOptions.map((option) => {
                          const Icon = option.icon;
                          return (
                            <DropdownMenuItem
                              key={option.kind}
                              className="min-h-9 gap-2 px-2"
                              onClick={() => onInsertNode(option.kind, target)}
                            >
                              <span className="grid size-6 place-items-center rounded-md bg-muted text-muted-foreground">
                                <Icon className="size-3.5" aria-hidden="true" />
                              </span>
                              {option.label}
                            </DropdownMenuItem>
                          );
                        })}
                      </DropdownMenuGroup>
                    )}
                    {onInsertNode && onRemoveEdge && <DropdownMenuSeparator />}
                    {onRemoveEdge && (
                      <DropdownMenuItem
                        variant="destructive"
                        className="min-h-9 gap-2 px-2"
                        onClick={() => onRemoveEdge(target)}
                      >
                        <Trash2 aria-hidden="true" />
                        {t(($) => $.actions.remove_connection)}
                      </DropdownMenuItem>
                    )}
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
            );
          })}

        <ol aria-label={t(($) => $.workbench.activity_map)}>
          {layout.nodes.map((canvasNode) => {
            const { definition: node, instance } = canvasNode;
            const selectable = Boolean(instance && onSelect) || Boolean(onSelectKey);
            const selected = instance
              ? instance.id === selectedId
              : node.key === selectedKey;
            const canAddBranch = workflowNodeCanAddOutgoing(
              resolvedDefinition,
              node.key,
            );
            const connectionTargets = canAddBranch
              ? workflowConnectionTargets(resolvedDefinition, node.key)
              : [];
            const showNodeActions = selected &&
              canAddBranch &&
              (Boolean(onAddBranch) || Boolean(onConnectNode));
            const addOptions: Array<{
              kind: WorkflowCanvasBranchKind;
              label: string;
              icon: typeof Signpost;
            }> = [
              {
                kind: "activity",
                label: t(($) => $.editor.node_activity),
                icon: Signpost,
              },
              {
                kind: "gateway",
                label: t(($) => $.editor.node_gateway),
                icon: Diamond,
              },
              {
                kind: "end",
                label: t(($) => $.editor.node_end),
                icon: CheckCircle2,
              },
            ];
            return (
              <li
                key={node.key}
                className="absolute"
                style={{
                  left: canvasNode.x,
                  top: canvasNode.y,
                  width: canvasNode.width,
                  height: canvasNode.height,
                }}
              >
                <button
                  type="button"
                  disabled={!selectable}
                  aria-pressed={selected}
                  aria-label={`${node.name || node.key}, ${instance?.status ?? node.kind}`}
                  onClick={() => {
                    if (instance && onSelect) onSelect(instance.id);
                    else onSelectKey?.(node.key);
                  }}
                  className={cn(
                    "flex size-full min-h-11 items-center gap-2 rounded-full border bg-background px-3 text-left shadow-xs outline-none transition-[border-color,box-shadow,background-color] hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 motion-reduce:transition-none",
                    selected &&
                      "border-brand bg-brand/5 ring-2 ring-brand/15",
                    !selectable && "cursor-default opacity-75",
                  )}
                >
                  <span
                    data-workflow-status={instance?.status ?? "draft"}
                    className="grid size-3 shrink-0 place-items-center"
                  >
                    <WorkflowStatusDot status={instance?.status ?? "unknown"} />
                  </span>
                  <span className="min-w-0 flex-1 truncate text-sm font-medium">
                    {node.name || node.key}
                  </span>
                </button>
                {showNodeActions && (
                  <div className="absolute top-1/2 -right-3 z-30 -translate-y-1/2">
                    <DropdownMenu>
                      <DropdownMenuTrigger
                        render={
                          <button
                            type="button"
                            aria-label={t(($) => $.editor.node_actions, {
                              node: node.name || node.key,
                            })}
                            className="grid size-7 place-items-center rounded-full border border-brand/50 bg-background text-brand shadow-sm outline-none transition-[transform,background-color,color,box-shadow] hover:scale-110 hover:bg-brand hover:text-white hover:shadow-md focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 motion-reduce:transition-none"
                          >
                            <GitFork className="size-3.5" aria-hidden="true" />
                          </button>
                        }
                      />
                      <DropdownMenuContent
                        align="start"
                        side="bottom"
                        sideOffset={8}
                        className="w-52 p-1.5"
                      >
                        <DropdownMenuGroup>
                          <DropdownMenuLabel>
                            {t(($) => $.editor.add_parallel_branch)}
                          </DropdownMenuLabel>
                          {onAddBranch && (
                            <DropdownMenuSub>
                              <DropdownMenuSubTrigger className="min-h-9 gap-2 px-2">
                                <Plus
                                  className="size-4 text-muted-foreground"
                                  aria-hidden="true"
                                />
                                {t(($) => $.actions.add_node)}
                              </DropdownMenuSubTrigger>
                              <DropdownMenuSubContent className="w-52 p-1.5">
                                {addOptions.map((option) => {
                                  const OptionIcon = option.icon;
                                  return (
                                    <DropdownMenuItem
                                      key={option.kind}
                                      className="min-h-9 gap-2 px-2"
                                      onClick={() =>
                                        onAddBranch(option.kind, node.key)}
                                    >
                                      <OptionIcon
                                        className="size-4 text-muted-foreground"
                                        aria-hidden="true"
                                      />
                                      {option.label}
                                    </DropdownMenuItem>
                                  );
                                })}
                              </DropdownMenuSubContent>
                            </DropdownMenuSub>
                          )}
                        </DropdownMenuGroup>
                        {onAddBranch && onConnectNode && (
                          <DropdownMenuSeparator />
                        )}
                        {onConnectNode && (
                          <DropdownMenuSub>
                            <DropdownMenuSubTrigger className="min-h-9 gap-2 px-2">
                              <Link2
                                className="size-4 text-muted-foreground"
                                aria-hidden="true"
                              />
                              {t(($) => $.editor.connect_to)}
                            </DropdownMenuSubTrigger>
                            <DropdownMenuSubContent className="max-h-72 w-52 overflow-y-auto p-1.5">
                              {connectionTargets.length === 0
                                ? (
                                  <DropdownMenuItem
                                    disabled
                                    className="min-h-9 px-2 text-muted-foreground"
                                  >
                                    {t(($) => $.editor.no_connection_targets)}
                                  </DropdownMenuItem>
                                )
                                : connectionTargets.map((target) => {
                                  const TargetIcon = nodeKindIcon(target.kind);
                                  return (
                                    <DropdownMenuItem
                                      key={target.key}
                                      className="min-h-9 gap-2 px-2"
                                      onClick={() => onConnectNode({
                                        from: node.key,
                                        to: target.key,
                                      })}
                                    >
                                      <TargetIcon
                                        className="size-4 text-muted-foreground"
                                        aria-hidden="true"
                                      />
                                      <span className="truncate">
                                        {target.name || target.key}
                                      </span>
                                    </DropdownMenuItem>
                                  );
                                })}
                            </DropdownMenuSubContent>
                          </DropdownMenuSub>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                )}
              </li>
            );
          })}
        </ol>
      </div>
    </div>
  );
}
