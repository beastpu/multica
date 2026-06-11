"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
} from "@multica/ui/components/ui/select";
import {
  feishuProjectIssueStatusesOptions,
  feishuProjectWorkItemTypesOptions,
} from "@multica/core/feishu-project/queries";
import { projectListOptions } from "@multica/core/projects";
import type {
  FeishuProjectWorkItemType,
  FeishuProjectWorkItemTypeConfig,
} from "@multica/core/types";
import { useT } from "../../i18n";

const MULTICA_STATUS_OPTIONS = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "blocked",
  "done",
  "cancelled",
] as const;

const NO_MAPPING = "__none__";
const ADD_TYPE = "__add__";
const TARGET_ROUTING = "__routing__";

export type SetWorkItemTypeConfigs = (
  updater: (prev: FeishuProjectWorkItemTypeConfig[]) => FeishuProjectWorkItemTypeConfig[],
) => void;

interface Props {
  workspaceId: string;
  // Gates the Meego registry / status queries so they only run once the basic
  // credentials exist (each call burns plugin-token quota).
  integrationReady: boolean;
  entries: FeishuProjectWorkItemTypeConfig[];
  setEntries: SetWorkItemTypeConfigs;
}

/**
 * "Synced work-item types" section: one card per type, each carrying its own
 * identifier prefix, sync target (business-line routing vs a pinned project)
 * and status mapping pair. Types are added from the space's type registry.
 */
