// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

const { getEnv, putEnv, deleteEnv } = vi.hoisted(() => ({
  getEnv: vi.fn(),
  putEnv: vi.fn(),
  deleteEnv: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    getCloudRuntimeEnv: (...a: unknown[]) => getEnv(...a),
    putCloudRuntimeEnv: (...a: unknown[]) => putEnv(...a),
    deleteCloudRuntimeEnv: (...a: unknown[]) => deleteEnv(...a),
  },
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

import { CloudRuntimeEnvCard } from "./cloud-runtime-env-card";
import { toast } from "sonner";

function renderCard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <CloudRuntimeEnvCard wsId="ws-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("CloudRuntimeEnvCard", () => {
  it("renders configured variables as name + last4 fingerprints, never plaintext", async () => {
    getEnv.mockResolvedValue({
      configured: true,
      env: [{ name: "ANTHROPIC_AUTH_TOKEN", last4: "abcd" }],
    });
    renderCard();
    expect(await screen.findByText("ANTHROPIC_AUTH_TOKEN")).toBeTruthy();
    expect(screen.getByText(/abcd/)).toBeTruthy();
  });

  it("saves the structured AI connection through putCloudRuntimeEnv", async () => {
    getEnv.mockResolvedValue({ configured: false, env: [] });
    putEnv.mockResolvedValue({ configured: true, env: [{ name: "OPENAI_API_KEY", last4: "wxyz" }] });
    renderCard();
    await screen.findByText(/No AI connection configured/i);

    fireEvent.change(screen.getByLabelText("Base URL"), {
      target: { value: "https://proxy.example/v1" },
    });
    fireEvent.change(screen.getByLabelText("API Key"), {
      target: { value: "sk-secret-wxyz" },
    });
    fireEvent.click(screen.getByText("Save connection"));

    await waitFor(() =>
      expect(putEnv).toHaveBeenCalledWith("ws-1", {
        MULTICA_CLOUD_RUNTIME_AI_PROVIDER: "multica_proxy",
        ANTHROPIC_BASE_URL: "https://proxy.example/v1",
        ANTHROPIC_AUTH_TOKEN: "sk-secret-wxyz",
      }),
    );
  });

  it("rejects lowercase names before calling the API", async () => {
    getEnv.mockResolvedValue({ configured: false, env: [] });
    renderCard();
    await screen.findByText(/No AI connection configured/i);

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "bad-name" } });
    fireEvent.change(screen.getByLabelText("Value"), { target: { value: "v" } });
    fireEvent.click(screen.getByText("Save connection"));

    await waitFor(() => expect(toast.error).toHaveBeenCalled());
    expect(putEnv).not.toHaveBeenCalled();
  });

  it("masks the value input so keys are not shoulder-surfable", async () => {
    getEnv.mockResolvedValue({ configured: false, env: [] });
    renderCard();
    await screen.findByText(/No AI connection configured/i);
    expect((screen.getByLabelText("Value") as HTMLInputElement).type).toBe("password");
    expect((screen.getByLabelText("API Key") as HTMLInputElement).type).toBe("password");
  });
});
