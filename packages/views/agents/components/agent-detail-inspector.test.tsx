// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Agent } from "@multica/core/types";
import { beforeEach, describe, expect, it, vi } from "vitest";
import enAgents from "../../locales/en/agents.json";
import enCommon from "../../locales/en/common.json";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };
const fileUploadMock = vi.hoisted(() => ({
  upload: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: { getBaseUrl: () => "" },
}));

vi.mock("@multica/core/hooks/use-file-upload", async () => {
  const actual = await vi.importActual("@multica/core/hooks/use-file-upload");
  return {
    ...actual,
    useFileUpload: () => ({ upload: fileUploadMock.upload, uploading: false }),
  };
});

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => (
    <img
      alt="Stale Agent"
      src="https://cdn.example.com/old-agent.png"
    />
  ),
}));

vi.mock("./inspector/concurrency-picker", () => ({
  ConcurrencyPicker: () => <span>concurrency-picker</span>,
}));
vi.mock("./inspector/model-picker", () => ({
  ModelPicker: () => <span>model-picker</span>,
}));
vi.mock("./inspector/runtime-picker", () => ({
  RuntimePicker: () => <span>runtime-picker</span>,
}));
vi.mock("./inspector/skill-attach", () => ({
  SkillAttach: () => <span>skill-attach</span>,
}));
vi.mock("./inspector/thinking-prop-row", () => ({
  ThinkingPropRow: () => <span>thinking-prop-row</span>,
}));
vi.mock("./inspector/visibility-picker", () => ({
  VisibilityPicker: () => <span>visibility-picker</span>,
}));
vi.mock("../../settings/components/lark-tab", () => ({
  LarkAgentBindButton: () => null,
}));
vi.mock("../../settings/components/slack-tab", () => ({
  SlackAgentBindButton: () => null,
}));

import { AgentDetailInspector } from "./agent-detail-inspector";

const baseAgent: Agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  name: "Fresh Agent",
  description: "",
  instructions: "",
  avatar_url: "https://cdn.example.com/new-agent.png",
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
  permission_mode: "public_to",
  invocation_targets: [{ target_type: "workspace", target_id: null }],
  status: "idle",
  max_concurrent_tasks: 1,
  model: "",
  owner_id: "user-1",
  skills: [],
  created_at: "2026-05-28T00:00:00Z",
  updated_at: "2026-05-28T00:00:00Z",
  archived_at: null,
  archived_by: null,
};

function renderInspector(canEdit: boolean, onUpdate = vi.fn().mockResolvedValue(undefined)) {
  const view = render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <AgentDetailInspector
        agent={baseAgent}
        runtime={null}
        owner={null}
        presence={null}
        runtimes={[]}
        members={[]}
        currentUserId={null}
        canEdit={canEdit}
        onUpdate={onUpdate}
        onShowIntegrations={vi.fn()}
      />
    </I18nProvider>,
  );
  return { ...view, onUpdate };
}

describe("AgentDetailInspector avatar preview", () => {
  beforeEach(() => {
    fileUploadMock.upload.mockReset();
  });

  it.each([true, false])(
    "renders the latest agent avatar from the detail record when canEdit=%s",
    (canEdit) => {
      renderInspector(canEdit);

      expect(screen.getByAltText("Fresh Agent")).toHaveAttribute(
        "src",
        "https://cdn.example.com/new-agent.png",
      );
      expect(screen.queryByAltText("Stale Agent")).not.toBeInTheDocument();
    },
  );

  it("persists the upload's durable URL instead of the raw storage URL", async () => {
    fileUploadMock.upload.mockResolvedValue({
      link: "https://multica-bucket.example.com/workspaces/ws-1/private.png",
      markdownLink: "/api/attachments/att-1/download",
    });
    const onUpdate = vi.fn().mockResolvedValue(undefined);
    const { container } = renderInspector(true, onUpdate);

    const input = container.querySelector<HTMLInputElement>("input[type='file']");
    expect(input).not.toBeNull();
    fireEvent.change(input!, {
      target: {
        files: [new File(["avatar"], "avatar.png", { type: "image/png" })],
      },
    });

    await waitFor(() => {
      expect(onUpdate).toHaveBeenCalledWith("agent-1", {
        avatar_url: "/api/attachments/att-1/download",
      });
    });
  });
});
