"use client";

import { ScrollText } from "lucide-react";
import type { AgentTask } from "@multica/core/types/agent";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { TranscriptButton } from "../common/task-transcript";
import { WorkflowStatusBadge, WORKFLOW_SECTION_HEADING } from "./workflow-status";

const LIVE_EXECUTION_STATUSES = new Set<AgentTask["status"]>([
  "queued",
  "dispatched",
  "waiting_local_directory",
  "running",
]);

interface WorkflowExecutionHistoryProps {
  executions: AgentTask[];
  agentName: (agentId: string) => string;
}

export function WorkflowExecutionHistory({
  executions,
  agentName,
}: WorkflowExecutionHistoryProps) {
  const { t } = useT("workflows");

  if (executions.length === 0) return null;

  return (
    <section
      aria-labelledby="workflow-execution-history-heading"
      className="overflow-hidden rounded-lg border bg-muted/10"
    >
      <div className="flex items-center justify-between gap-3 border-b px-3 py-2.5">
        <h3
          id="workflow-execution-history-heading"
          className={cn(WORKFLOW_SECTION_HEADING, "flex items-center gap-1.5")}
        >
          <ScrollText aria-hidden="true" className="size-3.5" />
          {t(($) => $.workbench.execution_history)}
        </h3>
        <span className="text-xs tabular-nums text-muted-foreground">
          {t(($) => $.workbench.execution_count, {
            count: executions.length,
          })}
        </span>
      </div>
      <div className="divide-y">
        {executions.map((execution) => {
          const name = agentName(execution.agent_id);
          const timestamp = execution.started_at ?? execution.created_at;
          return (
            <div
              key={execution.id}
              className="flex min-w-0 items-center gap-2 px-3 py-2.5"
            >
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 items-center gap-2">
                  <span className="truncate text-sm font-medium">{name}</span>
                  {(execution.attempt ?? 1) > 1 && (
                    <span className="shrink-0 text-[11px] text-muted-foreground">
                      {t(($) => $.workbench.execution_attempt, {
                        attempt: execution.attempt ?? 1,
                      })}
                    </span>
                  )}
                </div>
                <time
                  dateTime={timestamp}
                  className="mt-0.5 block truncate text-[11px] text-muted-foreground"
                >
                  {new Date(timestamp).toLocaleString()}
                </time>
              </div>
              <WorkflowStatusBadge status={execution.status} />
              <TranscriptButton
                task={execution}
                agentName={name}
                isLive={LIVE_EXECUTION_STATUSES.has(execution.status)}
                label={t(($) => $.workbench.view_execution)}
                title={t(($) => $.workbench.view_execution)}
                className="shrink-0 px-2 py-1 text-xs font-medium"
              />
            </div>
          );
        })}
      </div>
    </section>
  );
}
