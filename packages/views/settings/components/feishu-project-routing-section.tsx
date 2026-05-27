"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Loader2, RefreshCw, Save, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@multica/ui/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
} from "@multica/ui/components/ui/select";
import {
  feishuProjectBusinessLinesOptions,
  feishuProjectFieldsOptions,
  feishuProjectKeys,
  feishuProjectRoutesOptions,
  useReplaceFeishuProjectRoutes,
} from "@multica/core/feishu-project/queries";
import { projectListOptions } from "@multica/core/projects";
import type {
  FeishuProjectBusinessLineNode,
  FeishuProjectIntegration,
  FeishuProjectRouteInput,
} from "@multica/core/types";
import { useT } from "../../i18n";

interface Props {
  workspaceId: string;
  integration: FeishuProjectIntegration | null;
  onFieldChanged: (fieldKey: string, fieldName: string) => void;
}

// Each user-edited row in the routes table. business_line_id is the lookup key; we keep
// the parent denormalized so we can save it back to the server without re-fetching the
// tree at save time.
interface RouteRow {
  businessLineId: string;
  businessLineName: string;
  parentBusinessLineId: string;
  parentBusinessLineName: string;
  projectId: string;
}

const NO_PROJECT = "__none__";

/**
 * Business-line → project routing UI for a Feishu Project integration.
 *
 * Flow:
 *  1. User picks the "business-line field" (Meego field name varies per space, so we
 *     fetch the field list and let them pick).
 *  2. Once a field is chosen, we fetch the 2-level biz-line tree from Meego and render
 *     it as a checkbox tree.
 *  3. For each selected biz-line node, the user picks a workspace-local project from a
 *     dropdown.
 *  4. Save writes the full route table atomically (PUT replaces).
 *
 * Save here only commits routes — the field-key choice is part of the parent
 * integration form (so it flushes with the rest of the integration on its own Save).
 */
