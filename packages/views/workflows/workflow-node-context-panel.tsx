"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight, GitBranch } from "lucide-react";
import { useWorkspaceFeatureEnabled } from "@multica/core/config";
import { WORKFLOWS_ACTIVITY_ENGINE_FLAG } from "@multica/core/feature-flags";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  issueWorkflowNodeOptions,
  type WorkflowNodeContext,
  type WorkflowNodeContextUpstream,
} from "@multica/core/workflows";

import { useT } from "../i18n";
import { AppLink } from "../navigation";

// WorkflowNodeContextPanel is what a node child issue can say about the run it
// belongs to.
//
// The issue itself carries its instructions and a quoted requirement, and
// nothing else — what upstream concluded, what this activity owes and how far
// the run has got all change while the run is live, so writing them into the
// description would leave a copy that stops being true after the first rework.
// They are read here instead, at the one place a person actually opens.
//
// Read-only by design: delivery, review and completion live in the workbench,
// which this panel links to. Two places to submit the same thing is how they
// disagree.
export function WorkflowNodeContextPanel({ issueId }: { issueId: string }) {
  const wsId = useWorkspaceId();
  const enabled = useWorkspaceFeatureEnabled(
    wsId,
    WORKFLOWS_ACTIVITY_ENGINE_FLAG,
  );
  // A 404 is the ordinary answer for every issue that is not a workflow node,
  // so an error simply renders nothing. The spread would otherwise overwrite
  // the options' own issueId guard, so it is restated here.
  const query = useQuery({
    ...issueWorkflowNodeOptions(wsId, issueId),
    enabled: enabled && Boolean(issueId),
  });
  const context = query.data;
  if (!context?.node_key) return null;
  return <NodeContext context={context} />;
}

function NodeContext({ context }: { context: WorkflowNodeContext }) {
  const { t } = useT("workflows");
  const p = useWorkspacePaths();
  const owed = context.artifacts ?? [];
  const outputs = context.outputs ?? [];
  const upstream = context.upstream ?? [];
  return (
    <section className="shrink-0 border-b bg-muted/20 px-4 py-3 text-sm">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <GitBranch aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 truncate font-medium">
          {context.node_name || context.node_key}
        </span>
        {context.run_title && (
          <span className="min-w-0 truncate text-xs text-muted-foreground">
            {context.run_title}
          </span>
        )}
        {context.instance_id && (
          <AppLink
            href={p.workflowRun(context.instance_id)}
            className="ml-auto inline-flex min-h-8 shrink-0 items-center gap-1 rounded-md px-2 text-xs font-medium outline-none hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
          >
            {t(($) => $.node_context.open_run)}
            <ArrowUpRight aria-hidden="true" className="size-3.5" />
          </AppLink>
        )}
      </div>

      <div className="mt-3 grid gap-3 md:grid-cols-2">
        <div className="space-y-1.5">
          <p className="text-xs font-medium text-muted-foreground">
            {t(($) => $.node_context.upstream)}
          </p>
          {upstream.length === 0
            ? (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.node_context.no_upstream)}
              </p>
            )
            : upstream.map((entry) => (
              <UpstreamEntry key={entry.node_key} entry={entry} />
            ))}
        </div>

        <div className="space-y-1.5">
          <p className="text-xs font-medium text-muted-foreground">
            {t(($) => $.node_context.owes)}
          </p>
          {owed.length === 0 && outputs.length === 0 && (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.node_context.owes_conclusion_only)}
            </p>
          )}
          {owed.map((duty) => (
            <p key={duty.key} className="text-xs">
              <span className="font-medium">{duty.name || duty.key}</span>
              <span className="text-muted-foreground">
                {" — "}
                {duty.required
                  ? t(($) => $.node_context.required)
                  : t(($) => $.node_context.optional)}
                {" · "}
                {duty.delivered
                  ? t(($) => $.node_context.delivered)
                  : t(($) => $.node_context.not_delivered)}
              </span>
            </p>
          ))}
          {outputs.map((field) => (
            <p key={field.key} className="text-xs">
              <span className="font-medium">{field.desc || field.key}</span>
              <span className="text-muted-foreground">
                {" — "}
                {t(($) => $.node_context.output_field)}
              </span>
            </p>
          ))}
        </div>
      </div>
    </section>
  );
}

// A predecessor's conclusion, or — when nobody wrote one — the raw output the
// platform extracted, labelled as exactly that. Merging the two would tell a
// reader the platform summarised something a person never did.
function UpstreamEntry({ entry }: { entry: WorkflowNodeContextUpstream }) {
  const { t } = useT("workflows");
  const conclusion = entry.summary?.trim();
  const extract = entry.worker_output?.trim();
  return (
    <div className="space-y-0.5">
      <p className="text-xs font-medium">{entry.name || entry.node_key}</p>
      {conclusion
        ? <p className="line-clamp-4 whitespace-pre-wrap text-xs">{conclusion}</p>
        : extract
        ? (
          <>
            <p className="text-[11px] text-muted-foreground">
              {t(($) => $.node_context.extracted_output)}
            </p>
            <p className="line-clamp-4 whitespace-pre-wrap text-xs text-muted-foreground">
              {extract}
            </p>
          </>
        )
        : (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.node_context.no_conclusion)}
          </p>
        )}
      {(entry.issues?.length ?? 0) > 0 && (
        <p className="text-[11px] text-muted-foreground">
          {entry.issues.join(" · ")}
        </p>
      )}
    </div>
  );
}
