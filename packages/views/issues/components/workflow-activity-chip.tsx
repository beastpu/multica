"use client";

import { GitBranch } from "lucide-react";
import type { IssueWorkflowContext } from "@multica/core/types";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n";

export function WorkflowActivityChip({
  context,
  className = "",
}: {
  context: IssueWorkflowContext;
  className?: string;
}) {
  const { t } = useT("issues");
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            className={`inline-flex max-w-44 shrink-0 items-center gap-1 rounded-full border border-sky-600/25 bg-sky-600/10 px-1.5 py-0.5 text-[11px] font-medium text-sky-800 dark:text-sky-200 ${className}`}
          />
        }
      >
        <GitBranch className="size-3" aria-hidden />
        <span className="truncate">{context.activity_name || context.activity_key}</span>
      </TooltipTrigger>
      <TooltipContent className="max-w-80 space-y-1">
        <p className="font-medium">{context.workflow_template_name}</p>
        <p>
          {t(($) => $.workflow_context.host, {
            identifier: context.host_issue_identifier,
            title: context.host_issue_title,
          })}
        </p>
        <p>
          {context.required
            ? t(($) => $.workflow_context.required)
            : t(($) => $.workflow_context.optional)}
        </p>
      </TooltipContent>
    </Tooltip>
  );
}
