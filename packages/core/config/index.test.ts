import { describe, expect, it } from "vitest";
import { resolveWorkspaceFeatureState } from "./index";

describe("resolveWorkspaceFeatureState", () => {
  it("keeps workspace routes pending until their tenant-aware config resolves", () => {
    expect(resolveWorkspaceFeatureState(null, undefined, true, "workflow")).toBe(
      "loading",
    );
    expect(
      resolveWorkspaceFeatureState("workspace-1", undefined, true, "workflow"),
    ).toBe("loading");
  });

  it("fails closed only after the workspace config request settles", () => {
    expect(
      resolveWorkspaceFeatureState("workspace-1", undefined, false, "workflow"),
    ).toBe("disabled");
    expect(
      resolveWorkspaceFeatureState(
        "workspace-1",
        { workflow: false },
        false,
        "workflow",
      ),
    ).toBe("disabled");
    expect(
      resolveWorkspaceFeatureState(
        "workspace-1",
        { workflow: true },
        false,
        "workflow",
      ),
    ).toBe("enabled");
  });
});
