import { render, screen, waitFor } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../locales/en/common.json";

const authState = vi.hoisted(() => ({
  current: {
    user: null as { id: string } | null,
    isLoading: false,
  },
}));
const mockRedeem = vi.hoisted(() => vi.fn());
const mockPush = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: typeof authState.current) => unknown) =>
    selector(authState.current),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    redeemLarkBindingToken: mockRedeem,
  },
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: mockPush }),
}));

import { LarkBindPage } from "./bind-page";

const TEST_RESOURCES = {
  en: { common: enCommon },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function renderWithI18n(ui: ReactElement) {
  return render(ui, { wrapper: I18nWrapper });
}

describe("LarkBindPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authState.current = { user: null, isLoading: false };
    mockRedeem.mockResolvedValue({
      workspace_id: "ws-1",
      installation_id: "install-1",
      lark_open_id: "ou_1",
    });
  });

  it("redeems after the user signs in from the needs-auth state", async () => {
    const view = renderWithI18n(<LarkBindPage token="binding-token" />);

    expect(await screen.findByRole("button", { name: /sign in/i })).toBeInTheDocument();
    expect(mockRedeem).not.toHaveBeenCalled();

    authState.current = { user: { id: "user-1" }, isLoading: false };
    view.rerender(<LarkBindPage token="binding-token" />);

    await waitFor(() => {
      expect(mockRedeem).toHaveBeenCalledWith("binding-token");
    });
    expect(await screen.findByText(/you're bound/i)).toBeInTheDocument();
  });

  it("waits for auth initialization before deciding that sign-in is required", async () => {
    authState.current = { user: null, isLoading: true };
    const view = renderWithI18n(<LarkBindPage token="binding-token" />);

    expect(screen.getByText(/redeeming binding token/i)).toBeInTheDocument();
    expect(mockRedeem).not.toHaveBeenCalled();

    authState.current = { user: { id: "user-1" }, isLoading: false };
    view.rerender(<LarkBindPage token="binding-token" />);

    await waitFor(() => {
      expect(mockRedeem).toHaveBeenCalledWith("binding-token");
    });
  });
});
