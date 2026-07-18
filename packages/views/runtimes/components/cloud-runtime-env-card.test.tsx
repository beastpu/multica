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
  it("renders non-sensitive values and masks the configured API key", async () => {
    getEnv.mockResolvedValue({
      configured: true,
      env: [
        { name: "CODEX_BASE_URL", last4: "/v1", value: "https://proxy.example/v1" },
        { name: "ANTHROPIC_BASE_URL", last4: "/v1", value: "https://proxy.example/v1" },
        { name: "CODEX_MODEL", last4: "odex", value: "gpt-5-codex" },
        { name: "OPENAI_API_KEY", last4: "wxyz" },
      ],
    });
    renderCard();

    expect(await screen.findByDisplayValue("https://proxy.example/v1")).toBeTruthy();
    expect(screen.getByDisplayValue("gpt-5-codex")).toBeTruthy();
    expect(screen.getByText("Configured")).toBeTruthy();
    expect(screen.getByText(/wxyz/)).toBeTruthy();
    expect(screen.queryByDisplayValue(/sk-/)).toBeNull();
  });

  it("saves the LLM gateway connection for Codex and Claude Code", async () => {
    getEnv.mockResolvedValue({ configured: false, env: [] });
    putEnv.mockResolvedValue({
      configured: true,
      env: [{ name: "OPENAI_API_KEY", last4: "wxyz" }],
    });
    renderCard();
    await screen.findByText("LLM Gateway");

    fireEvent.change(screen.getByLabelText("LLM Gateway Base URL"), {
      target: { value: "https://proxy.example/v1" },
    });
    fireEvent.change(screen.getByLabelText("Default model"), {
      target: { value: "gpt-5-codex" },
    });
    fireEvent.change(screen.getByLabelText("LLM Gateway API Key (optional)"), {
      target: { value: "sk-secret-wxyz" },
    });
    fireEvent.click(screen.getByText("Save connection"));

    await waitFor(() =>
      expect(putEnv).toHaveBeenCalledWith("ws-1", {
        env: {
          CODEX_BASE_URL: "https://proxy.example/v1",
          ANTHROPIC_BASE_URL: "https://proxy.example/v1",
          CODEX_MODEL: "gpt-5-codex",
          MULTICA_CODEX_MODEL: "gpt-5-codex",
          MULTICA_CLAUDE_MODEL: "gpt-5-codex",
          ANTHROPIC_MODEL: "gpt-5-codex",
          OPENAI_API_KEY: "sk-secret-wxyz",
          ANTHROPIC_AUTH_TOKEN: "sk-secret-wxyz",
        },
      }),
    );
  });

  it("removes only the API key when requested", async () => {
    getEnv.mockResolvedValue({
      configured: true,
      env: [
        { name: "CODEX_BASE_URL", last4: "/v1", value: "https://proxy.example/v1" },
        { name: "ANTHROPIC_BASE_URL", last4: "/v1", value: "https://proxy.example/v1" },
        { name: "OPENAI_API_KEY", last4: "wxyz" },
        { name: "ANTHROPIC_AUTH_TOKEN", last4: "wxyz" },
      ],
    });
    putEnv.mockResolvedValue({
      configured: true,
      env: [{ name: "CODEX_BASE_URL", last4: "/v1", value: "https://proxy.example/v1" }],
    });
    renderCard();

    await screen.findByText("Configured");
    fireEvent.click(screen.getByText("Remove"));

    await waitFor(() =>
      expect(putEnv).toHaveBeenCalledWith("ws-1", {
        remove_env: ["OPENAI_API_KEY", "ANTHROPIC_AUTH_TOKEN"],
      }),
    );
  });

  it("rejects an empty save before calling the API", async () => {
    getEnv.mockResolvedValue({ configured: false, env: [] });
    renderCard();
    await screen.findByText("LLM Gateway");

    fireEvent.click(screen.getByText("Save connection"));

    await waitFor(() => expect(toast.error).toHaveBeenCalled());
    expect(putEnv).not.toHaveBeenCalled();
  });
});
