// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import type { WorkflowNodeInstance } from "@multica/core/workflows";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { NodeTransitionPanel } from "./workflow-workbench";

const mocks = vi.hoisted(() => ({
  submit: vi.fn(),
  transition: vi.fn(),
  recordVerdict: vi.fn(),
}));

vi.mock("@multica/core/workflows", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/workflows")>()),
  useCreateWorkflowSubmission: () => ({
    mutateAsync: mocks.submit,
    isPending: false,
    isError: false,
  }),
  useTransitionWorkflowNode: () => ({
    mutateAsync: mocks.transition,
    isPending: false,
    isError: false,
  }),
  useCreateWorkflowVerdict: () => ({
    mutateAsync: mocks.recordVerdict,
    isPending: false,
    isError: false,
  }),
}));

function workflowNode(overrides: Partial<WorkflowNodeInstance> = {}) {
  return {
    id: "node-1",
    workflow_instance_id: "instance-1",
    node_key: "work",
    node_kind: "activity",
    attempt: 1,
    name: "Work",
    display_order: 1,
    definition: {
      key: "work",
      kind: "activity",
      name: "Work",
      completion: { mode: "automatic" },
      reviewer: { kind: "role", role: "owner", required: true },
    },
    status: "in_review",
    waiting_reasons: [{ code: "review_required", message: "Waiting for the reviewer" }],
    latest_submission_id: "submission-1",
    latest_verdict_id: null,
    activated_at: "2026-08-03T00:00:00Z",
    completed_at: null,
    ...overrides,
  } as WorkflowNodeInstance;
}

function renderPanel({
  node = workflowNode(),
  canManage = true,
  canAdmin = false,
  instanceRunning = true,
}: {
  node?: WorkflowNodeInstance;
  canManage?: boolean;
  canAdmin?: boolean;
  instanceRunning?: boolean;
} = {}) {
  render(
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, workflows: enWorkflows } }}
    >
      <NodeTransitionPanel
        instanceId="instance-1"
        node={node}
        submissions={[]}
        canManage={canManage}
        canAdmin={canAdmin}
        instanceRunning={instanceRunning}
      />
    </I18nProvider>,
  );
}

describe("NodeTransitionPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("offers the reviewer a pass button that needs no reason", async () => {
    const user = userEvent.setup();
    renderPanel();

    await user.click(screen.getByRole("button", { name: "Pass" }));
    const confirm = screen.getAllByRole("button", { name: "Pass" }).at(-1)!;
    expect(confirm).toBeEnabled();
    await user.click(confirm);

    expect(mocks.recordVerdict).toHaveBeenCalledWith({
      result: "pass",
      reason: undefined,
    });
  });

  it("requires a reason before sending the activity back", async () => {
    const user = userEvent.setup();
    renderPanel();

    await user.click(screen.getByRole("button", { name: "Send back" }));
    const confirm = screen.getAllByRole("button", { name: "Send back" })
      .at(-1)!;
    expect(confirm).toBeDisabled();

    await user.type(
      screen.getByRole("textbox", { name: /Reason/ }),
      "Missing the demo page",
    );
    await user.click(confirm);

    expect(mocks.recordVerdict).toHaveBeenCalledWith({
      result: "fail",
      reason: "Missing the demo page",
    });
  });

  it("keeps the admin bypasses out of the primary slot", async () => {
    const user = userEvent.setup();
    renderPanel({ canAdmin: true });

    expect(screen.queryByRole("button", { name: "Force complete" }))
      .not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Manage activity" }));
    expect(await screen.findByRole("menuitem", { name: "Force complete" }))
      .toBeInTheDocument();
    expect(await screen.findByRole("menuitem", { name: "Skip" }))
      .toBeInTheDocument();
  });

  it("hides the review buttons from members who cannot decide", () => {
    renderPanel({ canManage: false });

    expect(screen.queryByRole("button", { name: "Pass" }))
      .not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send back" }))
      .not.toBeInTheDocument();
  });

  it("hides the review buttons when the reviewer is not a person", () => {
    renderPanel({
      node: workflowNode({
        definition: {
          key: "work",
          kind: "activity",
          name: "Work",
          completion: { mode: "automatic" },
          reviewer: { kind: "auto", required: true },
        },
      } as Partial<WorkflowNodeInstance>),
    });

    expect(screen.queryByRole("button", { name: "Pass" }))
      .not.toBeInTheDocument();
  });

  it("promotes rollback to the primary slot once the activity is done", () => {
    renderPanel({
      node: workflowNode({
        status: "completed",
        waiting_reasons: [],
        completed_at: "2026-08-03T01:00:00Z",
      }),
    });

    expect(screen.getByRole("button", { name: "Roll back here" }))
      .toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Manage activity" }))
      .not.toBeInTheDocument();
  });

  it("still shows manual completion for a legacy manual activity", () => {
    renderPanel({
      node: workflowNode({
        status: "active",
        waiting_reasons: [],
        definition: {
          key: "work",
          kind: "activity",
          name: "Work",
          completion: { mode: "manual" },
        },
      } as Partial<WorkflowNodeInstance>),
    });

    expect(screen.getByRole("button", { name: "Complete activity" }))
      .toBeInTheDocument();
  });
});
