"use client";

import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import { useWorkspaceFeatureEnabled } from "@multica/core/config";
import { WORKFLOWS_ACTIVITY_ENGINE_FLAG } from "@multica/core/feature-flags";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueWorkflowOptions } from "@multica/core/workflows";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { IssueDetail } from "../issues/components";
import { WorkflowStartDialog } from "./workflow-start-dialog";
import { WorkflowWorkbench } from "./workflow-workbench";

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
  const workflowQuery = useQuery({
    ...issueWorkflowOptions(wsId, issueId),
    enabled,
  });

  if (enabled && workflowQuery.isPending) {
    return (
      <div className="space-y-4 p-5">
        <Skeleton className="h-12 rounded-xl" />
        <Skeleton className="h-36 rounded-xl" />
        <Skeleton className="h-96 rounded-xl" />
      </div>
    );
  }
  if (workflowQuery.data?.instance.id) {
    return <WorkflowWorkbench instanceId={workflowQuery.data.instance.id} />;
  }

  // A 404 is the expected result for ordinary Issues and for an older server
  // that does not expose Workflow APIs. Either way, preserve the established
  // Issue surface instead of turning compatibility into a blank page.
  const isExpectedMiss = workflowQuery.error instanceof ApiError &&
    workflowQuery.error.status === 404;
  if (!enabled || isExpectedMiss || workflowQuery.isError) {
    return (
      <IssueDetail
        issueId={issueId}
        onDelete={onDelete}
        headerActions={enabled ? <WorkflowStartDialog issueId={issueId} /> : undefined}
      />
    );
  }
  return (
    <IssueDetail
      issueId={issueId}
      onDelete={onDelete}
      headerActions={<WorkflowStartDialog issueId={issueId} />}
    />
  );
}