export function FeishuProjectRoutingSection({ workspaceId, integration, onFieldChanged }: Props) {
  const { t } = useT("settings");
  const queryClient = useQueryClient();

  const integrationReady = Boolean(integration?.id && integration.has_plugin_secret);
  const fieldKey = integration?.business_line_field_key ?? "";
  const hasFieldKey = fieldKey.trim() !== "";

  const { data: fieldsData, isFetching: fieldsLoading, refetch: refetchFields } = useQuery({
    ...feishuProjectFieldsOptions(workspaceId, "issue", integrationReady),
  });
  const fields = fieldsData?.fields ?? [];

  const { data: businessLinesData, isFetching: bizLinesLoading, refetch: refetchBusinessLines } = useQuery({
    ...feishuProjectBusinessLinesOptions(workspaceId, integrationReady && hasFieldKey),
  });
  const businessLines = businessLinesData?.business_lines ?? [];

  const { data: routesData } = useQuery({
    ...feishuProjectRoutesOptions(workspaceId, integrationReady),
  });
  const savedRoutes = routesData?.routes ?? [];

  const { data: projects = [] } = useQuery(projectListOptions(workspaceId));

  // Local draft state. Seeded from saved routes; user edits stay local until Save.
  const [rows, setRows] = useState<RouteRow[]>([]);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  useEffect(() => {
    setRows(
      savedRoutes.map((r) => ({
        businessLineId: r.business_line_id,
        businessLineName: r.business_line_name,
        parentBusinessLineId: r.parent_business_line_id ?? "",
        parentBusinessLineName: r.parent_business_line_name ?? "",
        projectId: r.project_id,
      })),
    );
    // Auto-expand any parent whose child is currently routed, so the user can see the
    // existing selection without manually clicking the disclosure.
    const auto: Record<string, boolean> = {};
    for (const r of savedRoutes) {
      const parentId = r.parent_business_line_id ?? "";
      if (parentId) auto[parentId] = true;
    }
    setExpanded(auto);
  }, [savedRoutes]);

  const rowsByBizLineId = useMemo(() => {
    const map = new Map<string, RouteRow>();
    for (const r of rows) map.set(r.businessLineId, r);
    return map;
  }, [rows]);

  const replaceRoutes = useReplaceFeishuProjectRoutes(workspaceId);

  function toggleNode(node: FeishuProjectBusinessLineNode, parent: FeishuProjectBusinessLineNode | null) {
    setRows((prev) => {
      const exists = prev.find((r) => r.businessLineId === node.id);
      if (exists) {
        return prev.filter((r) => r.businessLineId !== node.id);
      }
      return [
        ...prev,
        {
          businessLineId: node.id,
          businessLineName: node.name,
          parentBusinessLineId: parent?.id ?? node.parent_id ?? "",
          parentBusinessLineName: parent?.name ?? node.parent_name ?? "",
          projectId: "",
        },
      ];
    });
  }

  function setRowProject(bizLineId: string, projectId: string | null) {
    const next = projectId && projectId !== NO_PROJECT ? projectId : "";
    setRows((prev) =>
      prev.map((r) => (r.businessLineId === bizLineId ? { ...r, projectId: next } : r)),
    );
  }

  function removeRow(bizLineId: string) {
    setRows((prev) => prev.filter((r) => r.businessLineId !== bizLineId));
  }

  async function handleSave() {
    // Validate: every row must have a project chosen. Validate here (not on the
    // backend's 400 response) so the user sees the bad row inline instead of a toast.
    const missing = rows.find((r) => !r.projectId);
    if (missing) {
      toast.error(
        t(($) => $.integrations.feishu_project_routes_missing_project, {
          name: missing.businessLineName || missing.businessLineId,
        }),
      );
      return;
    }
    const payload: FeishuProjectRouteInput[] = rows.map((r) => ({
      project_id: r.projectId,
      business_line_id: r.businessLineId,
      business_line_name: r.businessLineName,
      parent_business_line_id: r.parentBusinessLineId || undefined,
      parent_business_line_name: r.parentBusinessLineName || undefined,
    }));
    try {
      await replaceRoutes.mutateAsync({ routes: payload });
      toast.success(t(($) => $.integrations.feishu_project_routes_saved));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.integrations.feishu_project_routes_save_failed));
    }
  }

  async function handleRefreshFields() {
    try {
      const r = await refetchFields();
      if (r.error) {
        toast.error(r.error instanceof Error ? r.error.message : t(($) => $.integrations.feishu_project_fields_refresh_failed));
        return;
      }
      toast.success(t(($) => $.integrations.feishu_project_fields_refreshed, { count: r.data?.fields.length ?? 0 }));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.integrations.feishu_project_fields_refresh_failed));
    }
  }

  async function handleRefreshBusinessLines() {
    try {
      // Invalidate to bypass staleTime: Infinity, otherwise refetch is a no-op.
      await queryClient.invalidateQueries({ queryKey: feishuProjectKeys.businessLines(workspaceId) });
      const r = await refetchBusinessLines();
      if (r.error) {
        toast.error(r.error instanceof Error ? r.error.message : t(($) => $.integrations.feishu_project_business_lines_refresh_failed));
        return;
      }
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.integrations.feishu_project_business_lines_refresh_failed));
    }
  }

  function handlePickField(value: string | null) {
    if (!value) return;
    const chosen = fields.find((f) => f.key === value);
    onFieldChanged(value, chosen?.name ?? "");
    // Clear routes whose parent context no longer matches — when the field changes the
    // entire biz-line tree may differ. The user has to re-pick.
    setRows([]);
    setExpanded({});
  }

  if (!integrationReady) {
    return (
      <p className="rounded-md border border-border/70 px-3 py-3 text-xs text-muted-foreground">
        {t(($) => $.integrations.feishu_project_routes_needs_basic)}
      </p>
    );
  }

  return (
    <div className="space-y-4">
      {/* Field picker row */}
      <div className="grid gap-3 md:grid-cols-[1fr_auto] md:items-end">
        <label className="space-y-1.5 text-xs font-medium">
          {t(($) => $.integrations.feishu_project_business_line_field)}
          <Select value={fieldKey || ""} onValueChange={handlePickField}>
            <SelectTrigger className="w-full" disabled={fieldsLoading}>
              <span className="flex-1 truncate text-left">
                {fieldKey
                  ? (fields.find((f) => f.key === fieldKey)?.name ?? fieldKey)
                  : t(($) => $.integrations.feishu_project_business_line_field_placeholder)}
              </span>
            </SelectTrigger>
            <SelectContent align="start">
              {fields.length === 0 && (
                <div className="px-2 py-1.5 text-xs text-muted-foreground">
                  {fieldsLoading
                    ? t(($) => $.integrations.feishu_project_fields_loading)
                    : t(($) => $.integrations.feishu_project_fields_empty)}
                </div>
              )}
              {fields.map((f) => (
                <SelectItem key={f.key} value={f.key}>
                  {f.name} <span className="text-muted-foreground">({f.key})</span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </label>
        <Button type="button" size="sm" variant="outline" onClick={handleRefreshFields} disabled={fieldsLoading}>
          {fieldsLoading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
          {t(($) => $.integrations.feishu_project_refresh_fields)}
        </Button>
      </div>

      {hasFieldKey && (
        <>
          <div className="flex items-center justify-between gap-3 border-b border-border/70 pb-2">
            <p className="text-xs font-medium text-muted-foreground">
              {t(($) => $.integrations.feishu_project_business_lines_tree)}
            </p>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={handleRefreshBusinessLines}
              disabled={bizLinesLoading}
            >
              {bizLinesLoading ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <RefreshCw className="h-3.5 w-3.5" />
              )}
              {t(($) => $.integrations.feishu_project_refresh_business_lines)}
            </Button>
          </div>

          {businessLines.length === 0 ? (
            <p className="rounded-md border border-border/70 px-3 py-3 text-xs text-muted-foreground">
              {bizLinesLoading
                ? t(($) => $.integrations.feishu_project_business_lines_loading)
                : t(($) => $.integrations.feishu_project_business_lines_empty)}
            </p>
          ) : (
            <div className="overflow-hidden rounded-md border border-border/70">
              {businessLines.map((parent) => (
                <BizLineTreeRow
                  key={parent.id || parent.name}
                  node={parent}
                  parent={null}
                  expanded={expanded}
                  setExpanded={setExpanded}
                  rowsByBizLineId={rowsByBizLineId}
                  toggleNode={toggleNode}
                />
              ))}
            </div>
          )}

          {rows.length > 0 && (
            <div className="space-y-2">
              <p className="text-xs font-medium">
                {t(($) => $.integrations.feishu_project_routes_table_title)}
              </p>
              <div className="overflow-hidden rounded-md border border-border/70">
                {rows.map((row) => {
                  const projectChoices = projects.map((p) => ({ id: p.id, title: p.title }));
                  return (
                    <div
                      key={row.businessLineId}
                      className="grid grid-cols-[1fr_260px_auto] items-center gap-3 border-b border-border/70 px-3 py-2 last:border-b-0"
                    >
                      <div className="min-w-0">
                        <p className="truncate text-xs font-medium">
                          {row.businessLineName || row.businessLineId}
                        </p>
                        {row.parentBusinessLineName && (
                          <p className="truncate text-[11px] text-muted-foreground">
                            {row.parentBusinessLineName} / {row.businessLineName || row.businessLineId}
                          </p>
                        )}
                      </div>
                      <Select
                        value={row.projectId || NO_PROJECT}
                        onValueChange={(v) => setRowProject(row.businessLineId, v)}
                      >
                        <SelectTrigger size="sm" className="w-full">
                          <span className="flex-1 truncate text-left">
                            {row.projectId
                              ? (projectChoices.find((p) => p.id === row.projectId)?.title ?? row.projectId)
                              : t(($) => $.integrations.feishu_project_routes_pick_project)}
                          </span>
                        </SelectTrigger>
                        <SelectContent align="start">
                          {projectChoices.length === 0 && (
                            <div className="px-2 py-1.5 text-xs text-muted-foreground">
                              {t(($) => $.integrations.feishu_project_routes_no_projects)}
                            </div>
                          )}
                          {projectChoices.map((p) => (
                            <SelectItem key={p.id} value={p.id}>
                              {p.title}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        onClick={() => removeRow(row.businessLineId)}
                        aria-label={t(($) => $.integrations.feishu_project_routes_remove)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  );
                })}
              </div>
              <div className="flex justify-end">
                <Button size="sm" onClick={handleSave} disabled={replaceRoutes.isPending}>
                  {replaceRoutes.isPending ? (
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  ) : (
                    <Save className="h-3.5 w-3.5" />
                  )}
                  {t(($) => $.integrations.feishu_project_routes_save)}
                </Button>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}

interface RowProps {
  node: FeishuProjectBusinessLineNode;
  parent: FeishuProjectBusinessLineNode | null;
  expanded: Record<string, boolean>;
  setExpanded: (updater: (prev: Record<string, boolean>) => Record<string, boolean>) => void;
  rowsByBizLineId: Map<string, RouteRow>;
  toggleNode: (node: FeishuProjectBusinessLineNode, parent: FeishuProjectBusinessLineNode | null) => void;
}

function BizLineTreeRow({ node, parent, expanded, setExpanded, rowsByBizLineId, toggleNode }: RowProps) {
  const hasChildren = (node.children?.length ?? 0) > 0;
  const isOpen = expanded[node.id];
  const checked = rowsByBizLineId.has(node.id);
  const depth = parent ? 1 : 0;

  return (
    <>
      <div
        className="flex items-center gap-2 border-b border-border/70 px-3 py-1.5 last:border-b-0"
        style={{ paddingLeft: `${0.75 + depth * 1.25}rem` }}
      >
        {hasChildren ? (
          <button
            type="button"
            className="flex h-5 w-5 items-center justify-center rounded hover:bg-muted"
            onClick={() => setExpanded((prev) => ({ ...prev, [node.id]: !prev[node.id] }))}
            aria-label={isOpen ? "collapse" : "expand"}
          >
            {isOpen ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
          </button>
        ) : (
          <span className="inline-block h-5 w-5" />
        )}
        <input
          type="checkbox"
          className="h-3.5 w-3.5"
          checked={checked}
          onChange={() => toggleNode(node, parent)}
        />
        <span className="truncate text-xs">
          {node.name || node.id}
        </span>
      </div>
      {hasChildren && isOpen && (
        <>
          {(node.children ?? []).map((child) => (
            <BizLineTreeRow
              key={child.id || child.name}
              node={child}
              parent={node}
              expanded={expanded}
              setExpanded={setExpanded}
              rowsByBizLineId={rowsByBizLineId}
              toggleNode={toggleNode}
            />
          ))}
        </>
      )}
    </>
  );
}
