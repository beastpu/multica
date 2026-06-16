import { type ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockUpdateIntegration = vi.hoisted(() => vi.fn());
const mockReplaceRoutes = vi.hoisted(() => vi.fn());
const mockSyncIntegration = vi.hoisted(() => vi.fn());
const mockInvalidate = vi.hoisted(() => vi.fn());
const mockSetQueryData = vi.hoisted(() => vi.fn());

type MemberRole = "owner" | "admin" | "member" | "guest";

const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "admin" as MemberRole }],
}));
// null = no integration saved yet (the "not configured" locked state).
const integrationRef = vi.hoisted(() => ({ current: null as unknown }));
const routesRef = vi.hoisted(() => ({ current: { routes: [] as unknown[] } }));
const statusesRef = vi.hoisted(() => ({
  current: {
    statuses: [
      { key: "open", name: "Open" },
      { key: "closed", name: "Closed" },
    ],
  },
}));

// A saved integration whose credentials are complete. work_item_types carries
// one mapped type so the collapse-by-default behavior is observable.
function configuredIntegration() {
  return {
    id: "int-1",
    enabled: true,
    project_key: "proj",
    project_name: "proj",
    plugin_id: "MII_1",
    has_plugin_secret: true,
    default_plugin_available: false,
    default_plugin_id: null,
    actor_user_key: "u1",
    assign_open_items_to_owner_agent: false,
    sync_only_workspace_member_items: false,
    work_item_types: [
      {
        type_key: "issue",
        api_name: "issue",
        name: "Defect",
        identifier_prefix: "BUG",
        project_id: "",
        status_mapping: { open: "todo" },
        reverse_status_mapping: {},
      },
    ],
    label_sync_rules: [],
    business_line_field_key: "",
    business_line_field_name: "",
    last_error: null,
    last_synced_at: "2026-06-12T00:00:00Z",
  };
}

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
    if (opts.enabled === false) return { data: undefined, isLoading: false, isFetching: false };
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("members")) return { data: membersRef.current, isFetching: false };
    if (key.includes("fp-integration")) return { data: integrationRef.current, isFetching: false };
    if (key.includes("fp-routes")) return { data: routesRef.current, isFetching: false };
    if (key.includes("fp-statuses")) return { data: statusesRef.current, isFetching: false };
    if (key.includes("fp-types")) return { data: { work_item_types: [] }, isFetching: false };
    if (key.includes("fp-fields")) return { data: { fields: [] }, isFetching: false };
    return { data: undefined, isFetching: false, refetch: vi.fn() };
  },
  useQueryClient: () => ({
    invalidateQueries: mockInvalidate,
    setQueryData: mockSetQueryData,
  }),
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"], queryFn: vi.fn() }),
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: vi.fn() }),
}));

vi.mock("@multica/core/projects", () => ({
  projectListOptions: () => ({ queryKey: ["projects"], queryFn: vi.fn() }),
}));

vi.mock("@multica/core/feishu-project/queries", () => ({
  feishuProjectIntegrationOptions: () => ({ queryKey: ["fp-integration"], queryFn: vi.fn() }),
  feishuProjectFieldsOptions: (_ws: string, _type: string, enabled: boolean) => ({
    queryKey: ["fp-fields"],
    queryFn: vi.fn(),
    enabled,
  }),
  feishuProjectRoutesOptions: (_ws: string, enabled: boolean) => ({
    queryKey: ["fp-routes"],
    queryFn: vi.fn(),
    enabled,
  }),
  feishuProjectSyncOptions: (_ws: string, enabled: boolean) => ({
    queryKey: ["fp-sync"],
    queryFn: vi.fn(),
    enabled,
  }),
  feishuProjectWorkItemTypesOptions: (_ws: string, enabled: boolean) => ({
    queryKey: ["fp-types"],
    queryFn: vi.fn(),
    enabled,
  }),
  feishuProjectIssueStatusesOptions: (_ws: string, enabled: boolean, _typeKey: string) => ({
    queryKey: ["fp-statuses"],
    queryFn: vi.fn(),
    enabled,
  }),
  feishuProjectBusinessLinesOptions: (
    _ws: string,
    _field: string,
    _type: string,
    enabled: boolean,
  ) => ({
    queryKey: ["fp-bizlines"],
    queryFn: vi.fn(),
    enabled,
  }),
  feishuProjectKeys: {
    integration: (wsId: string) => ["fp-integration", wsId],
    sync: (wsId: string) => ["fp-sync", wsId],
    routes: (wsId: string) => ["fp-routes", wsId],
    issueStatusesAll: (wsId: string) => ["fp-statuses", wsId],
    fieldsAll: (wsId: string) => ["fp-fields", wsId],
    businessLinesAll: (wsId: string) => ["fp-bizlines", wsId],
    businessLines: (wsId: string, field: string, type: string) => ["fp-bizlines", wsId, field, type],
    workItemTypes: (wsId: string) => ["fp-types", wsId],
  },
}));

