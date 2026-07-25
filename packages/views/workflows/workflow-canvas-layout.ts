import type {
  WorkflowDefinition,
  WorkflowNodeDefinition,
  WorkflowNodeInstance,
} from "@multica/core/workflows";

export const WORKFLOW_CARD_WIDTH = 152;
export const WORKFLOW_CARD_HEIGHT = 44;
export const WORKFLOW_CONTROL_WIDTH = 120;
export const WORKFLOW_COLUMN_GAP = 96;
export const WORKFLOW_ROW_GAP = 40;
export const WORKFLOW_CANVAS_PADDING = 48;
export const WORKFLOW_CANVAS_MIN_WIDTH = 960;
export const WORKFLOW_CANVAS_MIN_HEIGHT = 420;

const EDGE_LANE_GAP = 22;

export interface WorkflowCanvasNode {
  definition: WorkflowNodeDefinition;
  instance?: WorkflowNodeInstance;
  rank: number;
  x: number;
  y: number;
  width: number;
  height: number;
}

interface LayoutPoint {
  x: number;
  y: number;
}

interface EdgeLane {
  side: "top" | "bottom";
  index: number;
}

export interface WorkflowCanvasLayout {
  nodes: WorkflowCanvasNode[];
  width: number;
  height: number;
  edgeLanes: Map<string, number>;
}

export interface WorkflowCanvasEdgeGeometry {
  path: string;
  control: { x: number; y: number };
  routed: boolean;
}

