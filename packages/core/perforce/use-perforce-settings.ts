"use client";

import { useMemo } from "react";
import { useCurrentWorkspace } from "../paths";
import { derivePerforceSettings, type PerforceSettings } from "./settings";

/** Reads the Perforce feature flags off the current workspace's settings. */
export function usePerforceSettings(): PerforceSettings {
  const workspace = useCurrentWorkspace();
  return useMemo(() => derivePerforceSettings(workspace), [workspace]);
}
