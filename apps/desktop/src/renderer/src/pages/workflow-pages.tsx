import { useEffect, type ReactNode } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { useWorkspaceFeatureState } from "@multica/core/config";
import { WORKFLOWS_ACTIVITY_ENGINE_FLAG } from "@multica/core/feature-flags";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useNavigation } from "@multica/views/navigation";
import {
  WorkflowsPage,
  WorkflowPage,
  WorkflowRunsPage,
  WorkflowWorkbench,
} from "@multica/views/workflows";
import { useDocumentTitle } from "@/hooks/use-document-title";

function WorkflowRouteGate({ children }: { children: ReactNode }) {
  const wsId = useWorkspaceId();
  const featureState = useWorkspaceFeatureState(
    wsId,
    WORKFLOWS_ACTIVITY_ENGINE_FLAG,
  );
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  // The redirect goes through the navigation adapter, not react-router's
  // <Navigate>: direct navigation breaks the Coordinator protocol (MUL-4741).
  const blocked = featureState !== "loading" && featureState !== "enabled";
  useEffect(() => {
    if (blocked) navigation.replace(paths.issues());
  }, [blocked, navigation, paths]);
  return featureState === "enabled" ? children : null;
}

export function WorkflowsRoute() {
  useDocumentTitle("Workflows");
  return (
    <WorkflowRouteGate>
      <WorkflowsPage />
    </WorkflowRouteGate>
  );
}

export function WorkflowRunsRoute() {
  const [searchParams] = useSearchParams();
  useDocumentTitle("Workflow runs");
  return (
    <WorkflowRouteGate>
      <WorkflowRunsPage
        workflowId={searchParams.get("workflow") ?? undefined}
      />
    </WorkflowRouteGate>
  );
}

export function WorkflowDetailPage() {
  const { id } = useParams<{ id: string }>();
  useDocumentTitle("Workflow");
  if (!id) return null;
  return (
    <WorkflowRouteGate>
      <WorkflowWorkbench instanceId={id} />
    </WorkflowRouteGate>
  );
}

export function WorkflowRoute() {
  const { id } = useParams<{ id: string }>();
  useDocumentTitle("Workflow template");
  if (!id) return null;
  return (
    <WorkflowRouteGate>
      <WorkflowPage templateId={id} />
    </WorkflowRouteGate>
  );
}
