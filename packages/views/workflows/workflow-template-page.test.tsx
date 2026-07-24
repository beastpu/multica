// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { WorkflowTemplatePage } from "./workflow-template-page";

const mocks = vi.hoisted(() => ({
  validate: vi.fn(),
  validateReset: vi.fn(),
  publish: vi.fn(),
  updateMetadata: vi.fn(),
  archive: vi.fn(),
  updateDraft: vi.fn(),
  createDraft: vi.fn(),
  validation: { valid: true, errors: [] as string[] },
}));

const definition = {
  schema_version: 1,
  name: "Delivery workflow",
  applies_to: { kind: "issue" },
  roles: [],
  nodes: [
    { key: "start", kind: "start", name: "Start" },
    { key: "work", kind: "activity", name: "Work" },
    { key: "end", kind: "end", name: "End" },
  ],
  edges: [
    { from: "start", to: "work" },
    { from: "work", to: "end" },
  ],
  acceptance: { policy: "none", rework_targets: [] },
};

const detail = {
  template: {
    id: "template-1",
    workspace_id: "workspace-1",
    name: "Delivery workflow",
    description: "Ship a requirement safely",
    applies_to_kind: "issue",
    applies_to_type_key: "requirement",
    status: "draft",
    latest_published_version_id: null,
    created_by: "user-1",
    archived_at: null,
    created_at: "2026-07-23T00:00:00Z",
    updated_at: "2026-07-23T00:00:00Z",
    latest_published_version: 0,
    draft_version: 1,
    has_draft: true,
    activity_count: 1,
    run_count: 0,
    last_published_by: null,
    last_published_at: null,
    latest_change_summary: "",
  },
  versions: [{
    id: "version-1",
    workspace_id: "workspace-1",
    template_id: "template-1",
    version: 1,
    revision: 1,
    status: "draft",
    definition,
    definition_checksum: "checksum",
    change_summary: "Initial delivery workflow",
    created_by: "user-1",
    published_by: null,
    published_at: null,
    created_at: "2026-07-23T00:00:00Z",
    updated_at: "2026-07-23T00:00:00Z",
  }],
};

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: unknown[] }) => {
    if (options.queryKey?.includes("members")) {
      return {
        data: [{ user_id: "user-1", role: "admin" }],
        isLoading: false,
        isError: false,
      };
    }
    return { data: detail, isLoading: false, isError: false };
  },
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (
    selector: (state: { user: { id: string } }) => unknown,
  ) => selector({ user: { id: "user-1" } }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"] }),
}));

vi.mock("@multica/core/workflows", () => ({
  workflowTemplateOptions: () => ({ queryKey: ["workflow-template"] }),
  useUpdateWorkflowTemplate: () => ({
    isPending: false,
    mutate: (
      input: unknown,
      options?: { onSuccess?: () => void },
    ) => {
      mocks.updateMetadata(input);
      options?.onSuccess?.();
    },
  }),
  useUpdateWorkflowTemplateDraft: () => ({
    isPending: false,
    mutate: mocks.updateDraft,
  }),
  useCreateWorkflowTemplateDraft: () => ({
    isPending: false,
    mutate: mocks.createDraft,
  }),
  usePublishWorkflowTemplate: () => ({
    isPending: false,
    mutate: (
      input: unknown,
      options?: { onSuccess?: () => void },
    ) => {
      mocks.publish(input);
      options?.onSuccess?.();
    },
  }),
  useArchiveWorkflowTemplate: () => ({
    isPending: false,
    mutate: (
      input: unknown,
      options?: { onSuccess?: () => void },
    ) => {
      mocks.archive(input);
      options?.onSuccess?.();
    },
  }),
  useValidateWorkflowTemplateDefinition: () => ({
    data: mocks.validation,
    isPending: false,
    mutate: mocks.validate,
    reset: mocks.validateReset,
  }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("../layout/collection-page", () => ({
  CollectionPageHeader: ({
    title,
    description,
    actions,
  }: {
    title: ReactNode;
    description?: ReactNode;
    actions?: ReactNode;
  }) => (
    <header>
      <h1>{title}</h1>
      <p>{description}</p>
      <div>{actions}</div>
    </header>
  ),
  CollectionPageState: ({ title }: { title: ReactNode }) => <div>{title}</div>,
}));

vi.mock("./workflow-canvas", () => ({
  WorkflowCanvas: ({
    onSelectKey,
  }: {
    onSelectKey?: (key: string) => void;
  }) => (
    <button type="button" onClick={() => onSelectKey?.("work")}>
      Work node
    </button>
  ),
}));

vi.mock("./workflow-definition-inspector", () => ({
  WorkflowDefinitionInspector: () => <div>Definition inspector</div>,
  WorkflowNodeDefinitionInspector: () => <div>Node inspector</div>,
}));

function renderPage() {
  return render(
    <I18nProvider
      locale="en"
      resources={{
        en: { common: enCommon, workflows: enWorkflows },
      }}
    >
      <WorkflowTemplatePage templateId="template-1" />
    </I18nProvider>,
  );
}

describe("WorkflowTemplatePage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.validation.valid = true;
    mocks.validation.errors = [];
  });

  it("validates the current definition and requires explicit publish confirmation", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Validate" }));
    expect(mocks.validate).toHaveBeenCalledWith(definition);

    await user.click(screen.getByRole("button", { name: "Publish" }));
    expect(mocks.publish).not.toHaveBeenCalled();

    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Publish version 1");
    expect(dialog).toHaveTextContent(
      "This will not modify workflow instances already running.",
    );

    await user.click(within(dialog).getByRole("button", { name: "Publish" }));
    await waitFor(() => expect(mocks.publish).toHaveBeenCalledTimes(1));
  });

  it("updates template metadata through the dedicated details dialog", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(
      await screen.findByRole("button", { name: "Edit details" }),
    );
    const dialog = await screen.findByRole("dialog");
    const name = within(dialog).getByLabelText("Template name");
    await user.clear(name);
    await user.type(name, "Release workflow");
    await user.click(within(dialog).getByRole("button", {
      name: "Save details",
    }));

    expect(mocks.updateMetadata).toHaveBeenCalledWith({
      name: "Release workflow",
      description: "Ship a requirement safely",
      applies_to_type_key: "requirement",
    });
  });

  it("does not archive until the destructive action is confirmed", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Archive" }));
    expect(mocks.archive).not.toHaveBeenCalled();

    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Archive" }));
    await waitFor(() => expect(mocks.archive).toHaveBeenCalledTimes(1));
  });
});
