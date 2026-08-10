// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import {
  NewWorkflowDialog,
  unusedWorkflowName,
  WorkflowsPage,
} from "./workflows-page";

const mocks = vi.hoisted(() => ({
  createWorkflow: vi.fn(),
  createTemplate: vi.fn(),
  navigate: vi.fn(),
  saveDefinition: vi.fn(),
  deleteWorkflow: vi.fn(),
  // A real mutation reports itself pending between the click and the reply.
  // With isPending pinned to false the confirmation looked fine in tests while
  // the browser left it on screen, backdrop and all.
  pending: { current: false },
  settle: { current: () => {} },
}));

const definition = {
  schema_version: 1,
  name: "Delivery workflow",
  roles: [
    {
      key: "owner",
      name: "Owner",
      required: true,
      allowed_actor_types: ["member"],
    },
    {
      key: "reviewer",
      name: "QA reviewer",
      required: true,
      allowed_actor_types: ["member"],
    },
  ],
  nodes: [
    { key: "start", kind: "start", name: "Start" },
    { key: "review", kind: "activity", name: "QA review" },
    { key: "build", kind: "activity", name: "Build" },
    { key: "end", kind: "end", name: "End" },
  ],
  edges: [
    { from: "start", to: "build" },
    { from: "build", to: "review" },
    { from: "review", to: "end" },
  ],
  acceptance: {},
};

const templateSummary = {
  id: "template-1",
  name: "Delivery workflow",
  description: "A long explanation that belongs in workflow details.",
  activity_count: 2,
  run_count: 4,
  recent_runs: [
    {
      id: "run-running",
      title: "Release run",
      status: "running",
      started_at: "2026-08-03T10:00:00.000Z",
      completed_at: null,
    },
    {
      id: "run-failed",
      title: "Failed run",
      status: "failed",
      started_at: "2026-08-02T10:00:00.000Z",
      completed_at: "2026-08-02T10:05:00.000Z",
    },
  ],
  last_published_by: null,
  last_published_at: null,
  latest_published_version: 3,
};

// The junk a workspace accumulates while learning the editor: published, never
// started. Deleting is only offered for real on this shape.
const neverRunSummary = {
  ...templateSummary,
  id: "template-2",
  name: "Unused workflow",
  run_count: 0,
  recent_runs: [],
};

// A third workflow, also with runs, so a test can confirm the second archive
// targets the row it names rather than the one before it.
const secondRunSummary = {
  ...templateSummary,
  id: "template-3",
  name: "Release workflow",
  recent_runs: [{
    id: "run-release",
    title: "Nightly release",
    status: "completed",
    started_at: "2026-08-01T10:00:00.000Z",
    completed_at: "2026-08-01T10:30:00.000Z",
  }],
};

const templateDetail = {
  workflow: templateSummary,
  versions: [{
    id: "version-1",
    version: 1,
    revision: 1,
    change_summary: "Initial release",
    definition,
  }],
};

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useQuery: (options: { queryKey?: readonly unknown[] }) => {
      const key = options.queryKey ?? [];
      if (key.includes("members")) {
        return {
          data: [
            { user_id: "user-1", name: "Current member", role: "admin" },
            { user_id: "user-2", name: "QA member" },
          ],
          isLoading: false,
          isError: false,
        };
      }
      if (key.includes("agents") || key.includes("squads")) {
        return { data: [], isLoading: false, isError: false };
      }
      if (key.includes("detail")) {
        return { data: templateDetail, isLoading: false, isError: false };
      }
      return {
        data: { workflows: [templateSummary, neverRunSummary, secondRunSummary] },
        isLoading: false,
        isError: false,
      };
    },
    useInfiniteQuery: () => ({
      data: { pages: [{ instances: [], total: 0 }] },
      isLoading: false,
      isError: false,
      hasNextPage: false,
      isFetchingNextPage: false,
      fetchNextPage: vi.fn(),
    }),
  };
});

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (
    selector: (state: { user: { id: string } }) => unknown,
  ) => selector({ user: { id: "user-1" } }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return {
    ...actual,
    useWorkspacePaths: () => ({
      workflow: (id: string) => `/workspace/workflows/${id}`,
      workflowRun: (id: string) => `/workspace/workflows/runs/${id}`,
      workflowRuns: (workflowId?: string) =>
        workflowId
          ? `/workspace/workflows/runs?workflow=${workflowId}`
          : "/workspace/workflows/runs",
    }),
  };
});

