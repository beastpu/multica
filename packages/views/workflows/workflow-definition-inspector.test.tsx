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
import {
  WorkflowDefinitionInspector,
  WorkflowNodeDefinitionInspector,
} from "./workflow-definition-inspector";

const node: WorkflowNodeDefinition = {
  key: "backend",
  kind: "activity",
  name: "Backend development",
  issue_policy: "fixed",
  executor: {
    kind: "actor",
    actor_type: "agent",
    actor_id: "agent-default",
    fallback: { kind: "manual" },
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
  acceptance: { policy: "none" },
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

describe("WorkflowDefinitionInspector", () => {
  it("keeps role management out of workflow settings", () => {
    render(
      <I18nProvider
        locale="en"
        resources={{ en: { workflows: enWorkflows } }}
      >
        <WorkflowDefinitionInspector
          definition={definition}
          readOnly={false}
          onChange={vi.fn()}
        />
      </I18nProvider>,
    );

    expect(screen.queryByText(enWorkflows.editor.workflow_roles))
      .not.toBeInTheDocument();
    expect(screen.getByText(enWorkflows.editor.workflow_acceptance))
      .toBeInTheDocument();
  });
});

describe("WorkflowNodeDefinitionInspector", () => {
  it("splits the activity config into info, work, and transition tabs", () => {
    renderInspector(vi.fn());

    const tabs = screen.getAllByRole("tab").map((tab) => tab.textContent);
    expect(tabs).toEqual([
      enWorkflows.editor.tab_info,
      enWorkflows.editor.tab_work,
      enWorkflows.editor.tab_transition,
    ]);
  });

  it("shows predecessors and successors from the graph edges", () => {
    renderInspector(vi.fn());

    expect(screen.getByText(enWorkflows.editor.flow_predecessors))
      .toBeInTheDocument();
    expect(screen.getByText("Start")).toBeInTheDocument();
    expect(screen.getByText("End")).toBeInTheDocument();
  });

  it("shows humanized executor kind labels instead of engine enums", () => {
    renderInspector(vi.fn());

    expect(screen.getAllByRole("option", { name: "By role" }).length)
      .toBeGreaterThan(0);
    expect(screen.queryByRole("option", { name: "fixed_role" })).toBeNull();
    expect(screen.queryByRole("option", { name: "manual" })).toBeNull();
  });

  it("hides executor fallback configuration from the default editor", () => {
    renderInspector(vi.fn());

    expect(screen.queryByText(enWorkflows.editor.executor_fallback))
      .not.toBeInTheDocument();
  });

  it("keeps the executor's fallback when the actor changes", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderInspector(onChange);

    await user.selectOptions(
      screen.getByLabelText(enWorkflows.editor.direct_executor),
      "squad:squad-review",
    );

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      executor: {
        kind: "actor",
        actor_type: "squad",
        actor_id: "squad-review",
        fallback: { kind: "manual" },
      },
    }));
  });

  it("shows the legacy completion behavior and stores an explicit manual mode", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderInspector(onChange);

    await user.click(
      screen.getByRole("tab", { name: enWorkflows.editor.tab_transition }),
    );
    expect(screen.getByLabelText("Completion trigger")).toHaveValue("automatic");
    expect(screen.getByText("Completion form")).toBeInTheDocument();

    await user.selectOptions(
      screen.getByLabelText("Completion trigger"),
      "manual",
    );

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      completion: expect.objectContaining({ mode: "manual" }),
    }));
  });

  it("pins a workspace member as the node owner", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <I18nProvider
        locale="en"
        resources={{ en: { workflows: enWorkflows } }}
      >
        <WorkflowNodeDefinitionInspector
          node={{ ...node, executor: undefined }}
          definition={definition}
          actorOptions={[
            ...actorOptions,
            { type: "member" as const, id: "member-1", name: "Ada" },
          ]}
          readOnly={false}
          onChange={onChange}
        />
      </I18nProvider>,
    );

    await user.selectOptions(
      screen.getByLabelText(enWorkflows.editor.node_owner_by_actor),
      "member:member-1",
    );

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      owner_role: undefined,
      executor: {
        kind: "actor",
        actor_type: "member",
        actor_id: "member-1",
        fallback: { kind: "manual" },
      },
    }));
  });

  it("enables the owner reviewer for a pinned member owner", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider
        locale="en"
        resources={{ en: { workflows: enWorkflows } }}
      >
        <WorkflowNodeDefinitionInspector
          node={{
            ...node,
            owner_role: undefined,
            executor: {
              kind: "actor",
              actor_type: "member",
              actor_id: "member-1",
              fallback: { kind: "manual" },
            },
          }}
          definition={definition}
          actorOptions={actorOptions}
          readOnly={false}
          onChange={vi.fn()}
        />
      </I18nProvider>,
    );

    await user.click(
      screen.getByRole("tab", { name: enWorkflows.editor.tab_transition }),
    );
    expect(
      screen.getByRole("option", {
        name: enWorkflows.editor.reviewer_kind_owner,
      }),
    ).toBeEnabled();
  });

  it("stores host-status node events from the transition tab", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderInspector(onChange);

    await user.click(
      screen.getByRole("tab", { name: enWorkflows.editor.tab_transition }),
    );
    await user.selectOptions(
      screen.getByLabelText(enWorkflows.editor.on_complete_host_status),
      "in_review",
    );

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      on_complete: [{ kind: "set_host_status", status: "in_review" }],
    }));
  });

  it("stores a reviewer that holds the node until it rules", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const memberOwnerDefinition: WorkflowDefinition = {
      ...definition,
      roles: [{
        key: "owner",
        name: "Owner",
        required: true,
        allowed_actor_types: ["member"],
      }],
    };
    render(
      <I18nProvider
        locale="en"
        resources={{ en: { workflows: enWorkflows } }}
      >
        <WorkflowNodeDefinitionInspector
          node={{ ...node, owner_role: "owner" }}
          definition={memberOwnerDefinition}
          actorOptions={actorOptions}
          readOnly={false}
          onChange={onChange}
        />
      </I18nProvider>,
    );

    await user.click(
      screen.getByRole("tab", { name: enWorkflows.editor.tab_transition }),
    );
    await user.selectOptions(
      screen.getByLabelText(enWorkflows.editor.reviewer),
      "owner",
    );

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      reviewer: expect.objectContaining({ kind: "owner", required: true }),
    }));
  });

  it("stores an agent reviewer and explains the built-in Critic protocol", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderInspector(onChange);

    await user.click(
      screen.getByRole("tab", { name: enWorkflows.editor.tab_transition }),
    );
    await user.selectOptions(
      screen.getByLabelText(enWorkflows.editor.reviewer),
      "agent",
    );

    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({
      reviewer: {
        kind: "actor",
        actor_type: "agent",
        required: true,
      },
    }));
  });

  it("keeps members out of the agent reviewer picker", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider locale="en" resources={{ en: { workflows: enWorkflows } }}>
        <WorkflowNodeDefinitionInspector
          node={{
            ...node,
            reviewer: {
              kind: "actor",
              actor_type: "agent",
              actor_id: "agent-default",
              required: true,
            },
          }}
          definition={definition}
          actorOptions={[
            ...actorOptions,
            { type: "member", id: "member-1", name: "Ada" },
          ]}
          readOnly={false}
          onChange={vi.fn()}
        />
      </I18nProvider>,
    );

    await user.click(
      screen.getByRole("tab", { name: enWorkflows.editor.tab_transition }),
    );
    const picker = screen.getByLabelText(enWorkflows.editor.reviewer_kind_agent);
    expect(picker).toHaveTextContent("Backend Agent");
    expect(picker).toHaveTextContent("Review Squad");
    expect(picker).not.toHaveTextContent("Ada");
    expect(screen.getByText(enWorkflows.editor.reviewer_agent_protocol_hint))
      .toBeInTheDocument();
  });
});

