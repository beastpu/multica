"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight, GitBranch } from "lucide-react";
import { useWorkspaceFeatureEnabled } from "@multica/core/config";
import { WORKFLOWS_ACTIVITY_ENGINE_FLAG } from "@multica/core/feature-flags";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { issueWorkflowOptions, type WorkflowInstance } from "@multica/core/workflows";
import { useT } from "../i18n";
import { AppLink } from "../navigation";
import { IssueDetail } from "../issues/components";
import { ArtifactAdoptProvider } from "./artifact-adopt-provider";
import { WorkflowNodeContextPanel } from "./workflow-node-context-panel";
import { WorkflowStartDialog } from "./workflow-start-dialog";
import { WorkflowStatusBadge } from "./workflow-status";

// The banner is how a host issue says it is under a workflow. It used to say it
// by becoming the workbench, which cost the issue its own page: description,
// comments, attachments and sub-issues had no route left, and the workbench's
// own "open parent issue" link led back to the workbench it was already
// showing. Two views now have two URLs, and each can reach the other.
function WorkflowRunBanner({ instance }: { instance: WorkflowInstance }) {
  const { t } = useT("workflows");
  const p = useWorkspacePaths();
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2 border-b bg-muted/30 px-4 py-2">
      <GitBranch aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      <span className="text-sm text-muted-foreground">
        {t(($) => $.workbench.driven_by_run)}
      </span>
      <span className="min-w-0 truncate text-sm font-medium">
        {instance.workflow_name}
      </span>
      <WorkflowStatusBadge status={instance.status} />
      <AppLink
        href={p.workflowRun(instance.id)}
        className="ml-auto inline-flex min-h-8 shrink-0 items-center gap-1 rounded-md px-2 text-xs font-medium outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
      >
        {t(($) => $.workbench.open_workbench)}
        <ArrowUpRight aria-hidden="true" className="size-3.5" />
      </AppLink>
    </div>
  );
}

export function WorkflowAwareIssueDetail({
  issueId,
  onDelete,
}: {
  issueId: string;
  onDelete?: () => void;
}) {
  const wsId = useWorkspaceId();
  const enabled = useWorkspaceFeatureEnabled(
    wsId,
    WORKFLOWS_ACTIVITY_ENGINE_FLAG,
  );
  // A 404 is the expected result for ordinary issues and for an older server
  // that does not expose Workflow APIs; both leave the issue exactly as it was.
  const workflowQuery = useQuery({
    ...issueWorkflowOptions(wsId, issueId),
    enabled,
  });
  const instance = workflowQuery.data?.instance;

  // The issue renders immediately rather than behind a skeleton: it does not
  // depend on the workflow lookup, and waiting made every ordinary issue pay
  // for a request that answers 404.
  const detail = (
    <ArtifactAdoptProvider issueId={issueId}>
      <IssueDetail
        issueId={issueId}
        onDelete={onDelete}
        headerActions={enabled && !instance?.id
          ? <WorkflowStartDialog issueId={issueId} />
          : undefined}
      />
    </ArtifactAdoptProvider>
  );

  // A host issue and a node child issue are two different relationships to a
  // run, and an issue is only ever one of them: the host owns the run, a child
  // is one activity inside it.
  if (!instance?.id) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        <WorkflowNodeContextPanel issueId={issueId} />
        {detail}
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <WorkflowRunBanner instance={instance} />
      {detail}
    </div>
  );
}
