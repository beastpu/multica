"use client";

import { useId, useMemo, useState } from "react";
import type { FormEvent, HTMLAttributes } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Cloud,
  Cpu,
  Database,
  HardDrive,
  KeyRound,
  Loader2,
  RefreshCw,
  Rocket,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import type { CloudRuntimeNode } from "@multica/core/runtimes";
import {
  CLOUD_RUNTIME_DEFAULT_INSTANCE_TYPE,
  CLOUD_RUNTIME_DISK_SIZE,
  CLOUD_RUNTIME_INSTANCE_PROFILES,
  CLOUD_RUNTIME_MAX_NODES_PER_WORKSPACE,
  cloudRuntimeEnvOptions,
  cloudRuntimeInstanceProfile,
  cloudRuntimeNodeListOptions,
  useCreateCloudRuntimeNode,
  useDeleteCloudRuntimeNode,
  useRebootCloudRuntimeNode,
} from "@multica/core/runtimes";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentMember } from "@multica/core/permissions";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@multica/ui/components/ui/tabs";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";
import { CloudRuntimeEnvCard } from "./cloud-runtime-env-card";

type CloudRuntimeTab = "nodes" | "ai" | "policy";

export function CloudRuntimeDialog({ onClose }: { onClose: () => void }) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const idPrefix = `cloud-runtime-${useId().replace(/:/g, "")}`;
  const formId = `${idPrefix}-form`;
  const [activeTab, setActiveTab] = useState<CloudRuntimeTab>("nodes");
  const [name, setName] = useState("");
  const [instanceType, setInstanceType] = useState<string>(
    CLOUD_RUNTIME_DEFAULT_INSTANCE_TYPE,
  );
  const [diskSizeGB, setDiskSizeGB] = useState(
    String(CLOUD_RUNTIME_DISK_SIZE.defaultGB),
  );

  const { role } = useCurrentMember(wsId);
  const isAdmin = role === "owner" || role === "admin";

  const nodesQuery = useQuery(
    cloudRuntimeNodeListOptions(wsId, { limit: 20, offset: 0 }),
  );
  const envQuery = useQuery(cloudRuntimeEnvOptions(wsId));
  const createNode = useCreateCloudRuntimeNode(wsId);
  const selectedProfile = cloudRuntimeInstanceProfile(instanceType);
  const aiConfigured =
    envQuery.data?.configured === true || (envQuery.data?.env.length ?? 0) > 0;

  const sortedNodes = useMemo(
    () =>
      (nodesQuery.data ?? []).toSorted(
        (a, b) =>
          new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
      ),
    [nodesQuery.data],
  );

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const diskSize = diskSizeGB.trim()
      ? Number(diskSizeGB.trim())
      : CLOUD_RUNTIME_DISK_SIZE.defaultGB;
    if (
      !Number.isInteger(diskSize) ||
      diskSize < CLOUD_RUNTIME_DISK_SIZE.minGB ||
      diskSize > CLOUD_RUNTIME_DISK_SIZE.maxGB
    ) {
      toast.error(
        t(($) => $.cloud_runtime.validation.disk_size_range, {
          min: CLOUD_RUNTIME_DISK_SIZE.minGB,
          max: CLOUD_RUNTIME_DISK_SIZE.maxGB,
        }),
      );
      return;
    }
    if (diskSize % CLOUD_RUNTIME_DISK_SIZE.stepGB !== 0) {
      toast.error(
        t(($) => $.cloud_runtime.validation.disk_size_step, {
          step: CLOUD_RUNTIME_DISK_SIZE.stepGB,
        }),
      );
      return;
    }

    try {
      await createNode.mutateAsync({
        instance_type: instanceType,
        name: valueOrUndefined(name),
        disk_size_gb: diskSize,
      });
      toast.success(t(($) => $.cloud_runtime.toast_created));
      setName("");
      setInstanceType(CLOUD_RUNTIME_DEFAULT_INSTANCE_TYPE);
      setDiskSizeGB(String(CLOUD_RUNTIME_DISK_SIZE.defaultGB));
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t(($) => $.cloud_runtime.toast_create_failed),
      );
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex max-h-[88vh] flex-col gap-0 p-0 sm:max-w-4xl">
        <DialogHeader className="border-b px-6 py-5">
          <DialogTitle className="flex items-center gap-2 text-base">
            <Cloud className="h-4 w-4 text-muted-foreground" />
            {t(($) => $.cloud_runtime.title)}
          </DialogTitle>
          <DialogDescription className="text-xs">
            {t(($) => $.cloud_runtime.description)}
          </DialogDescription>
        </DialogHeader>

        <Tabs
          value={activeTab}
          onValueChange={(next) => setActiveTab((next ?? "nodes") as CloudRuntimeTab)}
          className="min-h-0 flex-1 gap-0"
        >
          <div className="border-b px-6 pt-3">
            <TabsList variant="line" className="w-full justify-start gap-1 p-0">
              <TabsTrigger value="nodes" className="h-8 px-3 text-xs">
                <Cloud className="h-3.5 w-3.5" />
                {t(($) => $.cloud_runtime.tabs.nodes)}
              </TabsTrigger>
              <TabsTrigger value="ai" className="h-8 px-3 text-xs">
                <KeyRound className="h-3.5 w-3.5" />
                {t(($) => $.cloud_runtime.tabs.ai)}
                {aiConfigured && (
                  <span className="ml-0.5 h-1.5 w-1.5 rounded-full bg-success" />
                )}
              </TabsTrigger>
              <TabsTrigger value="policy" className="h-8 px-3 text-xs">
                <ShieldCheck className="h-3.5 w-3.5" />
                {t(($) => $.cloud_runtime.tabs.policy)}
              </TabsTrigger>
            </TabsList>
          </div>

          <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">
            <TabsContent value="nodes" className="m-0">
              <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(280px,0.82fr)]">
                <form id={formId} onSubmit={handleSubmit} className="space-y-4">
                  <div>
                    <h3 className="text-sm font-medium">
                      {t(($) => $.cloud_runtime.create_title)}
                    </h3>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {t(($) => $.cloud_runtime.create_hint)}
                    </p>
                  </div>

                  <AiConnectionSummary
                    configured={aiConfigured}
                    loading={envQuery.isLoading}
                    onConfigure={() => setActiveTab("ai")}
                  />

                  <div className="grid gap-3 sm:grid-cols-2">
                    <LabeledInput
                      id={`${idPrefix}-name`}
                      label={t(($) => $.cloud_runtime.fields.name)}
                      value={name}
                      onChange={setName}
                      placeholder={t(($) => $.cloud_runtime.placeholders.name)}
                    />
                    <InstanceTypeSelect
                      id={`${idPrefix}-instance-type`}
                      value={instanceType}
                      onChange={setInstanceType}
                    />
                    <DiskSizeInput
                      id={`${idPrefix}-disk-size`}
                      value={diskSizeGB}
                      onChange={setDiskSizeGB}
                    />
                  </div>

                  {selectedProfile && (
                    <div className="grid gap-2 rounded-md border bg-muted/20 p-3 text-xs text-muted-foreground sm:grid-cols-2">
                      <ResourceFact
                        icon={Cpu}
                        label={t(($) => $.cloud_runtime.policy.cpu)}
                        value={selectedProfile.cpu}
                      />
                      <ResourceFact
                        icon={Database}
                        label={t(($) => $.cloud_runtime.policy.memory)}
                        value={selectedProfile.memory}
                      />
                    </div>
                  )}
                </form>

                <CloudRuntimeNodeList
                  nodes={sortedNodes}
                  wsId={wsId}
                  isAdmin={isAdmin}
                  query={nodesQuery}
                />
              </div>
            </TabsContent>

            <TabsContent value="ai" className="m-0">
              <CloudRuntimeEnvCard wsId={wsId} readOnly={!isAdmin} />
            </TabsContent>

            <TabsContent value="policy" className="m-0">
              <CloudRuntimePolicyPanel />
            </TabsContent>
          </div>
        </Tabs>

        <DialogFooter className="m-0 border-t bg-muted/30 px-6 py-3">
          <Button type="button" variant="outline" size="sm" onClick={onClose}>
            {t(($) => $.cloud_runtime.cancel)}
          </Button>
          {activeTab === "nodes" && (
            <Button
              type="submit"
              size="sm"
              form={formId}
              disabled={createNode.isPending}
            >
              {createNode.isPending ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Rocket className="h-3.5 w-3.5" />
              )}
              {createNode.isPending
                ? t(($) => $.cloud_runtime.creating)
                : t(($) => $.cloud_runtime.create)}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function AiConnectionSummary({
  configured,
  loading,
  onConfigure,
}: {
  configured: boolean;
  loading: boolean;
  onConfigure: () => void;
}) {
  const { t } = useT("runtimes");
  if (loading) {
    return (
      <div className="flex items-center gap-2 rounded-md border bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        {t(($) => $.cloud_runtime.env.loading)}
      </div>
    );
  }
  return (
    <div
      className={cn(
        "flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-xs",
        configured
          ? "bg-success/5 text-success"
          : "bg-warning/5 text-warning",
      )}
    >
      <span>
        {configured
          ? t(($) => $.cloud_runtime.env.configured_summary)
          : t(($) => $.cloud_runtime.env.missing_summary)}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onConfigure}
        className="h-7 px-2 text-xs text-foreground"
      >
        {t(($) => $.cloud_runtime.env.configure)}
      </Button>
    </div>
  );
}

