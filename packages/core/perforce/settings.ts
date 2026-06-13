import type { Workspace } from "../types";

export interface PerforceSettings {
  /** Master switch. When false, the poller is paused and UI is gated off. */
  enabled: boolean;
  /** Issue-detail review sidebar visibility. Implies `enabled`. */
  reviewSidebar: boolean;
}

/**
 * Pure derivation from a workspace's settings JSONB. Perforce is opt-in:
 * `enabled` defaults to false so a workspace that never configures Perforce
 * shows no review sidebar and is never polled. An admin turns it on explicitly
 * (master switch). This differs from the GitHub integration, which defaults on.
 */
export function derivePerforceSettings(
  workspace: Pick<Workspace, "settings"> | null | undefined,
): PerforceSettings {
  const s = (workspace?.settings ?? {}) as Record<string, unknown>;
  const enabled = s.perforce_enabled === true;
  return {
    enabled,
    reviewSidebar: enabled && s.perforce_review_sidebar_enabled !== false,
  };
}
