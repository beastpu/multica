"use client";

import { useMemo, useState } from "react";
import { AlertTriangle, History, Loader2 } from "lucide-react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  workflowInstanceInfiniteListOptions,
  workflowListOptions,
  type WorkflowInstance,
  type WorkflowInstanceFilters,
} from "@multica/core/workflows";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { cn } from "@multica/ui/lib/utils";
import {
  CollectionPageHeader,
  CollectionPageState,
} from "../layout/collection-page";
import { useT } from "../i18n";
import { AppLink } from "../navigation";
import { WorkflowStatusBadge } from "./workflow-status";

// A run is finished or it is not, and that is the only cut worth putting in
// front of someone reading history. The five-select filter bar this replaced
// asked for a project, an owner and a node key before showing a single row.
// "active"/"terminal" are the server's aggregate status values; failed counts
// as active because a failed run can still be reconciled or resumed.
type RunScope = "all" | "open" | "closed";

// History is read by comparing rows and matching them against something that
// happened elsewhere — a deploy, an incident, another run. "18 hours ago" does
// not survive either use, so the column carries the timestamp itself, in a
// form that sorts and reads the same in every locale.
function absoluteTime(value: string): string {
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return "—";
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())} ` +
    `${pad(at.getHours())}:${pad(at.getMinutes())}`;
}

export function WorkflowRunsPage({ workflowId }: { workflowId?: string }) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const [scope, setScope] = useState<RunScope>("all");
  const [workflow, setWorkflow] = useState(workflowId ?? "");

  const filters = useMemo<WorkflowInstanceFilters>(() => ({
    status: scope === "all"
      ? undefined
      : (scope === "open" ? "active" : "terminal"),
    workflow_id: workflow || undefined,
    limit: 50,
  }), [scope, workflow]);

  const runsQuery = useInfiniteQuery(
    workflowInstanceInfiniteListOptions(wsId, filters),
  );
  const workflowsQuery = useQuery(workflowListOptions(wsId));
  // Narrowed to one workflow, the page is that workflow's history and says so
  // — otherwise every workflow's history looks like the same page.
  const scopedWorkflow = (workflowsQuery.data?.workflows ?? []).find(
    (item) => item.id === workflow,
  );
  const runs = useMemo(
    () => runsQuery.data?.pages.flatMap((page) => page.instances) ?? [],
    [runsQuery.data],
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <CollectionPageHeader
        icon={History}
        title={scopedWorkflow
          ? t(($) => $.runs.workflow_title, { name: scopedWorkflow.name })
          : t(($) => $.runs.all_title)}
        count={runs.length}
        description={scopedWorkflow
          ? t(($) => $.runs.workflow_description)
          : t(($) => $.runs.description)}
      />
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="flex flex-wrap items-center gap-2 px-3 py-2">
          <div
            aria-label={t(($) => $.filters.status)}
            className="flex items-center gap-1"
          >
            {([
              ["all", t(($) => $.filters.all_statuses)],
              ["open", t(($) => $.runs.scope_open)],
              ["closed", t(($) => $.runs.scope_closed)],
            ] as Array<[RunScope, string]>).map(([value, label]) => (
              <button
                key={value}
                type="button"
                aria-pressed={scope === value}
                onClick={() => setScope(value)}
                className={cn(
                  "min-h-8 rounded-md px-2.5 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  scope === value
                    ? "bg-muted text-foreground"
                    : "text-muted-foreground hover:bg-muted/60",
                )}
              >
                {label}
              </button>
            ))}
          </div>
          <select
            aria-label={t(($) => $.filters.template)}
            value={workflow}
            onChange={(event) => setWorkflow(event.target.value)}
            className="ml-auto min-h-8 rounded-md border border-input bg-background px-2 text-xs"
          >
            <option value="">{t(($) => $.filters.all_templates)}</option>
            {(workflowsQuery.data?.workflows ?? []).map((item) => (
              <option key={item.id} value={item.id}>{item.name}</option>
            ))}
          </select>
        </div>

        {runsQuery.isError ? (
          <CollectionPageState
            icon={AlertTriangle}
            title={t(($) => $.errors.load)}
            tone="destructive"
            role="alert"
          />
        ) : runsQuery.isLoading ? (
          <div className="space-y-2 px-3 py-3">
            <Skeleton className="h-12 rounded-lg" />
            <Skeleton className="h-12 rounded-lg" />
            <Skeleton className="h-12 rounded-lg" />
          </div>
        ) : runs.length === 0 ? (
          <CollectionPageState
            icon={History}
            title={t(($) => $.runs.empty_title)}
            description={t(($) => $.runs.empty_description)}
          />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[52rem] border-collapse text-sm">
              <caption className="sr-only">{t(($) => $.runs.all_title)}</caption>
              <thead>
                <tr className="border-b text-xs text-muted-foreground">
                  <th scope="col" className="px-3 py-2 text-left font-medium">
                    {t(($) => $.runs.column_run)}
                  </th>
                  <th scope="col" className="px-3 py-2 text-left font-medium">
                    {t(($) => $.templates.column_status)}
                  </th>
                  <th scope="col" className="px-3 py-2 text-left font-medium">
                    {t(($) => $.runs.current_activity)}
                  </th>
                  <th scope="col" className="px-3 py-2 text-right font-medium">
                    {t(($) => $.runs.progress)}
                  </th>
                  <th scope="col" className="px-3 py-2 text-right font-medium">
                    {t(($) => $.runs.column_started)}
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {runs.map((run) => (
                  <RunRow
                    key={run.id}
                    run={run}
                    href={p.workflowRun(run.id)}
                  />
                ))}
              </tbody>
            </table>
            {runsQuery.hasNextPage && (
              <div className="flex justify-center px-3 py-4">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={runsQuery.isFetchingNextPage}
                  onClick={() => void runsQuery.fetchNextPage()}
                >
                  {runsQuery.isFetchingNextPage && (
                    <Loader2 aria-hidden="true" className="animate-spin" />
                  )}
                  {t(($) => $.actions.load_more)}
                </Button>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function RunRow({ run, href }: { run: WorkflowInstance; href: string }) {
  const { t } = useT("workflows");
  const current = run.current_activities?.[0];
  return (
    <tr className="h-12 transition-colors hover:bg-muted/30">
      <td className="max-w-[24rem] px-3 py-2">
        <AppLink
          href={href}
          className="block min-w-0 outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <span className="block truncate font-medium hover:underline">
            {run.title || run.host_issue_title ||
              t(($) => $.runs.untitled_run)}
          </span>
          <span className="block truncate text-xs text-muted-foreground">
            {run.workflow_name}
          </span>
        </AppLink>
      </td>
      <td className="px-3 py-2">
        <WorkflowStatusBadge status={run.status} />
      </td>
      <td className="max-w-[16rem] px-3 py-2">
        <span className="block truncate text-xs text-muted-foreground">
          {current ? current.name : "—"}
        </span>
      </td>
      <td className="px-3 py-2 text-right text-xs tabular-nums text-muted-foreground">
        {run.activity_total > 0
          ? `${run.activity_completed}/${run.activity_total}`
          : "—"}
      </td>
      <td className="px-3 py-2 text-right">
        <time
          dateTime={run.started_at}
          className="text-xs tabular-nums text-muted-foreground"
        >
          {absoluteTime(run.started_at)}
        </time>
      </td>
    </tr>
  );
}
