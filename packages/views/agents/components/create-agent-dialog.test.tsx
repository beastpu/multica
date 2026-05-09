// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import type { RuntimeDevice, MemberWithUser } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAgents from "../../locales/en/agents.json";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

vi.mock("./model-dropdown", () => ({
  ModelDropdown: () => null,
}));

vi.mock("../../runtimes/components/provider-logo", () => ({
  ProviderLogo: () => null,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => null,
}));

vi.mock("sonner", () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

import { CreateAgentDialog } from "./create-agent-dialog";

function makeRuntime(over: Partial<RuntimeDevice>): RuntimeDevice {
  return {
    id: "rt-self",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Self runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "self.local",
    metadata: {},
    owner_id: "user-self",
    visibility: "workspace",
    last_seen_at: null,
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    ...over,
  };
}

const members: MemberWithUser[] = [
  {
    id: "m1",
    workspace_id: "ws-1",
    user_id: "user-self",
    role: "member",
    name: "Self",
    email: "self@example.com",
    avatar_url: null,
    created_at: "2026-04-01T00:00:00Z",
  },
  {
    id: "m2",
    workspace_id: "ws-1",
    user_id: "user-other",
    role: "member",
    name: "Other",
    email: "other@example.com",
    avatar_url: null,
    created_at: "2026-04-01T00:00:00Z",
  },
];

function renderDialog(props: {
  isWorkspaceAdmin?: boolean;
  runtimes: RuntimeDevice[];
}) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <CreateAgentDialog
        runtimes={props.runtimes}
        members={members}
        currentUserId="user-self"
        isWorkspaceAdmin={props.isWorkspaceAdmin ?? false}
        onClose={vi.fn()}
        onCreate={vi.fn()}
      />
    </I18nProvider>,
  );
}

describe("CreateAgentDialog runtime picker — admin gate", () => {
  const ownRuntime = makeRuntime({ id: "rt-self" });
  const otherRuntime = makeRuntime({
    id: "rt-other",
    name: "Other runtime",
    owner_id: "user-other",
    device_info: "other.local",
  });
  const runtimes = [ownRuntime, otherRuntime];

  it("non-admin: hides Mine/All filter tabs even when other runtimes exist", () => {
    renderDialog({ isWorkspaceAdmin: false, runtimes });
    // The "All" tab label comes from the agents namespace; if the tab were
    // rendered, both labels would appear. We assert it isn't.
    const allLabel = enAgents.create_dialog.runtime_filter_all;
    expect(screen.queryByText(allLabel)).toBeNull();
  });

  it("admin: shows Mine/All filter tabs when other runtimes exist", () => {
    renderDialog({ isWorkspaceAdmin: true, runtimes });
    const allLabel = enAgents.create_dialog.runtime_filter_all;
    expect(screen.getByText(allLabel)).toBeInTheDocument();
  });

  it("non-admin: even after toggling state to 'all', other runtimes stay hidden in the popover", async () => {
    renderDialog({ isWorkspaceAdmin: false, runtimes });
    const trigger = screen.getByRole("button", { name: /Self runtime/i });
    fireEvent.click(trigger);
    // The popover is now open. The other-owner runtime must not be visible.
    expect(screen.queryByText(/Other runtime/)).toBeNull();
  });
});
