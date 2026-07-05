import { type ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import { toast } from "sonner";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockPutCapability = vi.hoisted(() => vi.fn());
const mockDeleteCapability = vi.hoisted(() => vi.fn());
const mockInvalidate = vi.hoisted(() => vi.fn());
const mockPush = vi.hoisted(() => vi.fn());

// null = capability not configured (server 404 mapped to null by the client).
const capabilityRef = vi.hoisted(() => ({ current: null as unknown }));
const agentsRef = vi.hoisted(() => ({ current: [] as unknown[] }));

function configuredCapability() {
  return {
    capability: "p4_assessment",
    agent_id: "agent-1",
    agent_name: "Assessor",
    project_id: null,
    max_concurrent_tasks: 1,
    created_at: "2026-07-01T00:00:00Z",
  };
}

// Only the fields the section reads — the real Agent type is much wider.
function workspaceAgents() {
  return [
    { id: "agent-1", name: "Assessor", runtime_mode: "local", archived_at: null },
    { id: "agent-2", name: "Local Bot", runtime_mode: "local", archived_at: null },
    { id: "agent-3", name: "Cloud Bot", runtime_mode: "cloud", archived_at: null },
    { id: "agent-4", name: "Old Bot", runtime_mode: "local", archived_at: "2026-01-01T00:00:00Z" },
  ];
}

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[] }) => {
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("agents")) return { data: agentsRef.current, isFetching: false };
    if (key.includes("capabilities")) return { data: capabilityRef.current, isFetching: false };
    return { data: undefined, isFetching: false };
  },
  useQueryClient: () => ({ invalidateQueries: mockInvalidate }),
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({ agents: () => "/acme/agents" }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  CAPABILITY_P4_ASSESSMENT: "p4_assessment",
  agentListOptions: (wsId: string) => ({ queryKey: ["workspaces", wsId, "agents"], queryFn: vi.fn() }),
  workspaceCapabilityOptions: (wsId: string, capability: string) => ({
    queryKey: ["workspaces", wsId, "capabilities", capability],
    queryFn: vi.fn(),
  }),
  workspaceKeys: {
    capability: (wsId: string, capability: string) =>
      ["workspaces", wsId, "capabilities", capability] as const,
  },
}));

vi.mock("@multica/core/api", () => ({
  api: {
    putWorkspaceCapability: mockPutCapability,
    deleteWorkspaceCapability: mockDeleteCapability,
  },
}));

vi.mock("../../navigation", () => ({
  useNavigation: () => ({ push: mockPush }),
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

import { AssessmentCapabilitySection } from "./assessment-capability-section";

const STR = enSettings.assessment;

const TEST_RESOURCES = {
  en: { common: enCommon, settings: enSettings },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function resetFixtures() {
  vi.clearAllMocks();
  capabilityRef.current = configuredCapability();
  agentsRef.current = workspaceAgents();
  mockPutCapability.mockResolvedValue(configuredCapability());
  mockDeleteCapability.mockResolvedValue(undefined);
  mockInvalidate.mockResolvedValue(undefined);
}

describe("AssessmentCapabilitySection", () => {
  beforeEach(resetFixtures);

  it("renders the configured agent name as the status badge and in the selector", () => {
    render(<AssessmentCapabilitySection />, { wrapper: I18nWrapper });

    // Badge + select trigger both show the designated agent.
    expect(screen.getAllByText("Assessor").length).toBeGreaterThanOrEqual(2);
    expect(screen.queryByText(STR.not_configured)).toBeNull();
    expect(screen.getByRole("button", { name: STR.clear })).toBeTruthy();
  });

  it("renders the empty state when the capability is not configured (404 → null)", () => {
    capabilityRef.current = null;
    render(<AssessmentCapabilitySection />, { wrapper: I18nWrapper });

    expect(screen.getByText(STR.not_configured)).toBeTruthy();
    expect(screen.getByText(STR.select_placeholder)).toBeTruthy();
    // Nothing to clear yet; Save stays disabled until an agent is picked.
    expect(screen.queryByRole("button", { name: STR.clear })).toBeNull();
    const save = screen.getByRole("button", { name: STR.save }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
  });

  it("saves the selected agent via PUT and invalidates the capability query", async () => {
    const user = userEvent.setup();
    render(<AssessmentCapabilitySection />, { wrapper: I18nWrapper });

    await user.click(screen.getByLabelText(STR.agent_label));
    await user.click(await screen.findByRole("option", { name: "Local Bot" }));
    await user.click(screen.getByRole("button", { name: STR.save }));

    await waitFor(() => expect(mockPutCapability).toHaveBeenCalledTimes(1));
    expect(mockPutCapability).toHaveBeenCalledWith("workspace-1", "p4_assessment", {
      agent_id: "agent-2",
    });
    expect(mockInvalidate).toHaveBeenCalledWith({
      queryKey: ["workspaces", "workspace-1", "capabilities", "p4_assessment"],
    });
    expect(toast.success).toHaveBeenCalledWith(STR.saved);
  });

  it("disables agents on a non-local runtime in the selector, hides archived agents", async () => {
    const user = userEvent.setup();
    render(<AssessmentCapabilitySection />, { wrapper: I18nWrapper });

    await user.click(screen.getByLabelText(STR.agent_label));
    const cloudOption = await screen.findByRole("option", { name: new RegExp("Cloud Bot") });
    expect(cloudOption.getAttribute("aria-disabled")).toBe("true");
    expect(screen.getByText(STR.agent_not_local)).toBeTruthy();
    expect(screen.queryByRole("option", { name: /Old Bot/ })).toBeNull();
  });

  it("clears the capability via DELETE and invalidates", async () => {
    const user = userEvent.setup();
    render(<AssessmentCapabilitySection />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("button", { name: STR.clear }));

    await waitFor(() => expect(mockDeleteCapability).toHaveBeenCalledTimes(1));
    expect(mockDeleteCapability).toHaveBeenCalledWith("workspace-1", "p4_assessment");
    expect(mockInvalidate).toHaveBeenCalledWith({
      queryKey: ["workspaces", "workspace-1", "capabilities", "p4_assessment"],
    });
    expect(toast.success).toHaveBeenCalledWith(STR.cleared);
  });

  it("maps a known server rejection reason to readable copy", async () => {
    const user = userEvent.setup();
    mockPutCapability.mockRejectedValue(
      new Error("capability agent must run on a daemon (local) runtime"),
    );
    render(<AssessmentCapabilitySection />, { wrapper: I18nWrapper });

    await user.click(screen.getByLabelText(STR.agent_label));
    await user.click(await screen.findByRole("option", { name: "Local Bot" }));
    await user.click(screen.getByRole("button", { name: STR.save }));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(STR.error_agent_runtime_not_local),
    );
  });

  it("surfaces an unknown server reason as a generic failure carrying the raw text", async () => {
    const user = userEvent.setup();
    mockPutCapability.mockRejectedValue(new Error("some new backend reason"));
    render(<AssessmentCapabilitySection />, { wrapper: I18nWrapper });

    await user.click(screen.getByLabelText(STR.agent_label));
    await user.click(await screen.findByRole("option", { name: "Local Bot" }));
    await user.click(screen.getByRole("button", { name: STR.save }));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        STR.save_failed_reason.replace("{{reason}}", "some new backend reason"),
      ),
    );
  });

  it("navigates to the agents page from the create button", async () => {
    const user = userEvent.setup();
    render(<AssessmentCapabilitySection />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("button", { name: STR.create_agent }));
    expect(mockPush).toHaveBeenCalledWith("/acme/agents");
  });
});
