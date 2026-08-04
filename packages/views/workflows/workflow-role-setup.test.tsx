// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import type {
  WorkflowExecutorResolution,
  WorkflowNodeInstance,
  WorkflowNodeTask,
  WorkflowSubmission,
  WorkflowVerdict,
} from "@multica/core/workflows";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import {
  RoleSetupPanel,
  AcceptancePanel,
  SubmissionPanel,
  VerdictPanel,
  WorkflowTaskCard,
} from "./workflow-workbench";

const mocks = vi.hoisted(() => ({
  mutate: vi.fn(),
  submit: vi.fn(),
  resolveExecutor: vi.fn(),
  changeTask: vi.fn(),
  recordVerdict: vi.fn(),
  decideAcceptance: vi.fn(),
}));

vi.mock("@multica/core/workflows", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/workflows")>()),
  useUpdateWorkflowInstanceRoles: () => ({
    mutate: mocks.mutate,
    isPending: false,
    isError: false,
  }),
  useCreateWorkflowSubmission: () => ({
    mutate: mocks.submit,
    isPending: false,
    isError: false,
  }),
  useResolveWorkflowNodeExecutor: () => ({
    mutate: mocks.resolveExecutor,
    isPending: false,
  }),
  useChangeWorkflowNodeTask: () => ({
    mutate: mocks.changeTask,
    isPending: false,
    isError: false,
  }),
  useCreateWorkflowVerdict: () => ({
    mutate: mocks.recordVerdict,
    isPending: false,
    isError: false,
  }),
  useDecideWorkflowAcceptance: () => ({
    mutate: mocks.decideAcceptance,
    isPending: false,
  }),
}));

function renderPanel(canConfigure = true) {
  render(
    <I18nProvider
      locale="en"
      resources={{
        en: { common: enCommon, workflows: enWorkflows },
      }}
    >
      <RoleSetupPanel
        instanceId="instance-1"
        roles={[{
          key: "owner",
          name: "Owner",
          required: true,
          allowed_actor_types: ["member"],
        }]}
        currentAssignments={[]}
        actorOptions={[
          { type: "member", id: "member-1", name: "Ada" },
          { type: "agent", id: "agent-1", name: "Build Agent" },
        ]}
        canConfigure={canConfigure}
      />
    </I18nProvider>,
  );
}

describe("RoleSetupPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("requires every mandatory role and only submits eligible actors", async () => {
    const user = userEvent.setup();
    renderPanel();

    const save = screen.getByRole("button", {
      name: "Save role assignments",
    });
    expect(save).toBeDisabled();
    expect(screen.queryByRole("option", { name: /Build Agent/ }))
      .not.toBeInTheDocument();

    await user.selectOptions(
      screen.getByRole("combobox", { name: /Owner/ }),
      "member:member-1",
    );
    expect(save).toBeEnabled();
    await user.click(save);

    expect(mocks.mutate).toHaveBeenCalledWith([{
      role_key: "owner",
      actor_type: "member",
      actor_id: "member-1",
      source: "user_selected",
    }]);
  });

  it("shows the pending setup without controls to an unauthorized viewer", () => {
    renderPanel(false);

    expect(screen.getByText(
      "The workflow starter or a workspace admin must assign the missing roles.",
    )).toBeInTheDocument();
    expect(screen.queryByRole("button", {
      name: "Save role assignments",
    })).not.toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: /Owner/ })).toBeDisabled();
  });
});

function renderSubmissionPanel(
  submissions: WorkflowSubmission[] = [],
) {
  const node = {
    id: "node-1",
    status: "active",
    definition: {
      key: "review",
      kind: "activity",
      name: "Review",
      // The default node shape: no issues, no schema. Gating the panel on a
      // schema left exactly this node with nowhere to hand anything off from.
      issue_policy: "none",
    },
  } as unknown as WorkflowNodeInstance;
  render(
    <I18nProvider
      locale="en"
      resources={{
        en: { common: enCommon, workflows: enWorkflows },
      }}
    >
      <SubmissionPanel
        instanceId="instance-1"
        node={node}
        submissions={submissions}
        tasks={[]}
        actorOptions={[
          { type: "member", id: "member-1", name: "Ada" },
          { type: "agent", id: "agent-1", name: "Build Agent" },
        ]}
        canManage
        branchDuty={null}
      />
    </I18nProvider>,
  );
}

