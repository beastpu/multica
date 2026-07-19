import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { WorkspaceSlugProvider } from "@multica/core/paths";
import { renderWithI18n } from "../../../test/i18n";
import { NavigationProvider, type NavigationAdapter } from "../../../navigation";
import { LabelPicker } from "./label-picker";

const labels = vi.hoisted(() => [
  {
    id: "label-1",
    workspace_id: "ws-1",
    name: "Needs QA",
    color: "#22c55e",
    created_at: "2026-06-28T00:00:00Z",
    updated_at: "2026-06-28T00:00:00Z",
  },
]);

vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: (opts: { queryKey?: unknown[] }) => {
      const key = opts.queryKey ?? [];
      if (key.includes("issue")) return { data: labels, isLoading: false };
      return { data: labels, isLoading: false };
    },
  };
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/labels", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/labels")>(
      "@multica/core/labels",
    );
  return {
    ...actual,
    useAttachLabel: () => ({ mutate: vi.fn() }),
    useDetachLabel: () => ({ mutate: vi.fn() }),
    useCreateLabel: () => ({ mutate: vi.fn(), isPending: false }),
  };
});

describe("LabelPicker", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders an attached-label popover trigger without Base UI native button warnings", () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});

    const navigation: NavigationAdapter = {
      push: vi.fn(),
      replace: vi.fn(),
      back: vi.fn(),
      pathname: "/acme/issues/issue-1",
      searchParams: new URLSearchParams(),
      getShareableUrl: (path) => path,
    };
    renderWithI18n(
      <WorkspaceSlugProvider slug="acme">
        <NavigationProvider value={navigation}>
          <LabelPicker issueId="issue-1" />
        </NavigationProvider>
      </WorkspaceSlugProvider>,
    );

    expect(screen.getByText("Needs QA")).toBeTruthy();
    expect(
      error.mock.calls.some((call) =>
        String(call[0]).includes("expected a native <button>"),
      ),
    ).toBe(false);
  });
});
