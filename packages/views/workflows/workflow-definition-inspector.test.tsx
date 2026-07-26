// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import type {
  WorkflowDefinition,
  WorkflowNodeDefinition,
} from "@multica/core/workflows";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import enWorkflows from "../locales/en/workflows.json";
import { WorkflowNodeDefinitionInspector } from "./workflow-definition-inspector";

const node: WorkflowNodeDefinition = {
  key: "backend",
  kind: "activity",
  activity_mode: "work",
  name: "Backend development",
  issue_policy: "fixed",
  executor: {
    strategies: [{
      kind: "fixed_actor",
      actor_type: "agent",
      actor_id: "agent-default",
    }, {
      kind: "manual",
    }],
  },
  issue_templates: [{
    key: "implementation",
    title: "Implement {{host.title}}",
    required: true,
    initial_status: "todo",
  }],
};

const definition: WorkflowDefinition = {
  schema_version: 1,
  name: "Delivery",
  applies_to: { kind: "issue" },
  roles: [],
  nodes: [
    { key: "start", kind: "start", name: "Start" },
    node,
    { key: "end", kind: "end", name: "End" },
  ],
  edges: [
    { from: "start", to: "backend" },
    { from: "backend", to: "end" },
  ],
  acceptance: { policy: "none", rework_targets: [] },
};

const actorOptions = [{
  type: "agent" as const,
  id: "agent-default",
  name: "Backend Agent",
}, {
  type: "squad" as const,
  id: "squad-review",
  name: "Review Squad",
}];

function renderInspector(onChange: (value: WorkflowNodeDefinition) => void) {
  return render(
    <I18nProvider
      locale="en"
      resources={{ en: { workflows: enWorkflows } }}
    >
      <WorkflowNodeDefinitionInspector
        node={node}
        definition={definition}
        actorOptions={actorOptions}
        readOnly={false}
        onChange={onChange}
      />
    </I18nProvider>,
  );
}

describe("WorkflowNodeDefinitionInspector", () => {
  it("shows humanized executor strategy labels instead of engine enums", () => {
    renderInspector(vi.fn());

    expect(screen.getAllByRole("option", { name: "By role" }).length)
      .toBeGreaterThan(0);
    expect(screen.queryByRole("option", { name: "fixed_role" })).toBeNull();
    expect(screen.queryByRole("option", { name: "manual" })).toBeNull();
  });

  it("collapses executor resolution behind an advanced toggle when unconfigured", async () => {
    const user = userEvent.setup();
    const bareNode: WorkflowNodeDefinition = {
      key: "backend",
      kind: "activity",
      activity_mode: "work",
      name: "Backend development",
      issue_policy: "fixed",
      issue_templates: node.issue_templates,
    };
    render(
      <I18nProvider
        locale="en"
        resources={{ en: { workflows: enWorkflows } }}
      >
        <WorkflowNodeDefinitionInspector
          node={bareNode}
          definition={definition}
          actorOptions={actorOptions}
          readOnly={false}
          onChange={vi.fn()}
        />
      </I18nProvider>,
    );

    expect(screen.queryByText("Add resolution strategy")).toBeNull();
    await user.click(
      screen.getByRole("button", { name: /Executor resolution/ }),
    );
    expect(screen.getByText("Add resolution strategy")).toBeInTheDocument();
  });

  it("assigns a concrete node executor", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderInspector(onChange);

    await user.selectOptions(
      screen.getByLabelText("Default assignee"),
      "squad:squad-review",
    );

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      executor: {
        strategies: [{
          kind: "fixed_actor",
          actor_type: "squad",
          actor_id: "squad-review",
        }, {
          kind: "manual",
        }],
      },
    }));
  });

  it("stores an issue assignee as an override of the node default", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderInspector(onChange);

    await user.selectOptions(
      screen.getByLabelText("Direct assignee override"),
      "squad:squad-review",
    );

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      issue_templates: [expect.objectContaining({
        key: "implementation",
        assignee_role: undefined,
        assignee_type: "squad",
        assignee_id: "squad-review",
      })],
    }));
  });

  it("shows the legacy completion behavior and stores an explicit manual mode", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderInspector(onChange);

    expect(screen.getByLabelText("Completion method")).toHaveValue("automatic");
    expect(screen.getByText("Completion form")).toBeInTheDocument();

    await user.selectOptions(
      screen.getByLabelText("Completion method"),
      "manual",
    );

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      completion: expect.objectContaining({ mode: "manual" }),
    }));
  });
});
