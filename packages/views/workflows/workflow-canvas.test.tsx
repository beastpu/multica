import { fireEvent, render, screen, within } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import type {
  WorkflowDefinition,
  WorkflowNodeInstance,
} from "@multica/core/workflows";
import { describe, expect, it, vi } from "vitest";

import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { WorkflowCanvas } from "./workflow-canvas";

const RESOURCES = {
  en: { common: enCommon, workflows: enWorkflows },
};

const definition: WorkflowDefinition = {
  schema_version: 1,
  name: "Parallel delivery",
  applies_to: { kind: "issue" },
  roles: [],
  nodes: [
    { key: "start", kind: "start", name: "Start" },
    { key: "split", kind: "parallel_split", name: "Parallel work" },
    { key: "analysis", kind: "activity", name: "Analysis" },
    { key: "implementation", kind: "activity", name: "Implementation" },
    {
      key: "join",
      kind: "parallel_join",
      join_mode: "all",
      name: "Join",
    },
    { key: "end", kind: "end", name: "End" },
  ],
  edges: [
    { from: "start", to: "split" },
    { from: "split", to: "analysis" },
    { from: "split", to: "implementation" },
    { from: "analysis", to: "join" },
    { from: "implementation", to: "join" },
    { from: "join", to: "end" },
  ],
  acceptance: {},
};

function node(
  key: string,
  status: string,
  displayOrder: number,
): WorkflowNodeInstance {
  const nodeDefinition = definition.nodes.find((item) => item.key === key)!;
  return {
    id: `node-${key}`,
    workflow_instance_id: "instance-1",
    node_key: key,
    node_kind: nodeDefinition.kind,
    attempt: 1,
    name: nodeDefinition.name,
    display_order: displayOrder,
    definition: nodeDefinition,
    status,
    waiting_reasons: [],
    latest_submission_id: null,
    latest_verdict_id: null,
    activated_at: null,
    completed_at: null,
  };
}

function renderCanvas(onSelect = vi.fn()) {
  const nodes = [
    node("start", "completed", 0),
    node("split", "completed", 1),
    node("analysis", "skipped", 2),
    node("implementation", "blocked", 3),
    node("join", "pending", 4),
    node("end", "pending", 5),
  ];
  return {
    onSelect,
    ...render(
      <I18nProvider locale="en" resources={RESOURCES}>
        <WorkflowCanvas
          definition={definition}
          nodes={nodes}
          selectedId="node-implementation"
          onSelect={onSelect}
        />
      </I18nProvider>,
    ),
  };
}

describe("WorkflowCanvas", () => {
  it("renders serial, parallel, blocked, and skipped states with text labels", () => {
    renderCanvas();

    expect(
      screen.getByRole("region", { name: "Activity map" }),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("button")).toHaveLength(6);
    expect(
      screen.getByRole("button", { name: "Parallel work, completed" }),
    ).toBeInTheDocument();

    const blocked = screen.getByRole("button", {
      name: "Implementation, blocked",
    });
    expect(within(blocked).getByText("Blocked")).toBeInTheDocument();
    expect(blocked).toHaveAttribute("aria-pressed", "true");

    const skipped = screen.getByRole("button", { name: "Analysis, skipped" });
    expect(within(skipped).getByText("Skipped")).toBeInTheDocument();
  });

  it("selects a node without invoking any completion action", () => {
    const onSelect = vi.fn();
    renderCanvas(onSelect);

    fireEvent.click(screen.getByRole("button", { name: "Analysis, skipped" }));

    expect(onSelect).toHaveBeenCalledOnce();
    expect(onSelect).toHaveBeenCalledWith("node-analysis");
  });
});
