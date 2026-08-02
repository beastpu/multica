"use client";

import { use } from "react";
import { notFound } from "next/navigation";
import { useWorkspaceFeatureState } from "@multica/core/config";
import { WORKFLOWS_ACTIVITY_ENGINE_FLAG } from "@multica/core/feature-flags";
import { useWorkspaceId } from "@multica/core/hooks";
import { WorkflowWorkbench } from "@multica/views/workflows";

export default function Page({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const wsId = useWorkspaceId();
  const featureState = useWorkspaceFeatureState(
    wsId,
    WORKFLOWS_ACTIVITY_ENGINE_FLAG,
  );
  if (featureState === "loading") return null;
  if (featureState === "disabled") notFound();
  return <WorkflowWorkbench instanceId={id} />;
}