vi.mock("@multica/core/workflows", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/workflows")>();
  return {
    ...actual,
    // The dialog starts a run; creating a workflow definition is a separate
    // hook now that "template" is gone.
    useCreateWorkflowRun: () => ({
      mutate: (input: unknown) => mocks.createWorkflow(input),
      isPending: false,
    }),
    useCreateWorkflow: () => ({
      mutate: (input: unknown, options?: { onSuccess?: (r: unknown) => void }) => {
        mocks.createTemplate(input);
        options?.onSuccess?.({ workflow: { id: "created-1" } });
      },
      isPending: false,
    }),
    useCopyWorkflow: () => ({ mutate: vi.fn(), isPending: false }),
    useDeleteWorkflow: () => ({
      mutate: (id: string, options?: { onSuccess?: () => void }) => {
        mocks.deleteWorkflow(id);
        mocks.pending.current = true;
        mocks.settle.current = () => {
          mocks.pending.current = false;
          options?.onSuccess?.();
        };
      },
      isPending: mocks.pending.current,
    }),
    useCreateWorkflowTemplateFromBuiltin: () => ({
      mutate: vi.fn(),
      isPending: false,
      isError: false,
    }),
    useRunWorkflow: () => ({ mutate: vi.fn(), isPending: false }),
    useSaveWorkflowDefinition: () => ({
      mutate: (input: unknown) => mocks.saveDefinition(input),
      isPending: false,
    }),
  };
});

vi.mock("./workflow-start-dialog", () => ({
  WorkflowStartDialog: () => <button type="button">Start workflow</button>,
}));

vi.mock("../navigation", async () => {
  const React = await import("react");
  return {
    // Props beyond href/children have to survive: a row action renders its
    // Button through AppLink, and the label that names it for a reader
    // arrives as one of them.
    AppLink: ({
      href,
      children,
      ...rest
    }: {
      href: string;
      children: ReactNode;
    }) => React.createElement("a", { href, ...rest }, children),
    useNavigation: () => ({ push: mocks.navigate }),
  };
});

function wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider
      locale="en"
      resources={{
        en: { common: enCommon, workflows: enWorkflows },
      }}
    >
      {children}
    </I18nProvider>
  );
}

