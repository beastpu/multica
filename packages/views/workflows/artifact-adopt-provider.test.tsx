// @vitest-environment jsdom

import { I18nProvider } from "@multica/core/i18n/react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import enWorkflows from "../locales/en/workflows.json";
import { ArtifactAdoptProvider } from "./artifact-adopt-provider";
import { useAttachmentActions } from "../editor";

const state = vi.hoisted(() => ({
  issue: null as unknown,
  node: null as unknown,
  artifacts: { artifacts: [] as { artifact_key: string }[] },
  submit: vi.fn(),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: ({ queryKey }: { queryKey: unknown[] }) => {
    const key = JSON.stringify(queryKey);
    if (key.includes("artifacts")) return { data: state.artifacts };
    if (key.includes("node")) return { data: state.node };
    return { data: state.issue };
  },
}));

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/issues/queries", () => ({
  issueDetailOptions: (_ws: string, id: string) => ({ queryKey: ["issue", id] }),
}));
vi.mock("@multica/core/workflows", () => ({
  useSubmitWorkflowArtifact: () => ({
    mutate: state.submit,
    isPending: false,
    isError: false,
  }),
  workflowNodeOptions: (_ws: string, id: string) => ({ queryKey: ["node", id] }),
  workflowNodeArtifactsOptions: (_ws: string, id: string) => ({
    queryKey: ["node", id, "artifacts"],
  }),
}));

// Probe standing in for an attachment card: it renders whatever the surface
// contributed, which is exactly what the card does.
function Probe() {
  const actions = useAttachmentActions("att-1");
  return (
    <>
      {actions.map((action) => (
        <button
          key={action.id}
          type="button"
          onClick={() => action.onSelect("att-1")}
        >
          {action.label}
        </button>
      ))}
    </>
  );
}

function renderProvider() {
  return render(
    <I18nProvider locale="en" resources={{ en: { workflows: enWorkflows } }}>
      <ArtifactAdoptProvider issueId="issue-1">
        <Probe />
      </ArtifactAdoptProvider>
    </I18nProvider>,
  );
}

function nodeWithArtifacts(artifacts: unknown[]) {
  return { node: { definition: { artifacts } } };
}

beforeEach(() => {
  state.submit.mockClear();
  state.artifacts = { artifacts: [] };
  state.issue = {
    id: "issue-1",
    workflow_context: {
      workflow_instance_id: "run-1",
      workflow_node_instance_id: "node-1",
    },
  };
  state.node = nodeWithArtifacts([
    { key: "spec_file", name: "Spec file", kind: "attachment" },
  ]);
});

describe("ArtifactAdoptProvider", () => {
  it("offers adoption and submits the chosen slot with the attachment", async () => {
    const user = userEvent.setup();
    renderProvider();

    await user.click(
      screen.getByRole("button", { name: enWorkflows.workbench.adopt_as_artifact }),
    );
    await user.click(screen.getByRole("button", { name: /spec_file/ }));

    expect(state.submit).toHaveBeenCalledWith(
      expect.objectContaining({
        artifactKey: "spec_file",
        attachmentId: "att-1",
        issueId: "issue-1",
      }),
      expect.anything(),
    );
  });

  // The submit endpoint rejects an attachment sent to a document- or link-kind
  // requirement, so offering those would be a button that always fails.
  it("offers nothing when the node declares no attachment slot", () => {
    state.node = nodeWithArtifacts([
      { key: "design_doc", name: "Design doc", kind: "document" },
      { key: "pr", name: "PR", kind: "link" },
    ]);
    renderProvider();

    expect(
      screen.queryByRole("button", { name: enWorkflows.workbench.adopt_as_artifact }),
    ).toBeNull();
  });

  // Ordinary issues must be left completely alone.
  it("offers nothing outside a workflow node issue", () => {
    state.issue = { id: "issue-1", workflow_context: null };
    renderProvider();

    expect(
      screen.queryByRole("button", { name: enWorkflows.workbench.adopt_as_artifact }),
    ).toBeNull();
  });

  // Replacing keeps history server-side, but the user should know they are
  // overwriting rather than adding.
  it("flags a slot that already holds an artifact", async () => {
    state.artifacts = { artifacts: [{ artifact_key: "spec_file" }] };
    const user = userEvent.setup();
    renderProvider();

    await user.click(
      screen.getByRole("button", { name: enWorkflows.workbench.adopt_as_artifact }),
    );
    expect(
      screen.getByText(new RegExp(enWorkflows.workbench.adopt_replaces)),
    ).toBeInTheDocument();
  });
});
