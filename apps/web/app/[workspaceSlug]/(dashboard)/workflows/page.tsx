"use client";

import { notFound } from "next/navigation";
import { useWorkspaceFeatureEnabled } from "@multica/core/config";
import { WORKFLOWS_ACTIVITY_ENGINE_FLAG } from "@multica/core/feature-flags";
import { useWorkspaceId } from "@multica/core/hooks";
import { WorkflowsPage } from "@multica/views/workflows";

export default function Page() {
  const wsId = useWorkspaceId();
  const enabled = useWorkspaceFeatureEnabled(
    wsId,
    WORKFLOWS_ACTIVITY_ENGINE_FLAG,
  );
  if (!enabled) notFound();
  return <WorkflowsPage />;
}