describe("artifact declarations", () => {
  // Artifacts were only expressible by hand-editing the definition JSON, which
  // is why templates in the wild carry none and downstream nodes get a handoff
  // summary with nothing behind it.
  it("adds an artifact with a generated key and sensible defaults", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    renderInspector(onChange);

    await user.click(screen.getByRole("tab", { name: enWorkflows.editor.tab_work }));
    await user.click(
      screen.getByRole("button", { name: enWorkflows.editor.add_artifact }),
    );

    expect(onChange).toHaveBeenCalledTimes(1);
    const next = onChange.mock.calls[0]![0] as WorkflowNodeDefinition;
    expect(next.artifacts).toHaveLength(1);
    const [artifact] = next.artifacts!;
    // Required and document by default: the common case is "this node owes a
    // written deliverable", and an optional artifact gates nothing.
    expect(artifact).toMatchObject({ kind: "document", required: true });
    expect(artifact!.key).toMatch(/^artifact_/);
  });

  it("edits and removes a declared artifact", async () => {
    const onChange = vi.fn();
    const user = userEvent.setup();
    const withArtifact: WorkflowNodeDefinition = {
      ...node,
      artifacts: [{
        key: "design_doc",
        name: "Design doc",
        kind: "document",
        required: true,
      }],
    };
    render(
      <I18nProvider locale="en" resources={{ en: { workflows: enWorkflows } }}>
        <WorkflowNodeDefinitionInspector
          node={withArtifact}
          definition={definition}
          actorOptions={actorOptions}
          readOnly={false}
          onChange={onChange}
        />
      </I18nProvider>,
    );

    await user.click(screen.getByRole("tab", { name: enWorkflows.editor.tab_work }));
    // The key is shown, not editable: agents submit against it and the server
    // rejects anything else, so renaming it would orphan live submissions.
    expect(screen.getByText("design_doc")).toBeInTheDocument();

    await user.click(screen.getAllByRole("button", {
      name: enWorkflows.actions.remove,
    }).at(-1)!);
    const afterRemove = onChange.mock.calls.at(-1)![0] as WorkflowNodeDefinition;
    expect(afterRemove.artifacts).toEqual([]);
  });
});
