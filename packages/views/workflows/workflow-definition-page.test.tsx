// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { WorkflowPage } from "./workflow-definition-page";

const mocks = vi.hoisted(() => ({
  save: vi.fn(),
  updateMetadata: vi.fn(),
  archive: vi.fn(),
  validation: { valid: true, errors: [] as string[] },
}));

const definition = {
  schema_version: 1,
  name: "Delivery workflow",
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
  acceptance: { policy: "none" },
};

const detail = {
  workflow: {
    id: "template-1",
    workspace_id: "workspace-1",
    name: "Delivery workflow",
    description: "Ship a requirement safely",
    status: "published",
    latest_published_version_id: "version-1",
    created_by: "user-1",
    archived_at: null,
    created_at: "2026-07-23T00:00:00Z",
    updated_at: "2026-07-23T00:00:00Z",
    latest_published_version: 1,
    activity_count: 1,
    run_count: 0,
    last_published_by: null,
    last_published_at: null,
    latest_change_summary: "",
  },
  versions: [{
    id: "version-1",
    workspace_id: "workspace-1",
    workflow_id: "template-1",
    version: 1,
    revision: 1,
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
        data: [{
          user_id: "user-1",
          role: "admin",
          name: "Ada",
          email: "ada@example.com",
        }],
        isLoading: false,
        isError: false,
      };
    }
    if (options.queryKey?.includes("agents")) {
      return { data: [], isLoading: false, isError: false };
    }
    if (options.queryKey?.includes("squads")) {
      return { data: [], isLoading: false, isError: false };
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
  agentListOptions: () => ({ queryKey: ["agents"] }),
  squadListOptions: () => ({ queryKey: ["squads"] }),
}));

vi.mock("@multica/core/workflows", () => ({
  workflowOptions: () => ({ queryKey: ["workflow-template"] }),
  useUpdateWorkflow: () => ({
    isPending: false,
    mutate: (
      input: unknown,
      options?: { onSuccess?: () => void },
    ) => {
      mocks.updateMetadata(input);
      options?.onSuccess?.();
    },
  }),
  useSaveWorkflowDefinition: () => ({
    isPending: false,
    mutate: (
      input: { definition: unknown; change_summary?: string },
      options?: {
        onSuccess?: (result: unknown) => void;
        onError?: (error: Error) => void;
      },
    ) => {
      mocks.save(input);
      if (!mocks.validation.valid) {
        options?.onError?.(new Error(mocks.validation.errors[0] ?? "invalid"));
        return;
      }
      options?.onSuccess?.({
        version: {
          ...detail.versions[0],
          definition: input.definition,
          change_summary: input.change_summary ?? "",
        },
      });
    },
  }),
  useArchiveWorkflow: () => ({
    isPending: false,
    mutate: (
      input: unknown,
      options?: { onSuccess?: () => void },
    ) => {
      mocks.archive(input);
      options?.onSuccess?.();
    },
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
    onInsertNode,
    onAddBranch,
  }: {
    onSelectKey?: (key: string) => void;
    onInsertNode?: (
      kind: "activity",
      target: { from: string; to: string },
    ) => void;
    onAddBranch?: (kind: "activity", from: string) => void;
  }) => (
    <>
      <button type="button" onClick={() => onSelectKey?.("work")}>
        Work node
      </button>
      <button type="button" onClick={() => onSelectKey?.("start")}>
        Start node
      </button>
      <button type="button" onClick={() => onSelectKey?.("end")}>
        End node
      </button>
      <button
        type="button"
        onClick={() => onInsertNode?.("activity", {
          from: "work",
          to: "end",
        })}
      >
        Insert activity between Work and End
      </button>
      <button
        type="button"
        onClick={() => onAddBranch?.("activity", "work")}
      >
        Add parallel activity from Work
      </button>
    </>
  ),
}));

vi.mock("./workflow-definition-inspector", () => ({
  WorkflowNodeDefinitionInspector: ({
    onRemove,
  }: {
    onRemove?: () => void;
  }) => (
    <div>
      Node inspector
      {onRemove && (
        <button type="button" onClick={onRemove}>Delete node</button>
      )}
    </div>
  ),
  WorkflowRoleEditor: () => <div>Role editor</div>,
}));

function renderPage() {
  return render(
    <I18nProvider
      locale="en"
      resources={{
        en: { common: enCommon, workflows: enWorkflows },
      }}
    >
      <WorkflowPage templateId="template-1" />
    </I18nProvider>,
  );
}