export function workflowCanvasEdgeKey(from: string, to: string) {
  return `${from}\u0000${to}`;
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

function graphConnections(definition: WorkflowDefinition) {
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
  return { incoming, outgoing };
}

function topologicalRanks(definition: WorkflowDefinition) {
  const { incoming, outgoing } = graphConnections(definition);
  const nodeKeys = definition.nodes.map((node) => node.key);
  const rank = new Map<string, number>();
  const indegree = new Map(
    nodeKeys.map((key) => [key, incoming.get(key)?.length ?? 0]),
  );
  const queue = nodeKeys.filter((key) => indegree.get(key) === 0);
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

  // Malformed draft nodes remain visible so the editor can repair them.
  let fallbackRank = Math.max(0, ...rank.values()) + 1;
  for (const node of definition.nodes) {
    if (!rank.has(node.key)) {
      rank.set(node.key, fallbackRank);
      fallbackRank += 1;
    }
  }
  return rank;
}

function normalizedPositions(byRank: Map<number, WorkflowNodeDefinition[]>) {
  const positions = new Map<string, number>();
  for (const nodes of byRank.values()) {
    nodes.forEach((node, index) => {
      positions.set(
        node.key,
        nodes.length === 1 ? 0.5 : index / (nodes.length - 1),
      );
    });
  }
  return positions;
}

function reduceEdgeCrossings(
  definition: WorkflowDefinition,
  byRank: Map<number, WorkflowNodeDefinition[]>,
) {
  const { incoming, outgoing } = graphConnections(definition);
  const rankKeys = [...byRank.keys()].sort((a, b) => a - b);

  const sweep = (
    ranks: number[],
    neighbours: Map<string, string[]>,
  ) => {
    const positions = normalizedPositions(byRank);
    for (const rank of ranks) {
      const bucket = byRank.get(rank);
      if (!bucket || bucket.length < 2) continue;
      bucket.sort((left, right) => {
        const barycenter = (node: WorkflowNodeDefinition) => {
          const related = neighbours.get(node.key) ?? [];
          if (related.length === 0) return positions.get(node.key) ?? 0.5;
          return related.reduce(
            (sum, key) => sum + (positions.get(key) ?? 0.5),
            0,
          ) / related.length;
        };
        const difference = barycenter(left) - barycenter(right);
        if (Math.abs(difference) > 0.0001) return difference;
        return 0;
      });
    }
  };

  for (let iteration = 0; iteration < 4; iteration += 1) {
    sweep(rankKeys.slice(1), incoming);
    sweep([...rankKeys].reverse().slice(1), outgoing);
  }
}

function assignLongEdgeLanes(
  definition: WorkflowDefinition,
  ranks: Map<string, number>,
  byRank: Map<number, WorkflowNodeDefinition[]>,
) {
  const rowPosition = normalizedPositions(byRank);
  const lanes = new Map<string, EdgeLane>();
  let topCount = 0;
  let bottomCount = 0;

  for (const edge of definition.edges) {
    const fromRank = ranks.get(edge.from);
    const toRank = ranks.get(edge.to);
    if (
      fromRank === undefined ||
      toRank === undefined ||
      toRank - fromRank <= 1
    ) {
      continue;
    }

    const verticalScore =
      ((rowPosition.get(edge.from) ?? 0.5) +
        (rowPosition.get(edge.to) ?? 0.5)) / 2;
    const side = verticalScore < 0.45
      ? "top"
      : verticalScore > 0.55
      ? "bottom"
      : topCount <= bottomCount
      ? "top"
      : "bottom";
    const index = side === "top" ? topCount++ : bottomCount++;
    lanes.set(workflowCanvasEdgeKey(edge.from, edge.to), { side, index });
  }

  return { lanes, topCount, bottomCount };
}

export function buildWorkflowCanvasLayout(
  definition: WorkflowDefinition,
  instances: WorkflowNodeInstance[],
  options: { minHeight?: number } = {},
): WorkflowCanvasLayout {
  const minHeight = options.minHeight ?? WORKFLOW_CANVAS_MIN_HEIGHT;
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
  if (!useExplicit) reduceEdgeCrossings(definition, byRank);

  const { lanes, topCount, bottomCount } = assignLongEdgeLanes(
    definition,
    ranks,
    byRank,
  );
  const rows = Math.max(1, ...[...byRank.values()].map((nodes) => nodes.length));
  const gridHeight =
    rows * WORKFLOW_CARD_HEIGHT + (rows - 1) * WORKFLOW_ROW_GAP;
  const topClearance = WORKFLOW_CANVAS_PADDING + topCount * EDGE_LANE_GAP;
  const bottomClearance =
    WORKFLOW_CANVAS_PADDING + bottomCount * EDGE_LANE_GAP;
  const centeredGridTop = topClearance + Math.max(
    0,
    (
      minHeight -
      topClearance -
      bottomClearance -
      gridHeight
    ) / 2,
  );

  const nodes = definition.nodes.map((node): WorkflowCanvasNode => {
    const width = node.kind === "activity"
      ? WORKFLOW_CARD_WIDTH
      : WORKFLOW_CONTROL_WIDTH;
    const explicitPoint = explicit?.[node.key];
    if (useExplicit && isLayoutPoint(explicitPoint)) {
      return {
        definition: node,
        instance: latestByKey.get(node.key),
        rank: ranks.get(node.key) ?? 0,
        x: explicitPoint.x,
        y: explicitPoint.y,
        width,
        height: WORKFLOW_CARD_HEIGHT,
      };
    }
    const rank = ranks.get(node.key) ?? 0;
    const siblings = byRank.get(rank) ?? [node];
    const row = siblings.findIndex((candidate) => candidate.key === node.key);
    const siblingsHeight =
      siblings.length * WORKFLOW_CARD_HEIGHT +
      (siblings.length - 1) * WORKFLOW_ROW_GAP;
    return {
      definition: node,
      instance: latestByKey.get(node.key),
      rank,
      x: WORKFLOW_CANVAS_PADDING +
        rank * (WORKFLOW_CARD_WIDTH + WORKFLOW_COLUMN_GAP),
      y: centeredGridTop +
        (gridHeight - siblingsHeight) / 2 +
        row * (WORKFLOW_CARD_HEIGHT + WORKFLOW_ROW_GAP),
      width,
      height: WORKFLOW_CARD_HEIGHT,
    };
  });

  const requiredMinY = WORKFLOW_CANVAS_PADDING + topCount * EDGE_LANE_GAP;
  if (nodes.length > 0) {
    const minX = Math.min(...nodes.map((node) => node.x));
    const minY = Math.min(...nodes.map((node) => node.y));
    const offsetX = Math.max(0, WORKFLOW_CANVAS_PADDING - minX);
    const offsetY = Math.max(0, requiredMinY - minY);
    for (const node of nodes) {
      node.x += offsetX;
      node.y += offsetY;
    }
  }

  const maxNodeBottom = Math.max(
    centeredGridTop + gridHeight,
    ...nodes.map((node) => node.y + node.height),
  );
  const edgeLanes = new Map<string, number>();
  for (const [key, lane] of lanes) {
    edgeLanes.set(
      key,
      lane.side === "top"
        ? WORKFLOW_CANVAS_PADDING / 2 + lane.index * EDGE_LANE_GAP
        : maxNodeBottom +
          WORKFLOW_CANVAS_PADDING / 2 +
          lane.index * EDGE_LANE_GAP,
    );
  }

  return {
    nodes,
    width: Math.max(
      WORKFLOW_CANVAS_MIN_WIDTH,
      ...nodes.map(
        (node) => node.x + node.width + WORKFLOW_CANVAS_PADDING,
      ),
    ),
    height: Math.max(
      minHeight,
      maxNodeBottom + bottomClearance,
    ),
    edgeLanes,
  };
}

export function workflowCanvasEdgeGeometry(
  from: WorkflowCanvasNode,
  to: WorkflowCanvasNode,
  laneY?: number,
): WorkflowCanvasEdgeGeometry {
  const startX = from.x + from.width;
  const startY = from.y + from.height / 2;
  const endX = to.x;
  const endY = to.y + to.height / 2;

  if (laneY !== undefined && endX - startX > WORKFLOW_COLUMN_GAP) {
    const corridorOffset = Math.min(
      WORKFLOW_COLUMN_GAP / 2,
      Math.max(32, (endX - startX) * 0.16),
    );
    const sourceCorridorX = startX + corridorOffset;
    const targetCorridorX = endX - corridorOffset;
    return {
      path: [
        `M ${startX} ${startY}`,
        `C ${sourceCorridorX} ${startY}, ${sourceCorridorX} ${startY}, ${sourceCorridorX} ${laneY}`,
        `L ${targetCorridorX} ${laneY}`,
        `C ${targetCorridorX} ${endY}, ${targetCorridorX} ${endY}, ${endX} ${endY}`,
      ].join(" "),
      control: {
        x: (sourceCorridorX + targetCorridorX) / 2,
        y: laneY,
      },
      routed: true,
    };
  }

  const bend = Math.max(40, (endX - startX) / 2);
  return {
    path: `M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`,
    control: {
      x: (startX + endX) / 2,
      y: (startY + endY) / 2,
    },
    routed: false,
  };
}
