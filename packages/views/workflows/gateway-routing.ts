import type {
  WorkflowEvent,
  WorkflowNodeDefinition,
} from "@multica/core/workflows";

/** How one case of a gateway ended up. */
export interface GatewayCaseOutcome {
  id: string;
  label: string;
  /** The condition text; empty for the trailing else. */
  when: string;
  /** Where this case leads — the node's run-time name, or its key. */
  target: string;
  /**
   * `true` matched, `false` evaluated and missed, `null` never evaluated —
   * a switch gateway stops at its first hit, so the cases after it were not
   * judged at all. Collapsing that into "missed" would claim the run looked
   * at something it never read.
   */
  matched: boolean | null;
  /** This case is why the run went where it did. */
  selected: boolean;
}

/** One value a condition read, kept as of the moment the branch was taken. */
export interface GatewayEvidenceEntry {
  /** `node.field`, as written in the condition. */
  path: string;
  /** `undefined` means the field was not in the pool when the gateway ran. */
  value: unknown;
}

export interface GatewayRouting {
  /** False until the gateway has actually run. */
  decided: boolean;
  cases: GatewayCaseOutcome[];
  evidence: GatewayEvidenceEntry[];
  decidedAt: string | null;
}

/** The `node.routed` event for a node instance, if it has one yet. */
export function findGatewayRoutingEvent(
  events: WorkflowEvent[],
  nodeInstanceId: string,
): WorkflowEvent | undefined {
  return events.find(
    (event) =>
      event.event_type === "node.routed" &&
      event.workflow_node_instance_id === nodeInstanceId,
  );
}

function stringArray(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : [];
}

/**
 * Reads a gateway's routing table, merged with the outcome if it has run.
 *
 * The table comes from the definition either way, so an un-run gateway still
 * shows what it is going to decide and where each answer leads. The event
 * only adds which case won and what the conditions read — those are recorded
 * with the decision rather than re-derived from submissions, because a node
 * that has been reworked since would explain the branch with values that were
 * not there when it was taken.
 */
export function readGatewayRouting(
  definition: WorkflowNodeDefinition,
  edges: Array<{ from: string; to: string; from_case?: string }>,
  nodeLabel: (nodeKey: string) => string,
  event?: WorkflowEvent,
): GatewayRouting {
  const targets = new Map<string, string>();
  for (const edge of edges) {
    if (edge.from === definition.key && edge.from_case) {
      targets.set(edge.from_case, edge.to);
    }
  }

  const payload = event?.payload ?? {};
  const selectedIds = new Set([
    ...stringArray(payload.case_ids),
    ...(typeof payload.case_id === "string" ? [payload.case_id] : []),
  ]);
  const matchedRaw = payload.matched;
  const matched = matchedRaw !== null && typeof matchedRaw === "object"
    ? matchedRaw as Record<string, unknown>
    : {};

  const cases = (definition.cases ?? []).map((item): GatewayCaseOutcome => {
    const selected = selectedIds.has(item.id);
    const verdict = matched[item.id];
    return {
      id: item.id,
      label: item.label?.trim() || item.id,
      when: item.when ?? "",
      target: (() => {
        const key = targets.get(item.id);
        return key ? nodeLabel(key) : "";
      })(),
      // The else case is never evaluated — it is where "nothing matched"
      // goes — so its own outcome is simply whether it was taken.
      matched: item.id === "else"
        ? (event ? selected : null)
        : (typeof verdict === "boolean" ? verdict : null),
      selected,
    };
  });

  const evidenceRaw = payload.evidence;
  const evidence: GatewayEvidenceEntry[] =
    evidenceRaw !== null && typeof evidenceRaw === "object"
      ? Object.entries(evidenceRaw as Record<string, unknown>)
        .map(([path, value]) => ({ path, value: value ?? undefined }))
        .sort((a, b) => a.path.localeCompare(b.path))
      : [];

  return {
    decided: Boolean(event),
    cases,
    evidence,
    decidedAt: event?.created_at ?? null,
  };
}
