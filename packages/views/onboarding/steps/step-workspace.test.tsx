import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enOnboarding from "../../locales/en/onboarding.json";
import enWorkspace from "../../locales/en/workspace.json";
import type { Workspace } from "@multica/core/types";

const TEST_RESOURCES = {
  en: {
    common: enCommon,
    onboarding: enOnboarding,
    workspace: enWorkspace,
  },
};

type MockConfigState = {
  daemonAppUrl: string;
};

const mockUseConfigStore = vi.hoisted(() =>
  vi.fn((selector: (state: MockConfigState) => unknown) =>
    selector({ daemonAppUrl: "" }),
  ),
);

vi.mock("@multica/core/config", () => ({
  useConfigStore: (selector: (state: MockConfigState) => unknown) =>
    mockUseConfigStore(selector),
}));

vi.mock("@multica/core/workspace/mutations", () => ({
  useCreateWorkspace: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@multica/core/api", () => ({
  api: { getBaseUrl: () => "http://127.0.0.1:8080" },
}));

import { StepWorkspace } from "./step-workspace";

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function renderStep({
  existing,
  daemonAppUrl = "",
}: {
  existing: Workspace | null;
  daemonAppUrl?: string;
}) {
  mockUseConfigStore.mockImplementation(
    (selector: (state: MockConfigState) => unknown) =>
      selector({ daemonAppUrl }),
  );
  return render(
    <StepWorkspace existing={existing} onCreated={vi.fn()} onBack={vi.fn()} />,
    { wrapper: I18nWrapper },
  );
}

const EXISTING_WORKSPACE: Workspace = {
  id: "00000000-0000-0000-0000-000000000001",
  name: "Acme",
  slug: "acme",
  description: null,
  context: null,
  settings: {},
  repos: [],
  issue_prefix: "ACM",
  created_at: "2025-01-01T00:00:00Z",
  updated_at: "2025-01-01T00:00:00Z",
} as unknown as Workspace;

describe("StepWorkspace — unrestricted workspace creation", () => {
  it("renders the create form when the user has no workspace", () => {
    renderStep({ existing: null });

    expect(
      screen.getByText("Name your workspace.", { exact: false }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Workspace name")).toBeInTheDocument();
    expect(screen.getByLabelText("URL")).toBeInTheDocument();
  });

  it("keeps the create-new option when an existing workspace is available", () => {
    renderStep({ existing: EXISTING_WORKSPACE });

    expect(
      screen.getByText(/start another/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Create a new workspace", { exact: false }),
    ).toBeInTheDocument();
  });
});

// #4263: the workspace URL prefix must reflect the deployment's own host on
// self-hosted instances instead of the hardcoded `multica.ai`.
describe("StepWorkspace — workspace URL prefix", () => {
  it("shows the brand host when no app URL is configured", () => {
    renderStep({ existing: null });
    expect(screen.getByText("multica.ai/")).toBeInTheDocument();
  });

  it("shows the deployment host for self-hosted instances", () => {
    renderStep({
      existing: null,
      daemonAppUrl: "https://multica.example.com",
    });
    expect(screen.getByText("multica.example.com/")).toBeInTheDocument();
    expect(screen.queryByText("multica.ai/")).not.toBeInTheDocument();
  });
});