vi.mock("@multica/core/api", () => ({
  api: {
    updateFeishuProjectIntegration: mockUpdateIntegration,
    replaceFeishuProjectRoutes: mockReplaceRoutes,
    syncFeishuProjectIntegration: mockSyncIntegration,
  },
}));

vi.mock("@multica/core/auth", () => {
  const useAuthStore = Object.assign(
    (sel?: (s: { user: { id: string } }) => unknown) =>
      sel ? sel({ user: { id: "user-1" } }) : { user: { id: "user-1" } },
    { getState: () => ({ user: { id: "user-1" } }) },
  );
  return { useAuthStore };
});

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), message: vi.fn() },
}));

// LarkTab drags in the whole Lark install flow — irrelevant to the Feishu
// Project panel under test.
vi.mock("./lark-tab", () => ({
  LarkTab: () => <div data-testid="lark-tab" />,
}));

import { IntegrationsTab } from "./integrations-tab";

const STR = enSettings.integrations;

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
  membersRef.current = [{ user_id: "user-1", role: "admin" }];
  integrationRef.current = configuredIntegration();
  routesRef.current = { routes: [] };
  mockUpdateIntegration.mockResolvedValue(undefined);
  mockReplaceRoutes.mockResolvedValue(undefined);
  mockInvalidate.mockResolvedValue(undefined);
}

