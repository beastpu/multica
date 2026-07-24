// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { WorkflowStartDialog } from "./workflow-start-dialog";

const mocks = vi.hoisted(() => ({
  start: vi.fn(),
  navigate: vi.fn(),
}));

const definition = {
  schema_version: 1,
  name: "Delivery",
  applies_to: { kind: "issue" },
  roles: [{
    key: "owner",
    name: "Owner",
    required: true,
    allowed_actor_types: ["member"],
  }],
  nodes: [
    { key: "start", kind: "start", name: "Start" },
    { key: "build", kind: "activity", name: "Build" },
    { key: "end", kind: "end", name: "End" },
  ],
  edges: [
    { from: "start", to: "build" },
    { from: "build", to: "end" },
  ],
  acceptance: {},
};

const issues = [
  {
    id: "host-1",
    identifier: "MUL-1",
    title: "Existing requirement",
    parent_issue_id: null,
    status: "todo",
  },
  {
    id: "child-1",
    identifier: "MUL-2",
    title: "Child",
    parent_issue_id: "host-1",
    status: "todo",
  },
  {
    id: "done-1",
    identifier: "MUL-3",
    title: "Done",
    parent_issue_id: null,
    status: "done",
  },
];

const members = [{ user_id: "user-1", name: "Current member" }];
const emptyActors: never[] = [];
const templateDetail = {
  template: { id: "template-1", name: "Delivery" },
  versions: [{
    id: "version-1",
    version: 1,
    status: "published",
    definition,
  }],
};
const templateList = {
  templates: [{
    id: "template-1",
    name: "Delivery",
    status: "published",
  }],
};

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useQuery: (options: { queryKey?: readonly unknown[] }) => {
      const key = options.queryKey ?? [];
      if (key.includes("issues")) {
        return {
          data: issues,
          isLoading: false,
          isError: false,
        };
      }
      if (key.includes("members")) {
        return {
          data: members,
          isLoading: false,
          isError: false,
        };
      }
      if (key.includes("agents") || key.includes("squads")) {
        return { data: emptyActors, isLoading: false, isError: false };
      }
      if (key.includes("detail")) {
        return {
          data: templateDetail,
          isLoading: false,
          isError: false,
        };
      }
      return {
        data: templateList,
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
    useStartIssueWorkflow: (issueId: string) => ({
      mutate: (input: unknown) => mocks.start(issueId, input),
      isPending: false,
    }),
  };
});

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: mocks.navigate }),
}));

function wrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, workflows: enWorkflows } }}
    >
      {children}
    </I18nProvider>
  );
}

describe("WorkflowStartDialog", () => {
  beforeEach(() => {
    mocks.start.mockReset();
    mocks.navigate.mockReset();
  });

  it("starts from an eligible existing host with independent status by default", async () => {
    const user = userEvent.setup();
    render(<WorkflowStartDialog />, { wrapper });

    await user.click(
      screen.getByRole("button", { name: "Start from existing issue" }),
    );
    const dialog = await screen.findByRole("dialog");
    const host = within(dialog).getByLabelText("Host issue");
    expect(within(host).getAllByRole("option")).toHaveLength(2);
    await user.selectOptions(host, "host-1");
    expect(
      within(dialog).getByRole("radio", {
        name: /Keep issue status independent/,
      }),
    ).toBeChecked();

    await user.click(
      within(dialog).getByRole("button", { name: "Start workflow" }),
    );

    await waitFor(() => {
      expect(mocks.start).toHaveBeenCalledWith(
        "host-1",
        expect.objectContaining({
          template_id: "template-1",
          template_version_id: "version-1",
          host_status_mode: "independent",
          role_assignments: [{
            role_key: "owner",
            actor_type: "member",
            actor_id: "user-1",
            source: "user_selected",
          }],
        }),
      );
    });
  });
});
