import { createStore } from "zustand/vanilla";
import { useStore } from "zustand";
import { useQuery } from "@tanstack/react-query";
import { getApi } from "../api";

interface ConfigState {
  cdnDomain: string;
  // True when cdnDomain serves private content via time-bounded signed URLs
  // (CloudFront signing enabled server-side). Renderers must not treat a raw
  // storage URL on that domain as a loadable media source (MUL-3254).
  cdnSigned: boolean;
  allowSignup: boolean;
  googleClientId: string;
  daemonServerUrl: string;
  daemonAppUrl: string;
  // Self-host gate (#3433): when true, every "Create workspace" affordance
  // must be hidden. Defaults to false so unknown / older servers behave like
  // the managed-cloud case.
  workspaceCreationDisabled: boolean;
  featureFlags: Record<string, boolean>;
  // The running API build version, surfaced in the Help popover so
  // self-hosted operators can confirm what's deployed. Empty for dev builds
  // or servers older than this feature.
  serverVersion: string;
  setCdnConfig: (config: { cdnDomain: string; cdnSigned?: boolean }) => void;
  setAuthConfig: (config: {
    allowSignup: boolean;
    googleClientId?: string;
    workspaceCreationDisabled?: boolean;
  }) => void;
  setDaemonConfig: (config: {
    daemonServerUrl?: string;
    daemonAppUrl?: string;
  }) => void;
  setFeatureFlags: (flags?: Record<string, boolean>) => void;
  setServerVersion: (version?: string) => void;
}

export const configStore = createStore<ConfigState>((set) => ({
  cdnDomain: "",
  cdnSigned: false,
  allowSignup: true,
  googleClientId: "",
  daemonServerUrl: "",
  daemonAppUrl: "",
  workspaceCreationDisabled: false,
  featureFlags: {},
  serverVersion: "",
  setCdnConfig: ({ cdnDomain, cdnSigned = false }) => set({ cdnDomain, cdnSigned }),
  setAuthConfig: ({ allowSignup, googleClientId = "", workspaceCreationDisabled = false }) =>
    set({ allowSignup, googleClientId, workspaceCreationDisabled }),
  setDaemonConfig: ({ daemonServerUrl = "", daemonAppUrl = "" }) =>
    set({ daemonServerUrl, daemonAppUrl }),
  setFeatureFlags: (flags = {}) => set({ featureFlags: { ...flags } }),
  setServerVersion: (version = "") => set({ serverVersion: version }),
}));

export function useConfigStore(): ConfigState;
export function useConfigStore<T>(selector: (state: ConfigState) => T): T;
export function useConfigStore<T>(selector?: (state: ConfigState) => T) {
  return useStore(configStore, selector as (state: ConfigState) => T);
}

export function featureFlagEnabled(
  flags: Readonly<Record<string, boolean>> | undefined,
  key: string,
  defaultValue = false,
): boolean {
  return flags?.[key] ?? defaultValue;
}

export function useFeatureEnabled(key: string, defaultValue = false): boolean {
  return useConfigStore((state) =>
    featureFlagEnabled(state.featureFlags, key, defaultValue),
  );
}

export type WorkspaceFeatureState = "loading" | "enabled" | "disabled";

export function resolveWorkspaceFeatureState(
  workspaceId: string | null | undefined,
  flags: Readonly<Record<string, boolean>> | undefined,
  isPending: boolean,
  key: string,
): WorkspaceFeatureState {
  if (!workspaceId || isPending) return "loading";
  return flags?.[key] === true ? "enabled" : "disabled";
}

/**
 * Re-evaluates a public feature flag with the active workspace header.
 * The initial unauthenticated `/api/config` bootstrap has no tenant context,
 * so it cannot represent workspace allowlists. Workspace surfaces must use
 * this hook; a missing/legacy response fails closed.
 */
export function useWorkspaceFeatureState(
  workspaceId: string | null | undefined,
  key: string,
): WorkspaceFeatureState {
  const query = useQuery({
    queryKey: ["app-config", "workspace", workspaceId] as const,
    queryFn: () => getApi().getConfig(),
    enabled: Boolean(workspaceId),
    staleTime: Number.POSITIVE_INFINITY,
  });
  return resolveWorkspaceFeatureState(
    workspaceId,
    query.data?.feature_flags,
    query.isPending,
    key,
  );
}

export function useWorkspaceFeatureEnabled(
  workspaceId: string | null | undefined,
  key: string,
): boolean {
  return useWorkspaceFeatureState(workspaceId, key) === "enabled";
}