describe("IntegrationsTab (Feishu Project panel)", () => {
  beforeEach(resetFixtures);

  it("renders the four step headers, the sync card and a Connected badge for a configured integration", () => {
    render(<IntegrationsTab />, { wrapper: I18nWrapper });

    expect(screen.getByText(STR.feishu_project_step_connection_title)).toBeTruthy();
    expect(screen.getByText(STR.feishu_project_step_content_title)).toBeTruthy();
    expect(screen.getByText(STR.feishu_project_step_routing_title)).toBeTruthy();
    expect(screen.getByText(STR.feishu_project_step_rules_title)).toBeTruthy();
    expect(screen.getByText(STR.feishu_project_sync_section)).toBeTruthy();
    expect(screen.getByText(STR.feishu_project_conn_connected)).toBeTruthy();
    // No locked notes when credentials are saved and complete.
    expect(screen.queryByText(STR.feishu_project_step_locked)).toBeNull();
  });

  it("defaults the 'use default plugin' toggle on when the saved plugin id equals the deployment default", () => {
    integrationRef.current = {
      ...configuredIntegration(),
      default_plugin_available: true,
      default_plugin_id: "MII_DEFAULT",
      plugin_id: "MII_DEFAULT",
      has_plugin_secret: false,
    };
    render(<IntegrationsTab />, { wrapper: I18nWrapper });

    // Toggle present + on, custom credential fields hidden — the operator never
    // has to re-enter the company plugin.
    const row = screen.getByText(STR.feishu_project_plugin_use_default).closest("div")!.parentElement!;
    const toggle = row.querySelector('[role="switch"]') as HTMLElement;
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    expect(screen.queryByText(STR.feishu_project_plugin_id)).toBeNull();
  });

  it("locks steps 2-4 behind the connection step when no integration is saved", () => {
    integrationRef.current = null;
    render(<IntegrationsTab />, { wrapper: I18nWrapper });

    // "Not configured" appears both as the connection badge and as step 1's
    // pending status — both are expected.
    expect(screen.getAllByText(STR.feishu_project_conn_not_configured).length).toBeGreaterThan(0);
    expect(screen.getAllByText(STR.feishu_project_step_locked)).toHaveLength(3);
  });

  it("hides the config steps entirely for non-admin members", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    render(<IntegrationsTab />, { wrapper: I18nWrapper });

    expect(screen.getByText(STR.manage_hint)).toBeTruthy();
    expect(screen.queryByText(STR.feishu_project_step_connection_title)).toBeNull();
  });

  it("keeps the Save button mounted-but-disabled while pristine so it stays discoverable", () => {
    render(<IntegrationsTab />, { wrapper: I18nWrapper });

    // The save bar is always present; pristine state shows "all saved" + a
    // disabled Save and no Discard (the original bug hid the whole bar).
    expect(screen.getByText(STR.feishu_project_all_saved)).toBeTruthy();
    expect(screen.queryByText(STR.feishu_project_unsaved_changes)).toBeNull();
    const saveButton = screen.getByRole("button", {
      name: STR.feishu_project_save,
    }) as HTMLButtonElement;
    expect(saveButton.disabled).toBe(true);
    expect(screen.queryByRole("button", { name: STR.feishu_project_discard })).toBeNull();
  });

  it("shows the unsaved-changes affordance after an edit, blocks Sync now while dirty, and Discard restores the saved state", async () => {
    const user = userEvent.setup();
    render(<IntegrationsTab />, { wrapper: I18nWrapper });

    // Pristine: no unsaved warning, sync allowed.
    expect(screen.queryByText(STR.feishu_project_unsaved_changes)).toBeNull();
    const syncButton = screen.getByRole("button", {
      name: STR.feishu_project_sync_now,
    }) as HTMLButtonElement;
    expect(syncButton.disabled).toBe(false);

    // Toggle the enable switch → draft differs from server state.
    const enableSwitch = screen.getAllByRole("switch")[0]!;
    await user.click(enableSwitch);
    expect(screen.getByText(STR.feishu_project_unsaved_changes)).toBeTruthy();
    expect(syncButton.disabled).toBe(true);
    expect(screen.getByText(STR.feishu_project_sync_blocked_unsaved)).toBeTruthy();

    // Discard reseeds from the server snapshot → bar disappears, sync unblocks.
    await user.click(screen.getByRole("button", { name: STR.feishu_project_discard }));
    expect(screen.queryByText(STR.feishu_project_unsaved_changes)).toBeNull();
    expect(syncButton.disabled).toBe(false);
  });

  it("sends the default 30-day lookback with a manual sync", async () => {
    const user = userEvent.setup();
    mockSyncIntegration.mockResolvedValue({ status: "running", run: { id: "run-1" } });
    render(<IntegrationsTab />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("button", { name: STR.feishu_project_sync_now }));

    await waitFor(() => expect(mockSyncIntegration).toHaveBeenCalledTimes(1));
    expect(mockSyncIntegration).toHaveBeenLastCalledWith("workspace-1", {
      work_item_id: undefined,
      lookback_days: 30,
    });
  });

  it("sends the selected lookback window (up to half a year) with a manual sync", async () => {
    const user = userEvent.setup();
    mockSyncIntegration.mockResolvedValue({ status: "running", run: { id: "run-1" } });
    render(<IntegrationsTab />, { wrapper: I18nWrapper });

    await user.click(screen.getByLabelText(STR.feishu_project_sync_range_label));
    await user.click(screen.getByRole("option", { name: "Last 180 days" }));
    await user.click(screen.getByRole("button", { name: STR.feishu_project_sync_now }));

    await waitFor(() => expect(mockSyncIntegration).toHaveBeenCalledTimes(1));
    expect(mockSyncIntegration).toHaveBeenLastCalledWith("workspace-1", {
      work_item_id: undefined,
      lookback_days: 180,
    });
  });

  it("saves the draft and replaces routes when Save in the save bar is clicked", async () => {
    const user = userEvent.setup();
    render(<IntegrationsTab />, { wrapper: I18nWrapper });

    const enableSwitch = screen.getAllByRole("switch")[0]!;
    await user.click(enableSwitch);
    await user.click(screen.getByRole("button", { name: STR.feishu_project_save }));

    await waitFor(() => {
      expect(mockUpdateIntegration).toHaveBeenCalledTimes(1);
    });
    expect(mockUpdateIntegration).toHaveBeenCalledWith(
      "workspace-1",
      expect.objectContaining({ enabled: false }),
    );
    expect(mockReplaceRoutes).toHaveBeenCalledWith("workspace-1", { routes: [] });
  });

  it("renders the assignee-scope toggle and includes it in the save payload when turned on", async () => {
    const user = userEvent.setup();
    render(<IntegrationsTab />, { wrapper: I18nWrapper });

    expect(screen.getByText(STR.feishu_project_member_scope)).toBeTruthy();
    // The scope toggle is the switch inside the member-scope row.
    const scopeRow = screen.getByText(STR.feishu_project_member_scope).closest("div")!
      .parentElement!;
    const scopeSwitch = scopeRow.querySelector('[role="switch"]') as HTMLElement;
    await user.click(scopeSwitch);
    await user.click(screen.getByRole("button", { name: STR.feishu_project_save }));

    await waitFor(() => {
      expect(mockUpdateIntegration).toHaveBeenCalledTimes(1);
    });
    expect(mockUpdateIntegration).toHaveBeenCalledWith(
      "workspace-1",
      expect.objectContaining({ sync_only_workspace_member_items: true }),
    );
  });

  it("collapses a mapped work-item type to its summary row and expands it on click", async () => {
    const user = userEvent.setup();
    render(<IntegrationsTab />, { wrapper: I18nWrapper });

    // Mapped type starts collapsed: summary visible, detail form hidden.
    const summary = screen.getByText(/statuses mapped/);
    expect(summary).toBeTruthy();
    expect(screen.queryByText(STR.feishu_project_type_identifier_prefix)).toBeNull();

    await user.click(summary);
    expect(screen.getByText(STR.feishu_project_type_identifier_prefix)).toBeTruthy();
  });
});
