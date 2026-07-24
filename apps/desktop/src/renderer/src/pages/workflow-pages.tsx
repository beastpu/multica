import type { ReactNode } from "react";
import { Navigate, useParams } from "react-router-dom";
import { useWorkspaceFeatureEnabled } from "@multica/core/config";
import { WORKFLOWS_ACTIVITY_ENGINE_FLAG } from "@multica/core/feature-flags";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  WorkflowsPage,
  WorkflowTemplatePage,
  WorkflowWorkbench,
} from "@multica/views/workflows";
import { useDocumentTitle } from "@/hooks/use-document-title";

function WorkflowRouteGate({ children }: { children: ReactNode }) {
  const wsId = useWorkspaceId();
  const enabled = useWorkspaceFeatureEnabled(
    wsId,
    WORKFLOWS_ACTIVITY_ENGINE_FLAG,
  );
  const paths = useWorkspacePaths();
  return enabled ? children : <Navigate to={paths.issues()} replace />;
}

export function WorkflowsRoute() {
  useDocumentTitle("Workflows");
  return (
    <WorkflowRouteGate>
      <WorkflowsPage />
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

export function WorkflowTemplateRoute() {
  const { id } = useParams<{ id: string }>();
  useDocumentTitle("Workflow template");
  if (!id) return null;
  return (
    <WorkflowRouteGate>
      <WorkflowTemplatePage templateId={id} />
    </WorkflowRouteGate>
  );
}
