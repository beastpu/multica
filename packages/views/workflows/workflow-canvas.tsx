"use client";

import {
  CheckCircle2,
  CircleDashed,
  Diamond,
  GitFork,
  PlayCircle,
  Signpost,
} from "lucide-react";
import { useId, useMemo } from "react";
import type {
  WorkflowDefinition,
  WorkflowNodeDefinition,
  WorkflowNodeInstance,
} from "@multica/core/workflows";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { WorkflowStatusBadge } from "./workflow-status";

const CARD_WIDTH = 184;
const CARD_HEIGHT = 68;
const CONTROL_WIDTH = 148;
const COLUMN_GAP = 92;
const ROW_GAP = 28;
const CANVAS_PADDING = 32;

interface CanvasNode {
  definition: WorkflowNodeDefinition;
  instance?: WorkflowNodeInstance;
  x: number;
  y: number;
  width: number;
  height: number;
}

interface LayoutPoint {
  x: number;
  y: number;
}

function isLayoutPoint(value: unknown): value is LayoutPoint {
  if (!value || typeof value !== "object") return false;
  const point = value as Partial<LayoutPoint>;
  return Number.isFinite(point.x) && Number.isFinite(point.y);
}

function explicitLayout(definition: WorkflowDefinition) {
  if (!definition.layout || typeof definition.layout !== "object") return null;
  const nodes = (definition.layout as { nodes?: unknown }).nodes;
  if (!nodes || typeof nodes !== "object" || Array.isArray(nodes)) return null;
  return nodes as Record<string, unknown>;
}

function topologicalRanks(definition: WorkflowDefinition) {
  const nodeKeys = new Set(definition.nodes.map((node) => node.key));
  const incoming = new Map<string, string[]>();
  const outgoing = new Map<string, string[]>();
  for (const key of nodeKeys) {
    incoming.set(key, []);
    outgoing.set(key, []);
  }
  for (const edge of definition.edges) {
    if (!nodeKeys.has(edge.from) || !nodeKeys.has(edge.to)) continue;
    incoming.get(edge.to)!.push(edge.from);
    outgoing.get(edge.from)!.push(edge.to);
  }

  const rank = new Map<string, number>();
  const indegree = new Map(
    [...nodeKeys].map((key) => [key, incoming.get(key)!.length]),
  );
  const queue = [...nodeKeys].filter((key) => indegree.get(key) === 0);
  for (const key of queue) rank.set(key, 0);

  for (let index = 0; index < queue.length; index += 1) {
    const key = queue[index]!;
    for (const next of outgoing.get(key) ?? []) {
      rank.set(next, Math.max(rank.get(next) ?? 0, (rank.get(key) ?? 0) + 1));
      const remaining = (indegree.get(next) ?? 1) - 1;
      indegree.set(next, remaining);
      if (remaining === 0) queue.push(next);
    }
  }

  // A published definition is a DAG. Keeping unreachable/malformed draft
  // nodes visible makes the editor and defensive runtime rendering useful.
  let fallbackRank = Math.max(0, ...rank.values()) + 1;
  for (const node of definition.nodes) {
    if (!rank.has(node.key)) {
      rank.set(node.key, fallbackRank);
      fallbackRank += 1;
    }
  }
  return rank;
}