describe("NewWorkflowDialog", () => {
  beforeEach(() => {
    mocks.createWorkflow.mockReset();
    mocks.navigate.mockReset();
  });

  it("previews the version, requires roles, and defaults a new host to managed", async () => {
    const user = userEvent.setup();
    render(<NewWorkflowDialog />, { wrapper });

    await user.click(screen.getByRole("button", { name: "New workflow" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Build")).toBeInTheDocument();
    expect(within(dialog).getByText("QA review")).toBeInTheDocument();
    expect(
      within(dialog).getAllByRole("listitem").map((item) => item.textContent),
    ).toEqual(["Build", "→QA review"]);
    expect(
      within(dialog).getByRole("radio", {
        name: /Let workflow manage issue status/,
      }),
    ).toBeChecked();

    await user.type(
      within(dialog).getByRole("textbox", { name: "Requirement title" }),
      "Release 2.0",
    );
    await user.type(
      within(dialog).getByRole("textbox", { name: "Requirement description" }),
      "Ship the page",
    );
    const submit = within(dialog).getByRole("button", {
      name: "New workflow",
    });
    expect(submit).toBeDisabled();

    await user.selectOptions(
      within(dialog).getByLabelText(/^QA reviewer/),
      "member:user-2",
    );
    expect(submit).toBeEnabled();
    await user.click(submit);

    await waitFor(() => {
      expect(mocks.createWorkflow).toHaveBeenCalledWith(
        expect.objectContaining({
          title: "Release 2.0",
          description: "Ship the page",
          workflow_id: "template-1",
          workflow_version_id: "version-1",
          host_status_mode: "managed",
          role_assignments: [
            {
              role_key: "owner",
              actor_type: "member",
              actor_id: "user-1",
              source: "user_selected",
            },
            {
              role_key: "reviewer",
              actor_type: "member",
              actor_id: "user-2",
              source: "user_selected",
            },
          ],
        }),
      );
    });
  });
});

describe("WorkflowsPage", () => {
  beforeEach(() => {
    mocks.saveDefinition.mockReset();
    mocks.deleteWorkflow.mockReset();
    mocks.pending.current = false;
    mocks.settle.current = () => {};
  });

  // Roles were their own tab with their own workflow picker and their own
  // save button, editing the same definition the editor page edits. They now
  // live inside that editor, which leaves the list page with the two things
  // that are genuinely list-level: the workflows, and what you can start from.
  it("keeps definitions and starter templates as the only page tabs", () => {
    render(<WorkflowsPage />, { wrapper });

    expect(
      screen.getAllByRole("tab").map((tab) => tab.textContent),
    ).toEqual(["Workflows", "Starter templates"]);
    expect(screen.queryByRole("tab", { name: "Roles" })).toBeNull();
    expect(screen.queryByRole("tab", { name: "Active" })).toBeNull();
    expect(screen.queryByRole("tab", { name: "Related to me" })).toBeNull();
    expect(screen.queryByRole("tab", { name: "Completed" })).toBeNull();
  });

  it("shows workflow definitions in a compact operational table", () => {
    render(<WorkflowsPage />, { wrapper });

    expect(screen.getByRole("columnheader", { name: "Name" }))
      .toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Nodes" }))
      .toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Run history" }))
      .toBeInTheDocument();
    // The row's name is the operational destination: what this workflow has
    // done. Editing its definition lives in the overflow menu.
    expect(screen.getByRole("link", { name: "Delivery workflow" }))
      .toHaveAttribute(
        "href",
        "/workspace/workflows/runs?workflow=template-1",
      );
    // The latest run is named in the row; the rest live behind the count,
    // which is a link to this workflow's history rather than a row of dots
    // that said only "a run happened".
    expect(
      screen.getByRole("link", { name: /Running/ }),
    ).toHaveAttribute("href", "/workspace/workflows/runs/run-running");
    expect(
      screen.getAllByRole("link", { name: "4 runs" })[0],
    ).toHaveAttribute("href", "/workspace/workflows/runs?workflow=template-1");
    // The history is reachable from the page itself, not only from a workflow
    // that happens to have run more than once.
    expect(screen.getByRole("button", { name: "Workflow runs" }))
      .toBeInTheDocument();
  });

  it("keeps secondary workflow metadata out of the operational list", () => {
    render(<WorkflowsPage />, { wrapper });

    expect(screen.queryByText("v3")).toBeNull();
    expect(
      screen.queryByText("A long explanation that belongs in workflow details."),
    ).toBeNull();
    expect(screen.getAllByRole("button", { name: "New workflow" }))
      .toHaveLength(1);
  });

  it("creates a workflow whose name does not collide with the list", async () => {
    const user = userEvent.setup();
    render(<WorkflowsPage />, { wrapper });

    await user.click(screen.getByRole("button", { name: "New workflow" }));

    // Nothing in the list is called "New workflow", so the starter name is
    // free and used as-is; unusedWorkflowName covers the collision cases.
    expect(mocks.createTemplate).toHaveBeenCalledWith(
      expect.objectContaining({ name: "New workflow" }),
    );
  });

  // Running and editing are the reasons to be on this page. Editing used to be
  // one menu deep, which put the routine act behind the same click as the
  // destructive one.
  it("puts run and edit on the row and leaves the rest in the overflow", () => {
    render(<WorkflowsPage />, { wrapper });

    const editLinks = screen.getAllByRole("link", { name: "Edit workflow" });
    expect(editLinks[0]).toHaveAttribute(
      "href",
      "/workspace/workflows/template-1",
    );
    expect(screen.getAllByRole("button", { name: "Run" })).toHaveLength(3);
    // Copy and delete are not on the row — they are reached through the menu.
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Copy" })).toBeNull();
  });

  // The column is called "run history" and named no run. Status and time say
  // how the last one went; only the name says which one it was.
  it("names the latest run in the history column", () => {
    render(<WorkflowsPage />, { wrapper });

    expect(
      screen.getByRole("link", { name: /Release run/ }),
    ).toHaveAttribute("href", "/workspace/workflows/runs/run-running");
  });

  it("deletes a workflow that has never run, after confirming", async () => {
    const user = userEvent.setup();
    render(<WorkflowsPage />, { wrapper });

    await user.click(screen.getAllByRole("button", { name: "More actions" })[1]!);
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));

    expect(
      screen.getByText(/removed for good/),
    ).toBeInTheDocument();
    expect(mocks.deleteWorkflow).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(mocks.deleteWorkflow).toHaveBeenCalledWith("template-2");
  });

  // Deleting takes the run history with it, so the confirmation says how many
  // runs go — that count is the whole difference between this and deleting a
  // workflow nobody ever ran.
  it("says how much history goes with a workflow that has runs", async () => {
    const user = userEvent.setup();
    render(<WorkflowsPage />, { wrapper });

    await user.click(screen.getAllByRole("button", { name: "More actions" })[0]!);
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));

    expect(screen.getByText(/finished runs are removed/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(mocks.deleteWorkflow).toHaveBeenCalledWith("template-1");
  });

  // The mutation used to take its id when the hook ran, which is fine on a page
  // that edits one workflow and wrong on a list: the second confirmation acted
  // on whatever the first one had, the server answered for it, and the row the
  // reader actually picked was left untouched.
  it("deletes the workflow named in the confirmation, not the previous one", async () => {
    const user = userEvent.setup();
    render(<WorkflowsPage />, { wrapper });

    const openDeleteFor = async (index: number) => {
      await user.click(
        screen.getAllByRole("button", { name: "More actions" })[index]!,
      );
      await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    };

    await openDeleteFor(0);
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(mocks.deleteWorkflow).toHaveBeenLastCalledWith("template-1");
    mocks.settle.current();

    // Same dialog, different row. Nothing about the first choice may survive.
    await openDeleteFor(2);
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(mocks.deleteWorkflow).toHaveBeenLastCalledWith("template-3");
  });

  // The confirmation has to name the workflow: without it a reader could not
  // tell which one they were about to delete, which is exactly how the
  // stale-id bug above stayed invisible while it fired six times.
  it("names the workflow in the delete confirmation", async () => {
    const user = userEvent.setup();
    render(<WorkflowsPage />, { wrapper });

    await user.click(screen.getAllByRole("button", { name: "More actions" })[0]!);
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/Delivery workflow/)).toBeInTheDocument();
  });

  // Confirming left the dialog on screen with its backdrop still swallowing
  // clicks, so the row you picked next never registered and the next
  // confirmation was still about the previous workflow. One deletion per page
  // load was all the list could do.
  it("closes the confirmation as soon as it is confirmed", async () => {
    const user = userEvent.setup();
    render(<WorkflowsPage />, { wrapper });

    await user.click(screen.getAllByRole("button", { name: "More actions" })[0]!);
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    await user.click(screen.getByRole("button", { name: "Delete" }));

    // Still in flight — this is the window the dialog used to get stuck in.
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).toBeNull();
    });
  });

  it("confirms a second workflow after the first one is done", async () => {
    const user = userEvent.setup();
    render(<WorkflowsPage />, { wrapper });

    const confirmFor = async (index: number, action: string) => {
      await user.click(
        screen.getAllByRole("button", { name: "More actions" })[index]!,
      );
      await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
      await user.click(screen.getByRole("button", { name: action }));
      await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
      mocks.settle.current();
    };

    await confirmFor(0, "Delete");
    await confirmFor(1, "Delete");

    expect(mocks.deleteWorkflow).toHaveBeenCalledWith("template-1");
    expect(mocks.deleteWorkflow).toHaveBeenCalledWith("template-2");
  });

  it("suffixes the starter name until it is free", () => {
    expect(unusedWorkflowName("New workflow", new Set())).toBe("New workflow");
    expect(unusedWorkflowName("New workflow", new Set(["New workflow"])))
      .toBe("New workflow 2");
    // Skips over suffixes already in use rather than colliding again.
    expect(unusedWorkflowName(
      "New workflow",
      new Set(["New workflow", "New workflow 2", "New workflow 3"]),
    )).toBe("New workflow 4");
  });
});
