import { describe, expect, it } from "vitest";

import {
  findGatewayRoutingEvent,
  readGatewayRouting,
} from "./gateway-routing";
import type { WorkflowEvent, WorkflowNodeDefinition } from "@multica/core/workflows";

const gateway: WorkflowNodeDefinition = {
  key: "route",
  kind: "gateway",
  name: "分诊路由",
  cases: [
    { id: "c1", label: "非缺陷", when: "triage.is_bug == false" },
    { id: "c2", label: "高危", when: 'triage.severity == "high"' },
    { id: "else", label: "标准修复" },
  ],
};

const edges = [
  { from: "route", to: "close", from_case: "c1" },
  { from: "route", to: "hotfix", from_case: "c2" },
  { from: "route", to: "standard", from_case: "else" },
];

const names: Record<string, string> = {
  close: "直接关闭",
  hotfix: "紧急修复",
  standard: "标准修复流程",
};
const label = (key: string) => names[key] ?? key;

function routedEvent(payload: Record<string, unknown>): WorkflowEvent {
  return {
    id: "e1",
    workflow_node_instance_id: "n1",
    event_type: "node.routed",
    actor_type: "system",
    actor_id: null,
    idempotency_key: "route:n1:1",
    payload,
    created_at: "2026-08-05T02:00:00Z",
  };
}

describe("readGatewayRouting", () => {
  it("shows the table and its destinations before the gateway has run", () => {
    const routing = readGatewayRouting(gateway, edges, label);

    expect(routing.decided).toBe(false);
    expect(routing.cases.map((item) => item.target)).toEqual([
      "直接关闭",
      "紧急修复",
      "标准修复流程",
    ]);
    // Nothing has been judged yet, so no case may claim it was missed.
    expect(routing.cases.every((item) => item.matched === null)).toBe(true);
    expect(routing.cases.some((item) => item.selected)).toBe(false);
  });

  it("marks the winner, the miss, and what each condition read", () => {
    const routing = readGatewayRouting(
      gateway,
      edges,
      label,
      routedEvent({
        case_id: "c2",
        case_ids: ["c2"],
        selected_targets: ["hotfix"],
        matched: { c1: false, c2: true },
        evidence: { "triage.severity": "high", "triage.is_bug": true },
      }),
    );

    expect(routing.decided).toBe(true);
    expect(routing.cases[0]).toMatchObject({ id: "c1", matched: false, selected: false });
    expect(routing.cases[1]).toMatchObject({ id: "c2", matched: true, selected: true });
    expect(routing.cases[2]).toMatchObject({ id: "else", matched: false, selected: false });
    // Sorted, so the same decision always reads the same way.
    expect(routing.evidence).toEqual([
      { path: "triage.is_bug", value: true },
      { path: "triage.severity", value: "high" },
    ]);
  });

  // A switch gateway stops at its first hit. Reporting the untouched cases as
  // "missed" would describe a judgement the run never made.
  it("leaves cases the run never reached unjudged", () => {
    const routing = readGatewayRouting(
      gateway,
      edges,
      label,
      routedEvent({
        case_id: "c1",
        case_ids: ["c1"],
        selected_targets: ["close"],
        matched: { c1: true },
        evidence: { "triage.is_bug": false },
      }),
    );

    expect(routing.cases[1]).toMatchObject({ id: "c2", matched: null });
  });

  it("takes the else path when nothing matched", () => {
    const routing = readGatewayRouting(
      gateway,
      edges,
      label,
      routedEvent({
        case_id: "else",
        case_ids: ["else"],
        selected_targets: ["standard"],
        matched: { c1: false, c2: false },
        evidence: { "triage.is_bug": true, "triage.severity": "low" },
      }),
    );

    expect(routing.cases[2]).toMatchObject({ id: "else", matched: true, selected: true });
  });

  // A field nobody submitted is exactly the case that needs explaining, so it
  // is carried as a recorded absence rather than dropped from the list.
  it("keeps a field that was absent when the gateway ran", () => {
    const routing = readGatewayRouting(
      gateway,
      edges,
      label,
      routedEvent({
        case_id: "else",
        case_ids: ["else"],
        selected_targets: ["standard"],
        matched: { c1: false, c2: false },
        evidence: { "triage.is_bug": null },
      }),
    );

    expect(routing.evidence).toEqual([{ path: "triage.is_bug", value: undefined }]);
  });

  // The payload crosses the network, so a server that stops sending a field
  // has to degrade the panel, not blank the run out.
  it("survives a payload missing everything it expects", () => {
    const routing = readGatewayRouting(gateway, edges, label, routedEvent({}));

    expect(routing.decided).toBe(true);
    expect(routing.evidence).toEqual([]);
    expect(routing.cases).toHaveLength(3);
    expect(routing.cases.every((item) => item.selected === false)).toBe(true);
  });
});

describe("findGatewayRoutingEvent", () => {
  it("picks the routing event belonging to the node", () => {
    const mine = routedEvent({ case_id: "c1" });
    const other = { ...routedEvent({ case_id: "c2" }), id: "e2", workflow_node_instance_id: "n2" };
    const unrelated = { ...routedEvent({}), id: "e3", event_type: "node.completed" };

    expect(findGatewayRoutingEvent([unrelated, other, mine], "n1")).toBe(mine);
    expect(findGatewayRoutingEvent([unrelated, other], "n1")).toBeUndefined();
  });
});