describe("WorkflowPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.validation.valid = true;
    mocks.validation.errors = [];
  });

  it("does not expose the internal version change summary in the editor toolbar", async () => {
    renderPage();

    await screen.findByRole("combobox", { name: "Template version" });
    expect(screen.queryByRole("textbox", { name: "Change summary" }))
      .not.toBeInTheDocument();
  });

  it("saves the edited definition into a version in one action", async () => {
    // Authoring used to be four steps — create draft, save, validate, publish.
    // Saving is now all of them, so the edit and the click are the whole flow.
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", {
      name: "Insert activity between Work and End",
    }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(1));
    const input = mocks.save.mock.calls[0]![0] as {
      definition: typeof definition;
    };
    expect(input.definition.nodes).toHaveLength(4);
  });

  it("refuses to save a definition that does not validate", async () => {
    // Storing it would mean carrying a version nobody can run. The edits stay
    // in the editor instead, next to the message naming what to fix.
    mocks.validation.valid = false;
    mocks.validation.errors = ['node "work" has no outgoing edge'];
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", {
      name: "Insert activity between Work and End",
    }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(1));
    expect(
      await screen.findByText(
        "Not saved — this definition does not validate yet.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: 'node "work" has no outgoing edge' }),
    ).toBeInTheDocument();
  });

  it("updates template metadata through the dedicated details dialog", async () => {
    const user = userEvent.setup();
    renderPage();

    // Editing the name is rare, so it lives behind the overflow rather than
    // competing with save for the header row.
    await user.click(
      await screen.findByRole("button", { name: "More actions" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Edit details" }),
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
    });
  });

  it("does not archive until the destructive action is confirmed", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(
      await screen.findByRole("button", { name: "More actions" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Archive" }),
    );
    expect(mocks.archive).not.toHaveBeenCalled();

    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Archive" }));
    await waitFor(() => expect(mocks.archive).toHaveBeenCalledTimes(1));
  });

  it("inserts a connected node from the graph instead of creating an orphan", async () => {
    const user = userEvent.setup();
    renderPage();

    expect(screen.queryByLabelText("New node kind")).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText("Choose a downstream node"),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", {
      name: "Insert activity between Work and End",
    }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    const input = mocks.save.mock.calls[0]![0] as {
      definition: typeof definition;
    };
    const inserted = input.definition.nodes.find(
      (candidate) => candidate.key !== "start" &&
        candidate.key !== "work" &&
        candidate.key !== "end",
    );
    expect(inserted).toMatchObject({
      kind: "activity",
      name: "New activity",
      // A new activity produces no issues until the author opts in. Manual
      // confirmation is represented by a reviewer, not a second completion
      // mode, so authored nodes always use the automatic engine mode.
      issue_policy: "none",
      completion: { mode: "automatic", required_issue_outcome: "none" },
    });
    expect(inserted).not.toHaveProperty("reviewer");
    expect(inserted).not.toHaveProperty("issue_templates");
    expect(input.definition.edges).toEqual([
      { from: "start", to: "work" },
      { from: "work", to: inserted?.key },
      { from: inserted?.key, to: "end" },
    ]);
  });

  it("adds a direct parallel edge from the node handle without a split node", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(screen.getByRole("button", {
      name: "Add parallel activity from Work",
    }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    const input = mocks.save.mock.calls[0]![0] as {
      definition: typeof definition;
    };
    const branch = input.definition.nodes.find(
      (candidate) => candidate.kind === "activity" &&
        candidate.key !== "work",
    );
    expect(
      input.definition.nodes.some(
        (candidate) => candidate.kind === "parallel_split",
      ),
    ).toBe(false);
    expect(branch).toMatchObject({
      kind: "activity",
      name: "New activity",
    });
    expect(input.definition.edges).toEqual([
      { from: "start", to: "work" },
      { from: "work", to: "end" },
      { from: "work", to: branch?.key },
    ]);
  });
});

describe("WorkflowPage sections", () => {
  // Roles describe the workflow, not the selected node, so they are a tab
  // beside the graph rather than a second panel behind a segmented switch in
  // the node inspector. That switch sat directly above the node's own tab row
  // and read as a tab bar nobody had explained.
  it("keeps the graph and roles as the only editor tabs", async () => {
    const user = userEvent.setup();
    renderPage();

    // Workflow-level acceptance is gone: reviewing is a per-node concern, and
    // a second sign-off gate meant the same idea explained twice.
    expect((await screen.findAllByRole("tab")).map((tab) => tab.textContent))
      .toEqual(["Graph", "Roles"]);

    await user.click(screen.getByRole("tab", { name: "Roles" }));
    expect(await screen.findByText("Role editor")).toBeInTheDocument();
    expect(screen.queryByText("Node inspector")).toBeNull();

    // And back, so opening a section is not a one-way door out of the graph.
    await user.click(screen.getByRole("tab", { name: "Graph" }));
    expect(await screen.findByText("Node inspector")).toBeInTheDocument();
  });
});

describe("WorkflowPage node deletion", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  // Deleting rewires the edges around the node and the editor has no undo, so
  // the icon in the panel header asks before it acts.
  it("does not remove the node until the deletion is confirmed", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Work node" }));
    await user.click(screen.getByRole("button", { name: "Delete node" }));

    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    // Nothing changed, so there is nothing to save.
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Delete node" }));
    await user.click(
      within(await screen.findByRole("alertdialog"))
        .getByRole("button", { name: "Remove" }),
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    const input = mocks.save.mock.calls[0]![0] as {
      definition: typeof definition;
    };
    expect(input.definition.nodes.map((node) => node.key))
      .toEqual(["start", "end"]);
  });

  it("does not offer deletion for Start or End", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Start node" }));
    expect(screen.queryByRole("button", { name: "Delete node" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "End node" }));
    expect(screen.queryByRole("button", { name: "Delete node" })).toBeNull();
  });
});