describe("SubmissionPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("keeps all submission revisions visible", () => {
    const revisions = [2, 1].map((revision) => ({
      id: `submission-${revision}`,
      workflow_node_instance_id: "node-1",
      revision,
      status: "valid",
      payload: { summary: `Revision ${revision}` },
      summary: `Revision ${revision}`,
      source_issue_id: null,
      source_agent_run_id: null,
      evidence: [],
      submitted_by_type: "member",
      submitted_by_id: "member-1",
      created_at: "2026-07-23T00:00:00Z",
    }));
    renderSubmissionPanel(revisions);

    expect(screen.getByText("#1")).toBeInTheDocument();
    expect(screen.getByText("#2")).toBeInTheDocument();
    expect(screen.getByText("Revision 1")).toBeInTheDocument();
    expect(screen.getByText("Revision 2")).toBeInTheDocument();
  });
});

describe("WorkflowTaskCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows executor resolution evidence and supports manual fallback", async () => {
    const user = userEvent.setup();
    const task = {
      id: "task-1",
      definition: { title: "Implement API" },
      required: true,
      source: "template",
      materialization_status: "pending_materialization",
      executor_resolution_id: null,
      issue_id: null,
      last_error: "",
    } as unknown as WorkflowNodeTask;
    const resolution = {
      id: "resolution-1",
      workflow_node_instance_id: "node-1",
      workflow_node_task_id: "task-1",
      strategy: "capability_match",
      status: "unresolved",
      actor_type: null,
      actor_id: null,
      candidates: [],
      reason: "No actor matched the required capability",
      resolved_at: null,
      created_at: "2026-07-23T00:00:00Z",
    } satisfies WorkflowExecutorResolution;

    render(
      <I18nProvider
        locale="en"
        resources={{
          en: { common: enCommon, workflows: enWorkflows },
        }}
      >
        <WorkflowTaskCard
          instanceId="instance-1"
          nodeId="node-1"
          task={task}
          resolution={resolution}
          actorOptions={[
            { type: "member", id: "member-1", name: "Ada" },
          ]}
          canManage
          canAdmin={false}
        />
      </I18nProvider>,
    );

    expect(screen.getByText(
      /capability_match · No actor matched the required capability/,
    )).toBeInTheDocument();
    await user.selectOptions(
      screen.getByRole("combobox", {
        name: "Choose a member, agent, or squad",
      }),
      "member:member-1",
    );
    await user.click(screen.getByRole("button", { name: "Assign" }));

    expect(mocks.resolveExecutor).toHaveBeenCalledWith({
      task_id: "task-1",
      actor_type: "member",
      actor_id: "member-1",
      reason: "Configured manually in the workflow workbench",
    });
  });

  it("offers retry when a direct execution failed after materialization", async () => {
    const user = userEvent.setup();
    const task = {
      id: "task-direct",
      task_key: "execution",
      definition: { key: "diagnosis", kind: "activity", name: "Run diagnosis" },
      required: true,
      source: "execution",
      materialization_status: "materialized",
      executor_resolution_id: "resolution-direct",
      issue_id: null,
      last_error: "",
    } as WorkflowNodeTask;
    const resolution = {
      id: "resolution-direct",
      workflow_node_instance_id: "node-1",
      workflow_node_task_id: "task-direct",
      strategy: "pinned_actor",
      status: "resolved",
      actor_type: "agent",
      actor_id: "agent-1",
      candidates: [],
      reason: "",
      resolved_at: "2026-07-23T00:00:00Z",
      created_at: "2026-07-23T00:00:00Z",
    } satisfies WorkflowExecutorResolution;

    render(
      <I18nProvider
        locale="en"
        resources={{ en: { common: enCommon, workflows: enWorkflows } }}
      >
        <WorkflowTaskCard
          instanceId="instance-1"
          nodeId="node-1"
          task={task}
          resolution={resolution}
          actorOptions={[{ type: "agent", id: "agent-1", name: "Build Agent" }]}
          canManage
          canAdmin
          executionFailed
        />
      </I18nProvider>,
    );

    await user.click(screen.getByText("Recovery actions"));
    await user.type(
      screen.getByRole("textbox", { name: "Reason for this action (required)" }),
      "Retry the failed run",
    );
    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(mocks.changeTask).toHaveBeenCalledWith({
      taskId: "task-direct",
      action: "retry",
      reason: "Retry the failed run",
    });
  });
});

