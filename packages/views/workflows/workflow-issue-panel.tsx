"use client";

import type { Issue } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { ArrowLeft, ChevronDown, ChevronUp } from "lucide-react";

import { useT } from "../i18n";
import { IssueDetail } from "../issues/components";

// WorkflowIssuePanel reads one issue inside the run.
//
// Reading an issue here is mostly reading its comments, which is why this is a
// resizable panel rather than a popover: the thread needs width and scroll. It
// replaces the node sidebar instead of stacking beside it — a third column
// would leave none of them wide enough to read.
//
// Prev/next move within the node's own issues so a reviewer can sweep the
// whole node without going back to the board between each one; that round trip
// is the thing this panel exists to remove.
export function WorkflowIssuePanel({
  issueId,
  issues,
  onClose,
  onNavigate,
}: {
  issueId: string;
  issues: Issue[];
  onClose: () => void;
  onNavigate: (issueId: string) => void;
}) {
  const { t } = useT("workflows");
  const index = issues.findIndex((issue) => issue.id === issueId);
  const previous = index > 0 ? issues[index - 1] : undefined;
  const next = index >= 0 && index < issues.length - 1
    ? issues[index + 1]
    : undefined;

  return (
    <div className="-m-4 flex min-h-full flex-col">
      <div className="flex shrink-0 items-center justify-between gap-2 border-b bg-muted/15 px-3 py-2">
        <Button
          variant="ghost"
          size="sm"
          onClick={onClose}
          className="gap-1.5"
        >
          <ArrowLeft className="size-3.5" />
          {t(($) => $.workbench.back_to_node)}
        </Button>
        {issues.length > 1 && (
          <div className="flex items-center gap-1">
            <span className="mr-1 text-xs text-muted-foreground">
              {index >= 0 ? index + 1 : "-"}/{issues.length}
            </span>
            <Button
              variant="ghost"
              size="icon-sm"
              disabled={!previous}
              onClick={() => previous && onNavigate(previous.id)}
              aria-label={t(($) => $.workbench.previous_issue)}
            >
              <ChevronUp className="size-3.5" />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              disabled={!next}
              onClick={() => next && onNavigate(next.id)}
              aria-label={t(($) => $.workbench.next_issue)}
            >
              <ChevronDown className="size-3.5" />
            </Button>
          </div>
        )}
      </div>
      <div className="min-h-0 flex-1">
        {/*
          The issue's own property sidebar stays closed: this panel is already
          a sidebar, and the run's board is what the remaining width is for.
          A dedicated layoutId keeps the user's width here from fighting the
          full-page issue view's saved layout.
        */}
        <IssueDetail
          key={issueId}
          issueId={issueId}
          defaultSidebarOpen={false}
          layoutId="multica_workflow_issue_panel_layout"
        />
      </div>
    </div>
  );
}
