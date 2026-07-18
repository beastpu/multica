import { describe, expect, it } from "vitest";

import { canShowCloudRuntimeEntry } from "./runtimes-page";

describe("Cloud Runtime entry access", () => {
  it("requires both the web surface and an explicit workspace grant", () => {
    expect(canShowCloudRuntimeEntry(false, { enabled: true })).toBe(false);
    expect(canShowCloudRuntimeEntry(true, undefined)).toBe(false);
    expect(canShowCloudRuntimeEntry(true, { enabled: false })).toBe(false);
    expect(canShowCloudRuntimeEntry(true, { enabled: true })).toBe(true);
  });
});
