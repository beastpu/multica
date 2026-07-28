"use client";

import { useUpdateIssue } from "@multica/core/issues/mutations";
import { useWorkspacePaths } from "@multica/core/paths";
import type { Issue } from "@multica/core/types";
import { ArrowUpRight } from "lucide-react";

import { useT } from "../i18n";
import { StatusIcon } from "../issues/components/status-icon";
import { StatusPicker } from "../issues/components/pickers";
import { AppLink } from "../navigation";

// WorkflowNodeIssues lists the issues a node produced, with the status control
// inline.
//
// The node panel used to show only a completed/total count, so the one action
// this surface exists for — moving a node's issue to done — required leaving
// the run, changing the status on the issue page, and coming back to find
// where you were. The statuses were already in the client (the count is
// derived from them); only the control was missing.
//
// Status is the only field edited here on purpose. It is what gates the node,
// and a row that edits everything becomes an issue page — at which point the
// user is back to reading one issue at a time instead of driving the run.
export function WorkflowNodeIssues({
  issues,
  canManage,
}: {
  issues: Issue[];
  canManage: boolean;
}) {
  const { t } = useT("workflows");
  const p = useWorkspacePaths();
  const updateIssue = useUpdateIssue();

  if (issues.length === 0) {
    return null;
  }

  return (
    <section className="space-y-2">
      {/* Same heading rule as the rest of the node panel: xs and muted, so the
          issue titles below are what the eye lands on. */}
      <h3 className="text-xs font-medium text-muted-foreground">
        {t(($) => $.workbench.node_issues)}
      </h3>
      <ul className="divide-y rounded-xl border bg-surface">
        {issues.map((issue) => (
          <li
            key={issue.id}
            className="flex items-center gap-2 px-3 py-2"
          >
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
    </section>
  );
}
