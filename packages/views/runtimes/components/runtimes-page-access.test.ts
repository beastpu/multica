import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "@multica/core/types";
import type { RuntimeMachine } from "./runtime-machines";

import { canDeleteMachine, canShowCloudRuntimeEntry } from "./runtimes-page";

describe("Cloud Runtime entry access", () => {
  it("requires both the web surface and an explicit workspace grant", () => {
    expect(canShowCloudRuntimeEntry(false, { enabled: true })).toBe(false);
    expect(canShowCloudRuntimeEntry(true, undefined)).toBe(false);
    expect(canShowCloudRuntimeEntry(true, { enabled: false })).toBe(false);
    expect(canShowCloudRuntimeEntry(true, { enabled: true })).toBe(true);
  });
});

function runtime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Claude",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "offline",
    device_info: "",
    metadata: {},
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function machine(overrides: Partial<RuntimeMachine> = {}): RuntimeMachine {
  return {
    id: "local:daemon-1",
    daemonId: "daemon-1",
    title: "Machine",
    subtitle: "daemon daemon-1",
    deviceInfo: null,
    cliVersion: null,
    launchedBy: null,
    mode: "local",
    section: "remote",
    isCurrent: false,
    health: "offline",
    runtimes: [runtime()],
    onlineCount: 0,
    issueCount: 1,
    runningCount: 0,
    queuedCount: 0,
    providerNames: ["claude"],
    lastSeenAt: null,
    ...overrides,
  };
}

describe("machine-level runtime delete access", () => {
  it("allows workspace admins to delete cloud worker rows only when the node id is known", () => {
    expect(
      canDeleteMachine(
        machine({
          id: "cloud:node-a1441f33",
          daemonId: "node-a1441f33",
          mode: "cloud",
          section: "cloud",
          runtimes: [
            runtime({
              runtime_mode: "cloud",
              daemon_id: "node-a1441f33",
              owner_id: "user-2",
            }),
          ],
        }),
        "user-1",
        "admin",
      ),
    ).toBe(true);
    expect(
      canDeleteMachine(
        machine({ daemonId: null, mode: "cloud", section: "cloud" }),
        "user-1",
        "admin",
      ),
    ).toBe(false);
  });

  it("keeps ordinary daemon cleanup scoped to admins or owners of every child runtime", () => {
    expect(canDeleteMachine(machine(), "user-1", "member")).toBe(true);
    expect(
      canDeleteMachine(
        machine({ runtimes: [runtime(), runtime({ id: "rt-2", owner_id: "user-2" })] }),
        "user-1",
        "member",
      ),
    ).toBe(false);
    expect(
      canDeleteMachine(
        machine({ runtimes: [runtime({ owner_id: "user-2" })] }),
        "user-1",
        "owner",
      ),
    ).toBe(true);
  });
});