function workflowNode(overrides: Partial<WorkflowNodeInstance> = {}) {
  return {
    id: "node-1",
    workflow_instance_id: "instance-1",
    node_key: "review",
    node_kind: "activity",
    attempt: 1,
    name: "Review",
    display_order: 1,
    definition: {
      key: "review",
      kind: "activity",
      name: "Review",
      reviewer: { kind: "role", role: "qa", required: true },
    },
    status: "active",
    waiting_reasons: [],
    latest_submission_id: null,
    latest_verdict_id: null,
    activated_at: "2026-07-23T00:00:00Z",
    completed_at: null,
    ...overrides,
  } as WorkflowNodeInstance;
}

describe("VerdictPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the verdict record without offering a second form", () => {
    const verdict = {
      id: "verdict-1",
      workflow_node_instance_id: "node-1",
      revision: 2,
      result: "blocked",
      reason: "Security review is missing",
      confidence: 0.75,
      evidence: [{ artifact: "report" }],
      basis: {},
      evaluator_type: "member",
      evaluator_id: "member-1",
      created_at: "2026-07-23T00:00:00Z",
    } satisfies WorkflowVerdict;

    render(
      <I18nProvider
        locale="en"
        resources={{
          en: { common: enCommon, workflows: enWorkflows },
        }}
      >
        <VerdictPanel verdicts={[verdict]} />
      </I18nProvider>,
    );

    expect(screen.getByText("Security review is missing")).toBeInTheDocument();
    expect(screen.getByText(/75%/)).toBeInTheDocument();
    expect(screen.getByText(/"artifact": "report"/)).toBeInTheDocument();
    // Passing and sending back are the node's primary buttons; this tab is
    // the record of what was decided, not a second way to decide it.
    expect(screen.queryByRole("combobox", { name: "Check result" }))
      .not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Record check result" }))
      .not.toBeInTheDocument();
  });
});

describe("AcceptancePanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("supports approval and a reasoned rework target", async () => {
    const user = userEvent.setup();
    render(
      <I18nProvider
        locale="en"
        resources={{
          en: { common: enCommon, workflows: enWorkflows },
        }}
      >
        <AcceptancePanel
          instanceId="instance-1"
          targets={[{ value: "implementation", label: "Implementation" }]}
          pending
          canDecide
        />
      </I18nProvider>,
    );

    await user.click(screen.getByRole("button", { name: "Approve" }));
    expect(mocks.decideAcceptance).toHaveBeenCalledWith({
      status: "approved",
    });

    // A rejection is only useful if it says what is wrong, so the button stays
    // disabled until the expected-versus-actual answer is filled in — the
    // criterion and the hint are optional context around it.
    const rework = screen.getByRole("button", { name: "Request changes" });
    expect(rework).toBeDisabled();
    await user.type(
      screen.getByRole("textbox", { name: "Which criterion or test failed" }),
      "AC-004",
    );
    expect(rework).toBeDisabled();
    await user.type(
      screen.getByRole("textbox", { name: "Expected vs actual (required)" }),
      "should block, allowed instead",
    );
    expect(rework).toBeEnabled();
    await user.click(rework);
    expect(mocks.decideAcceptance).toHaveBeenLastCalledWith({
      status: "changes_requested",
      reason: "Failed: AC-004\nExpected vs actual: should block, allowed instead",
      rework_target_node_key: "implementation",
    });
  });
});

describe("SubmissionPanel handoff entry", () => {
  it("offers a handoff summary on a node that declares no schema", () => {
    renderSubmissionPanel();
    expect(screen.getByLabelText("Summary")).toBeInTheDocument();
  });
});