function CloudRuntimeNodeList({
  nodes,
  wsId,
  isAdmin,
  query,
}: {
  nodes: CloudRuntimeNode[];
  wsId: string;
  isAdmin: boolean;
  query: ReturnType<typeof useQuery<CloudRuntimeNode[]>>;
}) {
  const { t } = useT("runtimes");
  return (
    <section className="min-h-0 rounded-md border bg-muted/20">
      <div className="flex items-center justify-between border-b bg-background px-3 py-2.5">
        <h3 className="text-sm font-medium">
          {t(($) => $.cloud_runtime.nodes_title)}
        </h3>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
          className="h-7 px-2"
        >
          <RefreshCw
            className={cn("h-3.5 w-3.5", query.isFetching && "animate-spin")}
          />
          {t(($) => $.cloud_runtime.refresh)}
        </Button>
      </div>

      {query.isLoading ? (
        <div className="flex h-40 items-center justify-center">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : query.isError ? (
        <div className="flex h-40 flex-col items-center justify-center px-5 text-center">
          <p className="text-sm font-medium">
            {t(($) => $.cloud_runtime.nodes_failed)}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            {query.error instanceof Error
              ? query.error.message
              : t(($) => $.cloud_runtime.nodes_failed_hint)}
          </p>
        </div>
      ) : nodes.length === 0 ? (
        <div className="flex h-40 flex-col items-center justify-center px-5 text-center">
          <Cloud className="h-7 w-7 text-muted-foreground/50" />
          <p className="mt-3 text-sm font-medium">
            {t(($) => $.cloud_runtime.nodes_empty)}
          </p>
        </div>
      ) : (
        <div className="max-h-[410px] overflow-y-auto p-2">
          <div className="space-y-2">
            {nodes.map((node) => (
              <CloudRuntimeNodeRow
                key={node.id || node.instance_id || node.name}
                node={node}
                wsId={wsId}
                isAdmin={isAdmin}
              />
            ))}
          </div>
        </div>
      )}
    </section>
  );
}

function InstanceTypeSelect({
  id,
  value,
  onChange,
}: {
  id: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const { t } = useT("runtimes");
  const selected = cloudRuntimeInstanceProfile(value);
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-xs text-muted-foreground">
        {t(($) => $.cloud_runtime.fields.instance_type)}
      </Label>
      <Select
        items={CLOUD_RUNTIME_INSTANCE_PROFILES.map((profile) => ({
          value: profile.type,
          label: profile.type,
        }))}
        value={value}
        onValueChange={(next) => onChange(next ?? value)}
      >
        <SelectTrigger id={id} className="h-9 w-full rounded-md text-sm">
          <SelectValue>
            {() => (
              <span className="truncate">
                {value}
                {selected && (
                  <span className="ml-2 text-xs text-muted-foreground">
                    {selected.cpu} / {selected.memory}
                  </span>
                )}
              </span>
            )}
          </SelectValue>
        </SelectTrigger>
        <SelectContent align="start">
          {CLOUD_RUNTIME_INSTANCE_PROFILES.map((profile) => (
            <SelectItem key={profile.type} value={profile.type}>
              <div className="flex min-w-60 items-center justify-between gap-4">
                <span className="font-mono text-sm">{profile.type}</span>
                <span className="text-xs text-muted-foreground">
                  {profile.cpu} / {profile.memory}
                </span>
              </div>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

function DiskSizeInput({
  id,
  value,
  onChange,
}: {
  id: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const { t } = useT("runtimes");
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={id} className="text-xs text-muted-foreground">
          {t(($) => $.cloud_runtime.fields.disk_size)}
        </Label>
        <span className="text-[11px] text-muted-foreground">
          {t(($) => $.cloud_runtime.disk_range, {
            min: CLOUD_RUNTIME_DISK_SIZE.minGB,
            max: CLOUD_RUNTIME_DISK_SIZE.maxGB,
          })}
        </span>
      </div>
      <Input
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={String(CLOUD_RUNTIME_DISK_SIZE.defaultGB)}
        type="number"
        min={CLOUD_RUNTIME_DISK_SIZE.minGB}
        max={CLOUD_RUNTIME_DISK_SIZE.maxGB}
        step={CLOUD_RUNTIME_DISK_SIZE.stepGB}
        inputMode="numeric"
        className="h-9 text-sm"
      />
      <p className="text-[11px] text-muted-foreground/70">
        {t(($) => $.cloud_runtime.disk_delete_hint)}
      </p>
    </div>
  );
}

function CloudRuntimePolicyPanel() {
  const { t } = useT("runtimes");
  return (
    <div className="space-y-4">
      <section className="rounded-md border bg-muted/20">
        <div className="border-b bg-background px-3 py-2.5">
          <h3 className="text-sm font-medium">
            {t(($) => $.cloud_runtime.policy.instance_title)}
          </h3>
        </div>
        <div className="divide-y">
          {CLOUD_RUNTIME_INSTANCE_PROFILES.map((profile) => (
            <div
              key={profile.type}
              className="grid gap-3 px-3 py-3 text-sm sm:grid-cols-[1fr_auto_auto]"
            >
              <div>
                <div className="font-mono font-medium">{profile.type}</div>
                <p className="mt-1 text-xs text-muted-foreground">
                  {profile.description}
                </p>
              </div>
              <ResourcePill icon={Cpu} value={profile.cpu} />
              <ResourcePill icon={Database} value={profile.memory} />
            </div>
          ))}
        </div>
      </section>

      <section className="grid gap-3 sm:grid-cols-3">
        <PolicyStat
          icon={HardDrive}
          label={t(($) => $.cloud_runtime.policy.disk)}
          value={t(($) => $.cloud_runtime.policy.disk_value, {
            min: CLOUD_RUNTIME_DISK_SIZE.minGB,
            max: CLOUD_RUNTIME_DISK_SIZE.maxGB,
          })}
        />
        <PolicyStat
          icon={Cloud}
          label={t(($) => $.cloud_runtime.policy.node_limit)}
          value={t(($) => $.cloud_runtime.policy.node_limit_value, {
            count: CLOUD_RUNTIME_MAX_NODES_PER_WORKSPACE,
          })}
        />
        <PolicyStat
          icon={ShieldCheck}
          label={t(($) => $.cloud_runtime.policy.scope)}
          value={t(($) => $.cloud_runtime.policy.scope_value)}
        />
      </section>
    </div>
  );
}

function LabeledInput({
  id,
  label,
  value,
  onChange,
  placeholder,
  required,
  type = "text",
  inputMode,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  required?: boolean;
  type?: string;
  inputMode?: HTMLAttributes<HTMLInputElement>["inputMode"];
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </Label>
      <Input
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        required={required}
        type={type}
        inputMode={inputMode}
        className="h-9 text-sm"
      />
    </div>
  );
}

function ResourceFact({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof Cpu;
  label: string;
  value: string;
}) {
  return (
    <div className="flex items-center gap-2">
      <Icon className="h-3.5 w-3.5 text-muted-foreground" />
      <span>{label}</span>
      <span className="font-medium text-foreground">{value}</span>
    </div>
  );
}

function ResourcePill({ icon: Icon, value }: { icon: typeof Cpu; value: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-md bg-muted px-2 py-1 text-xs text-muted-foreground">
      <Icon className="h-3.5 w-3.5" />
      {value}
    </span>
  );
}

function PolicyStat({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof Cpu;
  label: string;
  value: string;
}) {
  return (
    <div className="rounded-md border bg-muted/20 p-3">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Icon className="h-3.5 w-3.5" />
        {label}
      </div>
      <div className="mt-2 text-sm font-medium">{value}</div>
    </div>
  );
}

function CloudRuntimeNodeRow({
  node,
  wsId,
  isAdmin,
}: {
  node: CloudRuntimeNode;
  wsId: string;
  isAdmin: boolean;
}) {
  const { t } = useT("runtimes");
  const deleteNode = useDeleteCloudRuntimeNode(wsId);
  const rebootNode = useRebootCloudRuntimeNode(wsId);
  const [confirming, setConfirming] = useState(false);
  const title =
    node.name.trim() ||
    node.instance_id.trim() ||
    t(($) => $.cloud_runtime.node_fallback_name);
  const created = formatDateTime(node.created_at);
  const profile = cloudRuntimeInstanceProfile(node.instance_type);

  const runDelete = () => {
    deleteNode.mutate(node.instance_id, {
      onSuccess: () => toast.success(t(($) => $.cloud_runtime.toast_deleted)),
      onError: (err) =>
        toast.error(
          err instanceof Error
            ? err.message
            : t(($) => $.cloud_runtime.toast_delete_failed),
        ),
    });
  };
  const runReboot = () => {
    rebootNode.mutate(node.instance_id, {
      onSuccess: () => toast.success(t(($) => $.cloud_runtime.toast_restarted)),
      onError: (err) =>
        toast.error(
          err instanceof Error
            ? err.message
            : t(($) => $.cloud_runtime.toast_restart_failed),
        ),
    });
  };

  return (
    <div className="group rounded-md border bg-background px-3 py-2.5">
      <div className="flex min-w-0 items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate text-sm font-medium">{title}</span>
            <CloudRuntimeStatusBadge status={node.status} />
          </div>
          <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            <span>{node.instance_type}</span>
            {profile && (
              <>
                <span className="text-muted-foreground/40">/</span>
                <span>
                  {profile.cpu} / {profile.memory}
                </span>
              </>
            )}
            <span className="text-muted-foreground/40">/</span>
            <span>{node.region}</span>
            {created && (
              <>
                <span className="text-muted-foreground/40">/</span>
                <span>{created}</span>
              </>
            )}
          </div>
        </div>
        {isAdmin && confirming ? (
          <div className="flex shrink-0 items-center gap-1">
            <Button
              type="button"
              variant="destructive"
              size="sm"
              className="h-7 px-2 text-xs"
              disabled={deleteNode.isPending}
              onClick={runDelete}
            >
              {deleteNode.isPending ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Trash2 className="h-3.5 w-3.5" />
              )}
              {t(($) => $.cloud_runtime.delete_confirm_action)}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-7 px-2 text-xs text-muted-foreground"
              disabled={deleteNode.isPending}
              onClick={() => setConfirming(false)}
            >
              {t(($) => $.cloud_runtime.cancel)}
            </Button>
          </div>
        ) : isAdmin ? (
          <div className="flex shrink-0 items-center gap-1 opacity-80 transition-opacity group-hover:opacity-100">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-7 px-2 text-xs text-muted-foreground"
              disabled={rebootNode.isPending || !node.instance_id}
              onClick={runReboot}
            >
              {rebootNode.isPending ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <RefreshCw className="h-3.5 w-3.5" />
              )}
              {t(($) => $.cloud_runtime.restart)}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-7 w-7 shrink-0 p-0 text-muted-foreground hover:text-destructive"
              onClick={() => setConfirming(true)}
              aria-label={t(($) => $.cloud_runtime.delete)}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        ) : null}
      </div>
      {node.instance_id && (
        <div className="mt-2 truncate font-mono text-[11px] text-muted-foreground/80">
          {node.instance_id}
        </div>
      )}
    </div>
  );
}

function CloudRuntimeStatusBadge({ status }: { status: string }) {
  const normalized = status.toLowerCase();
  const active = new Set(["running", "success", "online"]);
  const pending = new Set([
    "launching",
    "pending",
    "starting",
    "stopping",
    "rebooting",
    "terminating",
  ]);
  const failed = new Set(["failed", "terminated", "error"]);
  return (
    <Badge
      variant="secondary"
      className={cn(
        "h-5 rounded-md px-1.5 font-mono text-[10px]",
        active.has(normalized) && "bg-success/10 text-success",
        pending.has(normalized) && "bg-warning/10 text-warning",
        failed.has(normalized) && "bg-destructive/10 text-destructive",
      )}
    >
      {status || "unknown"}
    </Badge>
  );
}

function valueOrUndefined(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed ? trimmed : undefined;
}

function formatDateTime(value: string): string | null {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}
