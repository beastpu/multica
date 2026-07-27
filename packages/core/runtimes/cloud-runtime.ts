import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { runtimeKeys } from "./queries";

export interface CloudRuntimeAccess {
  enabled: boolean;
}

export interface CloudRuntimeNode {
  id: string;
  owner_id: string;
  instance_id: string;
  region: string;
  instance_type: string;
  image_id: string;
  subnet_id: string;
  name: string;
  status: string;
  tags: Record<string, string>;
  metadata: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface ListCloudRuntimeNodesParams {
  limit?: number;
  offset?: number;
}

/** A configured workspace env variable. Sensitive values never leave the server. */
export interface CloudRuntimeEnvVar {
  name: string;
  /** Last 4 characters of the value, for recognition without exposure. */
  last4: string;
  /** Plaintext for non-sensitive config values such as base URLs and model ids. */
  value?: string;
}

export interface CloudRuntimeEnv {
  configured: boolean;
  env: CloudRuntimeEnvVar[];
  updated_at?: string;
}

export interface CloudRuntimeEnvUpdate {
  env?: Record<string, string>;
  remove_env?: string[];
}

export interface CreateCloudRuntimeNodeRequest {
  instance_type: string;
  name?: string;
  region?: string;
  image_id?: string;
  subnet_id?: string;
  key_name?: string;
  iam_instance_profile?: string;
  disk_size_gb?: number;
  tags?: Record<string, string>;
}

export const CLOUD_RUNTIME_INSTANCE_PROFILES = [
  {
    type: "t4g.medium",
    cpu: "2 CPU",
    memory: "4 GiB RAM",
    description: "Balanced default for lightweight agent work.",
  },
  {
    type: "t4g.large",
    cpu: "2 CPU",
    memory: "8 GiB RAM",
    description: "More memory for larger repositories or long-running tasks.",
  },
] as const;

export const CLOUD_RUNTIME_DEFAULT_INSTANCE_TYPE =
  CLOUD_RUNTIME_INSTANCE_PROFILES[0].type;

export const CLOUD_RUNTIME_DISK_SIZE = {
  defaultGB: 20,
  minGB: 20,
  maxGB: 100,
  stepGB: 10,
} as const;

export const CLOUD_RUNTIME_MAX_NODES_PER_WORKSPACE = 3;

export function cloudRuntimeInstanceProfile(type: string) {
  return CLOUD_RUNTIME_INSTANCE_PROFILES.find((profile) => profile.type === type);
}

export const cloudRuntimeKeys = {
  all: (wsId: string) => ["cloud-runtime", wsId] as const,
  access: (wsId: string) => [...cloudRuntimeKeys.all(wsId), "access"] as const,
  nodes: (wsId: string) => [...cloudRuntimeKeys.all(wsId), "nodes"] as const,
};

export function cloudRuntimeAccessOptions(wsId: string) {
  return queryOptions({
    queryKey: cloudRuntimeKeys.access(wsId),
    queryFn: () => api.getCloudRuntimeAccess(),
    staleTime: 30 * 1000,
  });
}

const PENDING_NODE_STATUSES = new Set([
  "launching",
  "pending",
  "starting",
  "stopping",
  "rebooting",
  "terminating",
]);

export function isCloudRuntimeNodePending(status: string): boolean {
  return PENDING_NODE_STATUSES.has(status.toLowerCase());
}

export function cloudRuntimeNodeListOptions(
  wsId: string,
  params?: ListCloudRuntimeNodesParams,
) {
  const limit = params?.limit ?? 20;
  const offset = params?.offset ?? 0;
  return queryOptions({
    queryKey: [...cloudRuntimeKeys.nodes(wsId), { limit, offset }] as const,
    queryFn: () => api.listCloudRuntimeNodes({ limit, offset }),
    refetchInterval: (query) =>
      query.state.data?.some((node) => isCloudRuntimeNodePending(node.status))
        ? 5000
        : false,
    staleTime: 15 * 1000,
  });
}

export function useCreateCloudRuntimeNode(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateCloudRuntimeNodeRequest) =>
      api.createCloudRuntimeNode(data),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: cloudRuntimeKeys.all(wsId) });
    },
  });
}

export function useDeleteCloudRuntimeNode(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (instanceId: string) => api.deleteCloudRuntimeNode(instanceId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: cloudRuntimeKeys.all(wsId) });
      qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    },
  });
}

export function useRebootCloudRuntimeNode(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (instanceId: string) => api.rebootCloudRuntimeNode(instanceId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: cloudRuntimeKeys.all(wsId) });
    },
  });
}

export const cloudRuntimeEnvKeys = {
  all: (wsId: string) => ["cloud-runtime-env", wsId] as const,
};

export function cloudRuntimeEnvOptions(wsId: string) {
  return queryOptions({
    queryKey: cloudRuntimeEnvKeys.all(wsId),
    queryFn: () => api.getCloudRuntimeEnv(wsId),
    staleTime: 30 * 1000,
  });
}

export function useSaveCloudRuntimeEnv(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (update: CloudRuntimeEnvUpdate) =>
      api.putCloudRuntimeEnv(wsId, update),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: cloudRuntimeEnvKeys.all(wsId) });
    },
  });
}

export function useDeleteCloudRuntimeEnv(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.deleteCloudRuntimeEnv(wsId),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: cloudRuntimeEnvKeys.all(wsId) });
    },
  });
}
