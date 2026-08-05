// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import enWorkflows from "../locales/en/workflows.json";
import { GatewayRoutingPanel } from "./gateway-routing-panel";
import { readGatewayRouting } from "./gateway-routing";
import type { WorkflowEvent, WorkflowNodeDefinition } from "@multica/core/workflows";

const gateway: WorkflowNodeDefinition = {
  key: "route",
  kind: "gateway",
  name: "Triage routing",
  cases: [
    { id: "c1", label: "Not a bug", when: "triage.is_bug == false" },
    { id: "c2", label: "Urgent", when: 'triage.severity == "high"' },
    { id: "else", label: "Standard fix" },
  ],
};

const edges = [
  { from: "route", to: "close", from_case: "c1" },
  { from: "route", to: "hotfix", from_case: "c2" },
  { from: "route", to: "standard", from_case: "else" },
];

const names: Record<string, string> = {
  close: "Close it",
  hotfix: "Hotfix",
  standard: "Standard fix flow",
};

function renderPanel(event?: WorkflowEvent) {
  const routing = readGatewayRouting(
    gateway,
    edges,
    (key) => names[key] ?? key,
    event,
  );
  return render(
    <I18nProvider locale="en" resources={{ en: { workflows: enWorkflows } }}>
      <GatewayRoutingPanel routing={routing} />
    </I18nProvider>,
  );
}

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

describe("GatewayRoutingPanel", () => {
  // The branch not taken is the question people arrive with, so every case is
  // on screen with its condition — not only the winner.
  it("names where the run went and shows every case that was weighed", () => {
    renderPanel(routedEvent({
      case_id: "c2",
      case_ids: ["c2"],
      selected_targets: ["hotfix"],
      matched: { c1: false, c2: true },
      evidence: { "triage.is_bug": true, "triage.severity": "high" },
    }));

    expect(screen.getByText("Routed to Hotfix")).toBeTruthy();
    expect(screen.getByText("Not a bug")).toBeTruthy();
    expect(screen.getByText("Urgent")).toBeTruthy();
    expect(screen.getByText("Standard fix flow")).toBeTruthy();
    expect(screen.getByText("triage.is_bug == false")).toBeTruthy();
  });

  it("shows the values the conditions read", () => {
    renderPanel(routedEvent({
      case_id: "c2",
      case_ids: ["c2"],
      matched: { c1: false, c2: true },
      evidence: { "triage.is_bug": true, "triage.severity": "high" },
    }));

    expect(screen.getByText("triage.severity")).toBeTruthy();
    expect(screen.getByText("high")).toBeTruthy();
  });

  // A field nobody submitted is why a branch fell through; blank would read as
  // a rendering gap rather than the answer.
  it("says outright when a value was never submitted", () => {
    renderPanel(routedEvent({
      case_id: "else",
      case_ids: ["else"],
      matched: { c1: false, c2: false },
      evidence: { "triage.is_bug": null },
    }));

    expect(screen.getByText("Not submitted")).toBeTruthy();
  });

  it("presents the table as pending before the gateway runs", () => {
    renderPanel();

    expect(screen.getByText("This decision has not run yet.")).toBeTruthy();
    expect(screen.queryByText(/Routed to/)).toBeNull();
    // The destinations are still worth reading — that is what the node is for.
    expect(screen.getByText("Hotfix")).toBeTruthy();
  });
});