function buildCanvasNodes(
  definition: WorkflowDefinition,
  instances: WorkflowNodeInstance[],
) {
  const latestByKey = new Map(instances.map((node) => [node.node_key, node]));
  const ranks = topologicalRanks(definition);
  const byRank = new Map<number, WorkflowNodeDefinition[]>();
  for (const node of definition.nodes) {
    const nodeRank = ranks.get(node.key) ?? 0;
    const bucket = byRank.get(nodeRank) ?? [];
    bucket.push(node);
    byRank.set(nodeRank, bucket);
  }

  const explicit = explicitLayout(definition);
  const useExplicit = Boolean(
    explicit &&
      definition.nodes.length > 0 &&
      definition.nodes.every((node) => isLayoutPoint(explicit[node.key])),
  );
  const rows = Math.max(1, ...[...byRank.values()].map((nodes) => nodes.length));
  const gridHeight = rows * CARD_HEIGHT + (rows - 1) * ROW_GAP;

  const nodes = definition.nodes.map((node) => {
    const width = node.kind === "activity" ? CARD_WIDTH : CONTROL_WIDTH;
    const explicitPoint = explicit?.[node.key];
    if (useExplicit && isLayoutPoint(explicitPoint)) {
      return {
        definition: node,
        instance: latestByKey.get(node.key),
        x: explicitPoint.x,
        y: explicitPoint.y,
        width,
        height: CARD_HEIGHT,
      };
    }
    const rank = ranks.get(node.key) ?? 0;
    const siblings = byRank.get(rank) ?? [node];
    const row = siblings.findIndex((candidate) => candidate.key === node.key);
    const siblingsHeight =
      siblings.length * CARD_HEIGHT + (siblings.length - 1) * ROW_GAP;
    return {
      definition: node,
      instance: latestByKey.get(node.key),
      x: CANVAS_PADDING + rank * (CARD_WIDTH + COLUMN_GAP),
      y: CANVAS_PADDING + (gridHeight - siblingsHeight) / 2 +
        row * (CARD_HEIGHT + ROW_GAP),
      width,
      height: CARD_HEIGHT,
    };
  });

  const minX = Math.min(0, ...nodes.map((node) => node.x));
  const minY = Math.min(0, ...nodes.map((node) => node.y));
  if (minX < CANVAS_PADDING || minY < CANVAS_PADDING) {
    const offsetX = CANVAS_PADDING - minX;
    const offsetY = CANVAS_PADDING - minY;
    for (const node of nodes) {
      node.x += offsetX;
      node.y += offsetY;
    }
  }

  return {
    nodes,
    width: Math.max(
      680,
      ...nodes.map((node) => node.x + node.width + CANVAS_PADDING),
    ),
    height: Math.max(
      164,
      ...nodes.map((node) => node.y + node.height + CANVAS_PADDING),
    ),
  };
}

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

function edgePath(from: CanvasNode, to: CanvasNode) {
  const startX = from.x + from.width;
  const startY = from.y + from.height / 2;
  const endX = to.x;
  const endY = to.y + to.height / 2;
  const bend = Math.max(32, (endX - startX) / 2);
  return `M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`;
}

export function WorkflowCanvas({
  definition,
  nodes,
  selectedId,
  selectedKey,
  onSelect,
  onSelectKey,
}: {
  definition?: WorkflowDefinition;
  nodes: WorkflowNodeInstance[];
  selectedId?: string;
  selectedKey?: string;
  onSelect?: (id: string) => void;
  onSelectKey?: (key: string) => void;
}) {
  const { t } = useT("workflows");
  const markerId = `workflow-arrow-${useId().replaceAll(":", "")}`;
  const resolvedDefinition = definition ?? fallbackDefinition(nodes);
  const layout = useMemo(
    () => buildCanvasNodes(resolvedDefinition, nodes),
    [resolvedDefinition, nodes],
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
            const traversed = from.instance?.status === "completed" ||
              from.instance?.status === "skipped";
            return (
              <path
                key={`${edge.from}-${edge.to}-${index}`}
                d={edgePath(from, to)}
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

        <ol aria-label={t(($) => $.workbench.activity_map)}>
          {layout.nodes.map((canvasNode) => {
            const { definition: node, instance } = canvasNode;
            const Icon = nodeKindIcon(node.kind);
            const selectable = Boolean(instance && onSelect) || Boolean(onSelectKey);
            const selected = instance
              ? instance.id === selectedId
              : node.key === selectedKey;
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
                    "flex size-full min-h-11 items-center gap-2.5 rounded-xl border bg-background px-3 text-left shadow-xs outline-none transition-[border-color,box-shadow,background-color] hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 motion-reduce:transition-none",
                    node.kind !== "activity" && "rounded-full bg-muted/30",
                    selected &&
                      "border-brand bg-brand/5 ring-2 ring-brand/15",
                    !selectable && "cursor-default opacity-75",
                  )}
                >
                  <span
                    className={cn(
                      "grid size-8 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground",
                      node.kind !== "activity" && "rounded-full",
                    )}
                  >
                    <Icon className="size-4" aria-hidden="true" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">
                      {node.name || node.key}
                    </span>
                    <span className="mt-1 flex items-center gap-1.5">
                      {instance ? (
                        <WorkflowStatusBadge
                          status={instance.status}
                          className="h-5 max-w-full truncate px-1.5 text-[10px]"
                        />
                      ) : (
                        <span className="flex items-center gap-1 text-[10px] text-muted-foreground">
                          <CircleDashed className="size-3" />
                          {node.kind}
                        </span>
                      )}
                      {instance && instance.attempt > 1 && (
                        <span className="text-[10px] text-muted-foreground">
                          ×{instance.attempt}
                        </span>
                      )}
                    </span>
                  </span>
                </button>
              </li>
            );
          })}
        </ol>
      </div>
    </div>
  );
}
