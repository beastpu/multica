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
  deleteWorkflow: vi.fn(),
  navigate: vi.fn(),
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
    latest_published_version_id: "version-1",
    created_by: "user-1",
    created_at: "2026-07-23T00:00:00Z",
    updated_at: "2026-07-23T00:00:00Z",
    latest_published_version: 1,
    activity_count: 1,
    run_count: 0,
    recent_runs: [],
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

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    workflows: () => "/workspace/workflows",
  }),
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
  useDeleteWorkflow: () => ({
    isPending: false,
    mutate: (
      input: unknown,
      options?: { onSuccess?: () => void },
    ) => {
      mocks.deleteWorkflow(input);
      options?.onSuccess?.();
    },
  }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: mocks.navigate }),
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

vi.mock("./workflow-run-dialog", () => ({
  WorkflowRunDialog: ({
    open,
    preferredVersion,
  }: {
    open: boolean;
    preferredVersion?: { id: string } | null;
  }) => open
    ? (
      <div role="dialog" data-version-id={preferredVersion?.id}>
        Run workflow
      </div>
    )
    : null,
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
      base_version_id?: string;
    };
    expect(input.definition.nodes).toHaveLength(4);
    // Version numbers never collide, so the only thing that can tell the
    // server this edit started from a version that is no longer live is the
    // editor naming it.
    expect(input.base_version_id).toBe("version-1");
  });

  it("returns to the workflow list from the editor header", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", {
      name: "Back to workflows",
    }));

    expect(mocks.navigate).toHaveBeenCalledWith("/workspace/workflows");
  });

  it("opens the run dialog directly for the saved version", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Run" }));

    expect(await screen.findByRole("dialog")).toHaveAttribute(
      "data-version-id",
      "version-1",
    );
    expect(mocks.save).not.toHaveBeenCalled();
  });

  it("saves pending edits before opening the run dialog", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", {
      name: "Insert activity between Work and End",
    }));
    await user.click(screen.getByRole("button", { name: "Save and run" }));

    await waitFor(() => expect(mocks.save).toHaveBeenCalledTimes(1));
    expect(await screen.findByRole("dialog")).toHaveAttribute(
      "data-version-id",
      "version-1",
    );
  });

  it("asks before returning with unsaved edits", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", {
      name: "Insert activity between Work and End",
    }));
    await user.click(screen.getByRole("button", {
      name: "Back to workflows",
    }));

    expect(mocks.navigate).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", {
      name: "Discard and leave",
    }));
    expect(mocks.navigate).toHaveBeenCalledWith("/workspace/workflows");
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

  it("does not delete until the destructive action is confirmed", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(
      await screen.findByRole("button", { name: "More actions" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Delete" }),
    );
    expect(mocks.deleteWorkflow).not.toHaveBeenCalled();

    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(mocks.deleteWorkflow).toHaveBeenCalledTimes(1));
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
      // A new activity gets its own issue: that is where the work is stated,
      // where its executor can ask, and the id every `multica workflow`
      // command resolves itself from. The node waits for that issue, or it
      // would complete with the work untouched. Manual confirmation is
      // represented by a reviewer, not a second completion mode, so authored
      // nodes always use the automatic engine mode.
      issue_policy: "auto",
      completion: { mode: "automatic", required_issue_outcome: "done" },
    });
    // Auto names its own issue — the author is not asked for a title template.
    expect(inserted).not.toHaveProperty("issue_templates");
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

  it("does not show gateway routing controls for ordinary activity edges", async () => {
    renderPage();

    expect(await screen.findByText("Node inspector")).toBeInTheDocument();
    expect(screen.queryByText("Default branch")).toBeNull();
    expect(screen.queryByRole("button", { name: "Node choice" })).toBeNull();
  });

  // The connections help ends by saying the section configures branch rules,
  // which an activity's section does not. On an activity it was four lines
  // about a capability that is not there and a canvas that is.
  it("explains branch rules only where they can be edited", async () => {
    const user = userEvent.setup();
    renderPage();

    await user.click(await screen.findByRole("button", { name: "Work node" }));
    expect(screen.queryByText(/Configure branch rules here/)).toBeNull();
    // The section itself stays: it still names the predecessors and lists the
    // outgoing edges.
    expect(screen.getByText("Connections")).toBeInTheDocument();
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
