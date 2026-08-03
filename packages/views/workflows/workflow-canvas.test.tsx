import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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
  it("renders compact nodes with status dots and accessible status labels", () => {
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
    expect(
      blocked.querySelector('[data-workflow-status="blocked"]'),
    ).toBeInTheDocument();
    expect(within(blocked).queryByText("Blocked")).not.toBeInTheDocument();
    expect(blocked).toHaveAttribute("aria-pressed", "true");

    const skipped = screen.getByRole("button", { name: "Analysis, skipped" });
    expect(
      skipped.querySelector('[data-workflow-status="skipped"]'),
    ).toBeInTheDocument();
    expect(within(skipped).queryByText("Skipped")).not.toBeInTheDocument();
  });

  it("selects a node without invoking any completion action", () => {
    const onSelect = vi.fn();
    renderCanvas(onSelect);

    fireEvent.click(screen.getByRole("button", { name: "Analysis, skipped" }));

    expect(onSelect).toHaveBeenCalledOnce();
    expect(onSelect).toHaveBeenCalledWith("node-analysis");
  });

  it("inserts or removes a connection directly from its visual control", async () => {
    const user = userEvent.setup();
    const onInsertNode = vi.fn();
    const onRemoveEdge = vi.fn();
    const nodes = [
      node("start", "completed", 0),
      node("split", "completed", 1),
      node("analysis", "skipped", 2),
      node("implementation", "blocked", 3),
      node("join", "pending", 4),
      node("end", "pending", 5),
    ];
    render(
      <I18nProvider locale="en" resources={RESOURCES}>
        <WorkflowCanvas
          definition={definition}
          nodes={nodes}
          onInsertNode={onInsertNode}
          onRemoveEdge={onRemoveEdge}
        />
      </I18nProvider>,
    );

    const connectionControl = screen.getByRole("button", {
      name: "Insert a node between Start and Parallel work",
    });
    await user.click(connectionControl);
    expect(await screen.findByText("Insert in series")).toBeInTheDocument();
    expect(
      screen.queryByRole("menuitem", { name: "Parallel split" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("menuitem", { name: "Parallel join" }),
    ).not.toBeInTheDocument();
    await user.click(await screen.findByRole("menuitem", { name: "Activity" }));

    expect(onInsertNode).toHaveBeenCalledWith("activity", {
      from: "start",
      to: "split",
    });

    await user.click(connectionControl);
    await user.click(await screen.findByRole("menuitem", {
      name: "Remove connection",
    }));
    expect(onRemoveEdge).toHaveBeenCalledWith({
      from: "start",
      to: "split",
    });
  });

  it("adds a branch or connects the selected node to a valid existing node", async () => {
    const user = userEvent.setup();
    const onAddBranch = vi.fn();
    const onConnectNode = vi.fn();
    const nodes = [
      node("start", "completed", 0),
      node("split", "completed", 1),
      node("analysis", "skipped", 2),
      node("implementation", "blocked", 3),
      node("join", "pending", 4),
      node("end", "pending", 5),
    ];
    render(
      <I18nProvider locale="en" resources={RESOURCES}>
        <WorkflowCanvas
          definition={definition}
          nodes={nodes}
          selectedId="node-analysis"
          onSelect={vi.fn()}
          onAddBranch={onAddBranch}
          onConnectNode={onConnectNode}
        />
      </I18nProvider>,
    );

    const actions = screen.getByRole("button", {
      name: "Actions for Analysis",
    });
    await user.click(actions);
    await user.click(await screen.findByRole("menuitem", { name: "Add node" }));
    expect(
      screen.queryByRole("menuitem", { name: "Parallel split" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("menuitem", { name: "Parallel join" }),
    ).not.toBeInTheDocument();
    fireEvent.click(await screen.findByRole("menuitem", { name: "Activity" }));
    expect(onAddBranch).toHaveBeenCalledWith("activity", "analysis");

    await user.click(actions);
    const connectTo = await screen.findByRole("menuitem", {
      name: "Connect to",
    });
    await user.click(connectTo);
    fireEvent.click(await screen.findByRole("menuitem", {
      name: "Implementation",
    }));
    expect(onConnectNode).toHaveBeenCalledWith({
      from: "analysis",
      to: "implementation",
    });
  });

  it("keeps Connect to visible and explains when no legal target exists", async () => {
    const user = userEvent.setup();
    const nodes = [
      node("start", "completed", 0),
      node("split", "completed", 1),
      node("analysis", "skipped", 2),
      node("implementation", "blocked", 3),
      node("join", "pending", 4),
      node("end", "pending", 5),
    ];
    render(
      <I18nProvider locale="en" resources={RESOURCES}>
        <WorkflowCanvas
          definition={definition}
          nodes={nodes}
          selectedId="node-start"
          onSelect={vi.fn()}
          onConnectNode={vi.fn()}
        />
      </I18nProvider>,
    );

    await user.click(screen.getByRole("button", {
      name: "Actions for Start",
    }));
    await user.click(await screen.findByRole("menuitem", {
      name: "Connect to",
    }));

    expect(await screen.findByText("No available nodes")).toBeInTheDocument();
  });

  it("offers a parallel branch from an activity that already has a serial successor", async () => {
    const user = userEvent.setup();
    const onAddBranch = vi.fn();
    const nodes = [
      node("start", "completed", 0),
      node("split", "completed", 1),
      node("analysis", "skipped", 2),
      node("implementation", "blocked", 3),
      node("join", "pending", 4),
      node("end", "pending", 5),
    ];
    render(
      <I18nProvider locale="en" resources={RESOURCES}>
        <WorkflowCanvas
          definition={definition}
          nodes={nodes}
          selectedId="node-analysis"
          onSelect={vi.fn()}
          onAddBranch={onAddBranch}
        />
      </I18nProvider>,
    );

    await user.click(screen.getByRole("button", {
      name: "Actions for Analysis",
    }));
    expect(await screen.findByText("Add parallel branch")).toBeInTheDocument();
    await user.click(await screen.findByRole("menuitem", { name: "Add node" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Activity" }));

    expect(onAddBranch).toHaveBeenCalledWith("activity", "analysis");
  });
});
