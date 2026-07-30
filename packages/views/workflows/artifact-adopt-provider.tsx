"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueDetailOptions } from "@multica/core/issues/queries";
import {
  useSubmitWorkflowArtifact,
  workflowNodeArtifactsOptions,
  workflowNodeOptions,
} from "@multica/core/workflows";
import { Package } from "lucide-react";
import { useCallback, useMemo, useState, type ReactNode } from "react";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";

import { useT } from "../i18n";
import { AttachmentActionsProvider } from "../editor";

// ArtifactAdoptProvider offers "record this file as the node's artifact" on
// attachments inside a workflow node issue.
//
// This is the human counterpart to `workflow submit --artifact <key>
// --attachment-id`: the file is already in the system, so adopting it records
// that it is the deliverable rather than uploading a second copy. It exists
// because agents are not the only ones who deliver, and a person who has
// already dragged the file into the issue should not have to hand it over
// twice.
//
// Deliberately not automatic. A run produces drafts and intermediate files as
// well as deliverables; letting any *.md become an artifact would empty the
// word of meaning. Which file counts is a judgement, so a person makes it.
export function ArtifactAdoptProvider({
  issueId,
  children,
}: {
  issueId: string;
  children: ReactNode;
}) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const issueQuery = useQuery(issueDetailOptions(wsId, issueId));
  const issue = issueQuery.data;
  const context = issue?.workflow_context ?? null;
  const nodeInstanceId = context?.workflow_node_instance_id ?? "";
  const [pending, setPending] = useState<string | null>(null);

  const nodeQuery = useQuery({
    ...workflowNodeOptions(wsId, nodeInstanceId),
    enabled: Boolean(nodeInstanceId),
  });
  const artifactsQuery = useQuery({
    ...workflowNodeArtifactsOptions(wsId, nodeInstanceId),
    enabled: Boolean(nodeInstanceId),
  });
  const submit = useSubmitWorkflowArtifact(
    context?.workflow_instance_id ?? "",
    nodeInstanceId,
  );

  // Only slots the server would actually accept: the submit endpoint rejects
  // an attachment sent to a document- or link-kind requirement, so offering
  // those here would produce a button that always fails.
  const slots = useMemo(() => {
    const declared = nodeQuery.data?.node.definition.artifacts ?? [];
    return declared.filter((artifact) => artifact.kind === "attachment");
  }, [nodeQuery.data]);

  const delivered = useMemo(
    () =>
      new Set(
        (artifactsQuery.data?.artifacts ?? []).map((a) => a.artifact_key),
      ),
    [artifactsQuery.data],
  );

  const actionsFor = useCallback(
    (attachmentId: string) => {
      if (slots.length === 0) return [];
      return [{
        id: "adopt-artifact",
        label: t(($) => $.workbench.adopt_as_artifact),
        icon: <Package className="size-3.5" />,
        onSelect: () => setPending(attachmentId),
        disabled: submit.isPending,
      }];
    },
    [slots.length, submit.isPending, t],
  );

  const adopt = (artifactKey: string) => {
    if (!pending) return;
    submit.mutate(
      {
        artifactKey,
        attachmentId: pending,
        issueId: issue?.id,
      },
      { onSuccess: () => setPending(null) },
    );
  };

  if (!nodeInstanceId) return <>{children}</>;

  return (
    <AttachmentActionsProvider actionsFor={actionsFor}>
      {children}
      <Dialog open={pending !== null} onOpenChange={() => setPending(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t(($) => $.workbench.adopt_as_artifact)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.workbench.adopt_as_artifact_help)}
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-2 py-1">
            {slots.map((slot) => (
              <Button
                key={slot.key}
                type="button"
                variant="outline"
                className="h-auto justify-start py-2 text-left"
                disabled={submit.isPending}
                onClick={() => adopt(slot.key)}
              >
                <span className="flex min-w-0 flex-col">
                  <span className="truncate text-sm">{slot.name}</span>
                  <span className="truncate font-mono text-xs text-muted-foreground">
                    {slot.key}
                    {/* Replacing keeps the old one as history, but the user
                        should know they are overwriting rather than adding. */}
                    {delivered.has(slot.key)
                      ? ` · ${t(($) => $.workbench.adopt_replaces)}`
                      : ""}
                  </span>
                </span>
              </Button>
            ))}
          </div>
          {submit.isError && (
            <p className="text-sm text-destructive" role="alert">
              {t(($) => $.errors.action_failed)}
            </p>
          )}
          <DialogFooter />
        </DialogContent>
      </Dialog>
    </AttachmentActionsProvider>
  );
}
