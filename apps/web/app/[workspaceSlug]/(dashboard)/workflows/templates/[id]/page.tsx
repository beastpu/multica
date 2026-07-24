"use client";

import { use } from "react";
import { notFound } from "next/navigation";
import { useWorkspaceFeatureEnabled } from "@multica/core/config";
import { WORKFLOWS_ACTIVITY_ENGINE_FLAG } from "@multica/core/feature-flags";
import { useWorkspaceId } from "@multica/core/hooks";
import { WorkflowTemplatePage } from "@multica/views/workflows";

export default function Page({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const wsId = useWorkspaceId();
  const enabled = useWorkspaceFeatureEnabled(
    wsId,
    WORKFLOWS_ACTIVITY_ENGINE_FLAG,
  );
  if (!enabled) notFound();
  return <WorkflowTemplatePage templateId={id} />;
}
