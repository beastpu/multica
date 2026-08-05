// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import type { Workflow, WorkflowVersion } from "@multica/core/workflows";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enCommon from "../locales/en/common.json";
import enWorkflows from "../locales/en/workflows.json";
import { defaultRunTitle, WorkflowRunDialog } from "./workflow-run-dialog";

const mocks = vi.hoisted(() => ({
  // What the detail query has cached — deliberately stale, the way it is right
  // after a save whose invalidation has not landed yet.
  versions: { current: [] as WorkflowVersion[] },
}));

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useQuery: (options: { queryKey?: unknown[] }) => {
      const key = JSON.stringify(options.queryKey ?? []);
      if (key.includes("workflow") && !key.includes("member") &&
        !key.includes("agent") && !key.includes("squad")) {
        return {
          data: { workflow: workflowFixture, versions: mocks.versions.current },
          isLoading: false,
        };
      }
      return { data: [], isLoading: false };
    },
  };
});

vi.mock("@multica/core/workflows", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/workflows")>()),
  useRunWorkflow: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: unknown) => unknown) =>
    selector({ user: { id: "member-1" } }),
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));

vi.mock("@multica/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/paths")>()),
  useWorkspacePaths: () => ({
    workflowRun: (id: string) => `/workspace/workflows/runs/${id}`,
  }),
}));

vi.mock("../navigation", () => ({ useNavigation: () => ({ push: vi.fn() }) }));

const workflowFixture = {
  id: "workflow-1",
  name: "Defect fix",
  status: "published",
} as unknown as Workflow;

function version(id: string, number: number): WorkflowVersion {
  return {
    id,
    version: number,
    change_summary: "",
    definition: {
      schema_version: 1,
      name: "Defect fix",
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
      acceptance: {},
    },
  } as unknown as WorkflowVersion;
}

describe("WorkflowRunDialog version selection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.versions.current = [version("v1", 1), version("v2", 2)];
  });

  function dialog(preferred: WorkflowVersion | null, open: boolean) {
    return (
      <I18nProvider
        locale="en"
        resources={{ en: { common: enCommon, workflows: enWorkflows } }}
      >
        <WorkflowRunDialog
          workflow={workflowFixture}
          preferredVersion={preferred}
          open={open}
          onOpenChange={() => {}}
        />
      </I18nProvider>
    );
  }

  const versionSelect = () =>
    screen.getByLabelText(/version/i) as HTMLSelectElement;

  // "Save and run" hands over the version it just wrote, before the detail
  // query has refetched. The dialog stays mounted across opens, so it has to
  // prefer that version over both its own previous selection and the stale
  // cached list — running a superseded definition would be silent.
  it("runs the just-saved version after the dialog has been opened before", () => {
    const { rerender } = render(dialog(mocks.versions.current[1]!, true));
    expect(versionSelect().value).toBe("v2");

    rerender(dialog(null, false));

    // The save wrote v3; the cached list still ends at v2 until it refetches.
    const saved = version("v3", 3);
    rerender(dialog(saved, true));
    expect(versionSelect().value).toBe("v3");
  });

  it("falls back to the newest known version when none is preferred", () => {
    render(dialog(null, true));
    expect(versionSelect().value).toBe("v2");
  });
});

describe("WorkflowRunDialog naming", () => {
  // Every run opened prefilled with the workflow's own name, so a workflow's
  // history was a column of identical titles and nothing on screen said which
  // run was which.
  it("numbers a new run after the ones already recorded", () => {
    expect(defaultRunTitle("Defect fix", 0)).toBe("Defect fix #1");
    expect(defaultRunTitle("Defect fix", 4)).toBe("Defect fix #5");
  });

  it("prefills the run name with the next number", () => {
    render(
      <I18nProvider
        locale="en"
        resources={{ en: { common: enCommon, workflows: enWorkflows } }}
      >
        <WorkflowRunDialog
          workflow={{ ...workflowFixture, run_count: 2 } as Workflow}
          open
          onOpenChange={() => {}}
        />
      </I18nProvider>,
    );

    expect(screen.getByLabelText("Run name")).toHaveValue("Defect fix #3");
  });
});
