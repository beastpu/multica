import { type ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// cmdk calls scrollIntoView on mount; jsdom doesn't implement it.
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = vi.fn();
}
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

// The field picker reads the Meego field list via feishuProjectFieldsOptions.
// Returning two fields lets us exercise the trigger + dropdown affordances.
const fieldsRef = vi.hoisted(() => ({
  current: {
    fields: [
      { key: "field_biz", name: "所属业务线", type: "select" },
      { key: "field_team", name: "团队", type: "select" },
    ],
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: { queryKey: unknown[]; enabled?: boolean }) => {
    if (opts.enabled === false) return { data: undefined, isFetching: false, refetch: vi.fn() };
    const key = JSON.stringify(opts.queryKey);
    if (key.includes("fp-fields")) return { data: fieldsRef.current, isFetching: false, refetch: vi.fn() };
    if (key.includes("fp-bizlines")) return { data: { business_lines: [] }, isFetching: false, refetch: vi.fn() };
    return { data: [], isFetching: false, refetch: vi.fn() };
  },
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
  queryOptions: <T,>(opts: T) => opts,
}));

vi.mock("@multica/core/feishu-project/queries", () => ({
  feishuProjectFieldsOptions: (_ws: string, _type: string, enabled: boolean) => ({
    queryKey: ["fp-fields"],
    queryFn: vi.fn(),
    enabled,
  }),
  feishuProjectBusinessLinesOptions: (_ws: string, _f: string, _t: string, enabled: boolean) => ({
    queryKey: ["fp-bizlines"],
    queryFn: vi.fn(),
    enabled,
  }),
  feishuProjectKeys: {
    businessLines: (wsId: string, field: string, type: string) => ["fp-bizlines", wsId, field, type],
  },
}));

vi.mock("@multica/core/projects", () => ({
  projectListOptions: () => ({ queryKey: ["projects"], queryFn: vi.fn() }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["agents"], queryFn: vi.fn() }),
}));

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

import { FeishuProjectRoutingSection } from "./feishu-project-routing-section";

const STR = enSettings.integrations;
const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

// integrationReady requires id + (has_plugin_secret || default_plugin_available).
const readyIntegration = {
  id: "int-1",
  has_plugin_secret: true,
  default_plugin_available: false,
  business_line_field_key: "",
  business_line_field_name: "",
} as never;

function renderSection(fieldKey: string, onFieldChanged = vi.fn()) {
  render(
    <FeishuProjectRoutingSection
      workspaceId="ws-1"
      integration={readyIntegration}
      fieldKey={fieldKey}
      onFieldChanged={onFieldChanged}
      rows={[]}
      setRows={vi.fn()}
      expanded={{}}
      setExpanded={vi.fn()}
    />,
    { wrapper: I18nWrapper },
  );
  return { onFieldChanged };
}

describe("FeishuProjectRoutingSection field picker", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fieldsRef.current = {
      fields: [
        { key: "field_biz", name: "所属业务线", type: "select" },
        { key: "field_team", name: "团队", type: "select" },
      ],
    };
  });

  it("shows an explicit 'no routing' option in the dropdown", async () => {
    const user = userEvent.setup();
    renderSection("");

    await user.click(screen.getByText(STR.feishu_project_business_line_field_placeholder));
    await waitFor(() => {
      expect(screen.getByText(STR.feishu_project_business_line_field_none)).toBeTruthy();
    });
  });

  it("renders no clear button while no field is selected", () => {
    renderSection("");
    expect(
      screen.queryByRole("button", { name: STR.feishu_project_business_line_field_clear }),
    ).toBeNull();
  });

  it("shows a clear button that disables routing in one click when a field is selected", async () => {
    const user = userEvent.setup();
    const { onFieldChanged } = renderSection("field_biz");

    const clear = screen.getByRole("button", {
      name: STR.feishu_project_business_line_field_clear,
    });
    expect(clear).toBeTruthy();
    await user.click(clear);
    // Clearing routes through onFieldChanged("", "") — the parent's "disable
    // routing" signal — without needing to open the dropdown.
    expect(onFieldChanged).toHaveBeenCalledWith("", "");
  });
});
