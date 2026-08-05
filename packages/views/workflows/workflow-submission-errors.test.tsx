// @vitest-environment jsdom

import { ApiError } from "@multica/core/api";
import { I18nProvider } from "@multica/core/i18n/react";
import type { WorkflowNodeInstance } from "@multica/core/workflows";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { SubmissionPanel } from "./workflow-workbench";

const mocks = vi.hoisted(() => ({
  submit: vi.fn(),
  submitError: { current: null as unknown },
  isError: { current: false },
}));

vi.mock("@multica/core/workflows", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/workflows")>()),
  useCreateWorkflowSubmission: () => ({
    mutate: mocks.submit,
    isPending: false,
    isError: mocks.isError.current,
    error: mocks.submitError.current,
  }),
  useResolveWorkflowNodeExecutor: () => ({ mutate: vi.fn(), isPending: false }),
  useChangeWorkflowNodeTask: () => ({
    mutate: vi.fn(), isPending: false, isError: false,
  }),
}));

function renderPanel() {
  const node = {
    id: "node-1",
    status: "active",
    definition: {
      key: "triage",
      kind: "activity",
      name: "Triage",
      issue_policy: "none",
      outputs: [
        { key: "is_bug", type: "bool", required: true, desc: "Is it a real defect" },
        { key: "category", type: "enum", values: ["bug", "duplicate"], required: true },
      ],
    },
  } as unknown as WorkflowNodeInstance;
  render(
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, workflows: enWorkflows } }}
    >
      <SubmissionPanel
        instanceId="instance-1"
        node={node}
        submissions={[]}
        tasks={[]}
        actorOptions={[]}
        canManage
      />
    </I18nProvider>,
  );
}

describe("SubmissionPanel output fields", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.isError.current = false;
    mocks.submitError.current = null;
  });

  it("labels a field by its description and keeps the key visible", () => {
    renderPanel();
    // The description is what a person reads; the key still shows because it
    // is what conditions and agents address the field by.
    expect(screen.getByText("Is it a real defect")).toBeInTheDocument();
    expect(screen.getByText("is_bug")).toBeInTheDocument();
    // A field with no description falls back to its key as the label.
    expect(screen.getByText("category")).toBeInTheDocument();
  });

  it("names each rejected field instead of a generic failure", () => {
    mocks.isError.current = true;
    mocks.submitError.current = new ApiError("bad request", 400, "Bad Request", {
      error: "output_validation_failed",
      fields: [
        { key: "is_bug", problem: "missing_required" },
        {
          key: "category",
          problem: "invalid_enum",
          got: "urgent",
          expected: ["bug", "duplicate"],
        },
      ],
    });
    renderPanel();

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("required, but not filled in");
    expect(alert).toHaveTextContent("not one of the allowed values");
    // The accepted values travel with the error so the fix needs no guessing.
    expect(alert).toHaveTextContent("bug, duplicate");
    expect(alert).not.toHaveTextContent("Something went wrong");
  });

  it("falls back to the generic message for non-validation failures", () => {
    mocks.isError.current = true;
    mocks.submitError.current = new ApiError("boom", 500, "Server Error", {
      error: "internal",
    });
    renderPanel();

    const alert = screen.getByRole("alert");
    expect(alert).not.toHaveTextContent("required, but not filled in");
  });
});
