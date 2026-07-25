// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { NewWorkflowDialog } from "./workflows-page";

const mocks = vi.hoisted(() => ({
  createWorkflow: vi.fn(),
  navigate: vi.fn(),
}));

const definition = {
  schema_version: 1,
  name: "Delivery workflow",
  applies_to: { kind: "issue" },
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
  status: "published",
  name: "Delivery workflow",
};

const templateDetail = {
  template: templateSummary,
  versions: [{
    id: "version-1",
    version: 1,
    status: "published",
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
            { user_id: "user-1", name: "Current member" },
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
        data: { templates: [templateSummary] },
        isLoading: false,
        isError: false,
      };
    },
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
      workflowDetail: (id: string) => `/workspace/workflows/${id}`,
    }),
  };
});

vi.mock("@multica/core/workflows", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/workflows")>();
  return {
    ...actual,
    useCreateWorkflow: () => ({
      mutate: (input: unknown) => mocks.createWorkflow(input),
      isPending: false,
    }),
  };
});

vi.mock("../navigation", async () => {
  const React = await import("react");
  return {
    AppLink: ({
      href,
      children,
    }: {
      href: string;
      children: ReactNode;
    }) => React.createElement("a", { href }, children),
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
          template_id: "template-1",
          template_version_id: "version-1",
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
