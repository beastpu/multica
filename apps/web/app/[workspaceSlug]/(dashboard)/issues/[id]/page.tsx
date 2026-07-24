"use client";

import { use } from "react";
import { WorkflowAwareIssueDetail } from "@multica/views/workflows";
import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";

export default function IssueDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return (
    <ErrorBoundary resetKeys={[id]}>
      <WorkflowAwareIssueDetail issueId={id} />
    </ErrorBoundary>
  );
}