export function FeishuProjectWorkItemTypesSection({
  workspaceId,
  integrationReady,
  entries,
  setEntries,
}: Props) {
  const { t } = useT("settings");

  const { data: registryData, isFetching: registryLoading } = useQuery({
    ...feishuProjectWorkItemTypesOptions(workspaceId, integrationReady),
  });
  const registry = registryData?.work_item_types ?? [];
  const addable = registry.filter(
    (item) => !entries.some((entry) => entry.type_key === item.type_key),
  );

  const { data: projects = [] } = useQuery(projectListOptions(workspaceId));

  function addType(typeKey: string) {
    const picked = registry.find((item) => item.type_key === typeKey);
    if (!picked) return;
    setEntries((prev) => {
      if (prev.some((entry) => entry.type_key === picked.type_key)) return prev;
      return [
        ...prev,
        {
          type_key: picked.type_key,
          api_name: picked.api_name,
          name: picked.name,
          identifier_prefix: suggestIdentifierPrefix(picked),
          project_id: "",
          status_mapping: {},
          reverse_status_mapping: {},
        },
      ];
    });
  }

  function patchEntry(
    typeKey: string,
    patch: (entry: FeishuProjectWorkItemTypeConfig) => FeishuProjectWorkItemTypeConfig,
  ) {
    setEntries((prev) => prev.map((entry) => (entry.type_key === typeKey ? patch(entry) : entry)));
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3 border-b border-border/70 pb-2">
        <div>
          <p className="text-xs font-medium text-muted-foreground">
            {t(($) => $.integrations.feishu_project_types_section)}
          </p>
          <p className="mt-1 text-[11px] text-muted-foreground">
            {t(($) => $.integrations.feishu_project_types_hint)}
          </p>
        </div>
        <Select value={ADD_TYPE} onValueChange={(v) => v && v !== ADD_TYPE && addType(v)}>
          <SelectTrigger size="sm" className="w-44">
            <span className="flex items-center gap-1.5">
              <Plus className="h-3.5 w-3.5" />
              {t(($) => $.integrations.feishu_project_types_add)}
            </span>
          </SelectTrigger>
          <SelectContent align="end">
            {addable.length === 0 && (
              <div className="px-2 py-1.5 text-xs text-muted-foreground">
                {registryLoading
                  ? t(($) => $.integrations.feishu_project_types_registry_loading)
                  : t(($) => $.integrations.feishu_project_types_registry_empty)}
              </div>
            )}
            {addable.map((item) => (
              <SelectItem key={item.type_key} value={item.type_key}>
                {item.name} ({item.api_name})
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {entries.length === 0 ? (
        <p className="rounded-md border border-border/70 px-3 py-3 text-xs text-muted-foreground">
          {t(($) => $.integrations.feishu_project_types_empty)}
        </p>
      ) : (
        entries.map((entry) => (
          <WorkItemTypeCard
            key={entry.type_key}
            workspaceId={workspaceId}
            integrationReady={integrationReady}
            entry={entry}
            projects={projects.map((p) => ({ id: p.id, title: p.title }))}
            onPatch={(patch) => patchEntry(entry.type_key, patch)}
            onRemove={() =>
              setEntries((prev) => prev.filter((item) => item.type_key !== entry.type_key))
            }
          />
        ))
      )}
    </div>
  );
}

// suggestIdentifierPrefix proposes the [PREFIX-id] title prefix for a freshly
// added type. BUG/REQ match the established conventions for built-in types;
// custom types fall back to a sanitized api_name.
function suggestIdentifierPrefix(item: FeishuProjectWorkItemType): string {
  if (item.type_key === "issue") return "BUG";
  if (item.api_name === "story") return "REQ";
  if (item.name.includes("工单")) return "TICKET";
  const cleaned = item.api_name.toUpperCase().replace(/[^A-Z0-9_]/g, "").slice(0, 16);
  return cleaned || "ITEM";
}

function WorkItemTypeCard({
  workspaceId,
  integrationReady,
  entry,
  projects,
  onPatch,
  onRemove,
}: {
  workspaceId: string;
  integrationReady: boolean;
  entry: FeishuProjectWorkItemTypeConfig;
  projects: Array<{ id: string; title: string }>;
  onPatch: (
    patch: (entry: FeishuProjectWorkItemTypeConfig) => FeishuProjectWorkItemTypeConfig,
  ) => void;
  onRemove: () => void;
}) {
  const { t } = useT("settings");

  const { data: statusesData } = useQuery({
    ...feishuProjectIssueStatusesOptions(workspaceId, integrationReady, entry.type_key),
  });
  const statuses = statusesData?.statuses ?? [];

  // The mapping tables only render rows for the live state flow, so a status
  // that left the flow is hidden but its saved mapping is preserved as-is — we
  // deliberately do NOT prune it on load. A transient Meego outage can return a
  // partial status list; pruning against it would silently delete the
  // operator's valid mappings before they ever hit Save. A stale key that no
  // longer matches any Feishu item is harmless (it just never matches).

  const target = entry.project_id ? entry.project_id : TARGET_ROUTING;

  return (
    <div className="space-y-4 rounded-md border border-border/70 p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm font-medium">
            {entry.name || entry.type_key}
            {entry.api_name && (
              <span className="ml-1.5 font-mono text-[11px] font-normal text-muted-foreground">
                {entry.api_name}
              </span>
            )}
          </p>
        </div>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={onRemove}
          aria-label={t(($) => $.integrations.feishu_project_type_remove)}
        >
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <label className="space-y-1.5 text-xs font-medium">
          {t(($) => $.integrations.feishu_project_type_identifier_prefix)}
          <Input
            value={entry.identifier_prefix ?? ""}
            onChange={(e) =>
              onPatch((prev) => ({
                ...prev,
                identifier_prefix: e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "").slice(0, 16),
              }))
            }
            placeholder="BUG"
          />
          <span className="block text-[11px] font-normal text-muted-foreground">
            {t(($) => $.integrations.feishu_project_type_identifier_prefix_hint)}
          </span>
        </label>

        <label className="space-y-1.5 text-xs font-medium">
          {t(($) => $.integrations.feishu_project_type_target)}
          <Select
            value={target}
            onValueChange={(v) =>
              onPatch((prev) => ({ ...prev, project_id: !v || v === TARGET_ROUTING ? "" : v }))
            }
          >
            <SelectTrigger size="sm" className="w-full">
              <span className="flex-1 truncate text-left">
                {entry.project_id
                  ? (projects.find((p) => p.id === entry.project_id)?.title ?? entry.project_id)
                  : t(($) => $.integrations.feishu_project_type_target_routing)}
              </span>
            </SelectTrigger>
            <SelectContent align="start">
              <SelectItem value={TARGET_ROUTING}>
                {t(($) => $.integrations.feishu_project_type_target_routing)}
              </SelectItem>
              {projects.length === 0 && (
                <div className="max-w-56 px-2 py-1.5 text-xs text-muted-foreground">
                  {t(($) => $.integrations.feishu_project_routes_no_projects)}
                </div>
              )}
              {projects.map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.title}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <span className="block text-[11px] font-normal text-muted-foreground">
            {t(($) => $.integrations.feishu_project_type_target_hint)}
          </span>
        </label>
      </div>

      {statuses.length === 0 ? (
        <p className="rounded-md border border-border/70 px-3 py-3 text-xs text-muted-foreground">
          {t(($) => $.integrations.feishu_project_statuses_empty)}
        </p>
      ) : (
        <FeishuProjectStatusMappingTables
          statuses={statuses}
          statusMapping={entry.status_mapping}
          reverseStatusMapping={entry.reverse_status_mapping}
          onStatusMappingChange={(key, value) =>
            onPatch((prev) => ({ ...prev, status_mapping: setMappingValue(prev.status_mapping, key, value) }))
          }
          onReverseStatusMappingChange={(key, value) =>
            onPatch((prev) => ({
              ...prev,
              reverse_status_mapping: setMappingValue(prev.reverse_status_mapping, key, value),
            }))
          }
        />
      )}
    </div>
  );
}

// Forward + reverse status mapping grids for one work-item type.
function FeishuProjectStatusMappingTables({
  statuses,
  statusMapping,
  reverseStatusMapping,
  onStatusMappingChange,
  onReverseStatusMappingChange,
}: {
  statuses: Array<{ key: string; name: string }>;
  statusMapping: Record<string, string>;
  reverseStatusMapping: Record<string, string>;
  onStatusMappingChange: (key: string, value: string) => void;
  onReverseStatusMappingChange: (key: string, value: string) => void;
}) {
  const { t } = useT("settings");
  const statusKeys = useMemo(() => new Set(statuses.map((status) => status.key)), [statuses]);

  return (
    <div className="grid gap-5 xl:grid-cols-2">
      <div className="space-y-2">
        <p className="text-xs font-medium">
          {t(($) => $.integrations.feishu_project_status_mapping)}
        </p>
        <div className="overflow-hidden rounded-md border border-border/70">
          {statuses.map((status) => (
            <div key={status.key} className="grid grid-cols-[1fr_180px] items-center gap-3 border-b border-border/70 px-3 py-2 last:border-b-0">
              <div className="min-w-0">
                <p className="truncate text-xs font-medium">{status.name}</p>
                <p className="truncate font-mono text-[11px] text-muted-foreground">{status.key}</p>
              </div>
              <Select
                value={statusMapping[status.key] || NO_MAPPING}
                onValueChange={(value) => onStatusMappingChange(status.key, value || NO_MAPPING)}
              >
                <SelectTrigger size="sm" className="w-full">
                  <span className="flex-1 truncate text-left">
                    {statusMapping[status.key] || t(($) => $.integrations.feishu_project_no_mapping)}
                  </span>
                </SelectTrigger>
                <SelectContent align="start">
                  <SelectItem value={NO_MAPPING}>{t(($) => $.integrations.feishu_project_no_mapping)}</SelectItem>
                  {MULTICA_STATUS_OPTIONS.map((option) => (
                    <SelectItem key={option} value={option}>{option}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          ))}
        </div>
      </div>

      <div className="space-y-2">
        <p className="text-xs font-medium">
          {t(($) => $.integrations.feishu_project_reverse_mapping)}
        </p>
        <p className="text-[11px] text-muted-foreground">
          {t(($) => $.integrations.feishu_project_reverse_mapping_disable_hint)}
        </p>
        <div className="overflow-hidden rounded-md border border-border/70">
          {MULTICA_STATUS_OPTIONS.map((status) => {
            const current = reverseStatusMapping[status];
            const selected = current && statusKeys.has(current) ? current : NO_MAPPING;
            return (
              <div key={status} className="grid grid-cols-[1fr_180px] items-center gap-3 border-b border-border/70 px-3 py-2 last:border-b-0">
                <p className="font-mono text-xs font-medium">{status}</p>
                <Select
                  value={selected}
                  onValueChange={(value) => onReverseStatusMappingChange(status, value || NO_MAPPING)}
                >
                  <SelectTrigger size="sm" className="w-full">
                    <span className="flex-1 truncate text-left">
                      {selected === NO_MAPPING
                        ? t(($) => $.integrations.feishu_project_no_mapping)
                        : statusOptionLabel(statuses, selected)}
                    </span>
                  </SelectTrigger>
                  <SelectContent align="start">
                    <SelectItem value={NO_MAPPING}>{t(($) => $.integrations.feishu_project_no_mapping)}</SelectItem>
                    {statuses.map((option) => (
                      <SelectItem key={option.key} value={option.key}>
                        {option.name} ({option.key})
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

// "No mapping" drops the key entirely (both directions): an unmapped Feishu
// status is not synced, identical to one the operator never touched. Keeping an
// empty-string value instead would still pull that status into the sync scope.
function setMappingValue(mapping: Record<string, string>, key: string, value: string): Record<string, string> {
  const next = { ...mapping };
  if (!value || value === NO_MAPPING) {
    delete next[key];
  } else {
    next[key] = value;
  }
  return next;
}

function statusOptionLabel(options: Array<{ key: string; name: string }>, key: string): string {
  const option = options.find((item) => item.key === key);
  return option ? `${option.name} (${option.key})` : key;
}
