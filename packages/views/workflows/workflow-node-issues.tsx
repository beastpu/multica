"use client";

import { useUpdateIssue } from "@multica/core/issues/mutations";
import { useWorkspacePaths } from "@multica/core/paths";
import type { Issue } from "@multica/core/types";
import { ArrowUpRight, Check } from "lucide-react";
import type { ReactNode } from "react";
import { WORKFLOW_SECTION_HEADING } from "./workflow-status";

import { useT } from "../i18n";
import { StatusIcon } from "../issues/components/status-icon";
import { StatusPicker } from "../issues/components/pickers";
import { AppLink } from "../navigation";

// WorkflowNodeIssues is the node's "what is left" block: what still blocks it,
// the rule that decides blocked, and the action that closes it.
//
// The three used to be separate — a completion-rule section, an issue list,
// and a node-operations card at the bottom of the panel — which put the
// surface's primary action below four tabs of reference material. They are one
// question ("can this node move, and if not, why") so they are one block.
//
// Only blocking issues are listed. The board beside the panel already shows
// every issue by status; repeating that asks the same information to be read
// twice, while "what is still missing" is what the board cannot say.
//
// Status is the only field editable here. It is what gates the node, and a row
// that edits everything becomes an issue page — the surface being avoided.
export function WorkflowNodeIssues({
  issues,
  canManage,
  rule,
  completed,
  total,
  action,
  attempt,
  blockers,
}: {
  issues: Issue[];
  canManage: boolean;
  /** Plain-language statement of what the node needs to advance. */
  rule?: string;
  completed?: number;
  /** 0 when the node has no required-issue rule; the count is then hidden. */
  total?: number;
  /** The node's transition controls, rendered under the list. */
  action?: ReactNode;
  /**
   * The node's current attempt. Rework continues on the same issue, so past 1
   * the identifier alone no longer says this work has been sent back before.
   */
  attempt?: number;
  /**
   * Why the node has not advanced, rendered above the rule that defines
   * "advanced". They answer one question between them and used to sit in two
   * separate blocks with the tab strip in between.
   */
  blockers?: ReactNode;
}) {
  const { t } = useT("workflows");
  const p = useWorkspacePaths();
  const updateIssue = useUpdateIssue();
  const showCount = typeof total === "number" && total > 0;

  return (
    <section className="space-y-2 border-y py-4">
      {blockers}
      <div className="flex items-center justify-between gap-3">
        <h3 className={WORKFLOW_SECTION_HEADING}>
          {t(($) => $.workbench.node_issues)}
        </h3>
        {showCount && (
          <span className="text-xs tabular-nums text-muted-foreground">
            {completed}/{total}
          </span>
        )}
      </div>

      {issues.length > 0
        ? (
          <ul className="divide-y rounded-xl border bg-surface">
            {issues.map((issue) => (
              <li key={issue.id} className="flex items-center gap-2 px-3 py-2">
                {canManage
                  ? (
                    <StatusPicker
                      status={issue.status}
                      align="start"
                      onUpdate={(updates) =>
                        updateIssue.mutate({ id: issue.id, ...updates })}
                    />
                  )
                  : (
                    <StatusIcon
                      status={issue.status}
                      className="size-3.5 shrink-0"
                    />
                  )}
                <span className="shrink-0 font-mono text-xs text-muted-foreground">
                  {issue.identifier}
                </span>
                {typeof attempt === "number" && attempt > 1 && (
                  <span
                    className="shrink-0 rounded-full bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 tabular-nums dark:text-amber-400"
                    title={t(($) => $.workbench.rework_badge_title)}
                  >
                    {t(($) => $.workbench.rework_badge, { attempt })}
                  </span>
                )}
                <span className="min-w-0 flex-1 truncate text-sm">
                  {issue.title}
                </span>
                <AppLink
                  href={p.issueDetail(issue.id)}
                  className="inline-flex min-h-11 shrink-0 items-center rounded-md px-2 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:min-h-8"
                  aria-label={`${t(($) => $.workbench.open_issue)}: ${issue.identifier}`}
                >
                  <ArrowUpRight className="size-3.5" />
                </AppLink>
              </li>
            ))}
          </ul>
        )
        : (
          // Nothing blocking is a result worth stating outright — an empty
          // area reads as "not loaded yet" rather than "you are clear".
          <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
            <Check className="size-3.5 shrink-0 text-primary" aria-hidden="true" />
            {t(($) => $.workbench.node_issues_clear)}
          </p>
        )}

      {rule && (
        <p className="text-xs text-muted-foreground">{rule}</p>
      )}
      {action}
    </section>
  );
}
