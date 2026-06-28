"use client";

import { useState } from "react";
import { ArrowUpRight, Bot, GitBranch, ShieldCheck } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  operationsFixesOptions,
  useUpdateAgentFixReview,
} from "@multica/core/dashboard";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import type { AgentFixRecord, Issue } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import {
  AgentFixReviewDialog,
  agentFixEnumLabel,
  type UsageT,
} from "../../dashboard/components/agent-fix-review";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";

const ISSUE_ASSESSMENT_LOOKBACK_DAYS = 90;

export function issueHasLegacyP4AssessmentSignal(issue: Issue): boolean {
  const title = issue.title.toLowerCase();
  if (title.includes("p4 assessment") || title.includes("swarm")) return true;

  const metadata = issue.metadata ?? {};
  return (
    metadata.demo === true ||
    metadata.p4_assessment === true ||
    typeof metadata.swarm_review === "string" ||
    typeof metadata.p4_status === "string"
  );
}

export function IssueP4AssessmentTags({
  issue,
  className,
}: {
  issue: Issue;
  className?: string;
}) {
  const { t } = useT("issues");
  const { t: tUsage } = useT("usage");
  const record = useIssueP4AssessmentRecord(issue);
  const outcome = record?.human_review?.outcome?.trim();
  const [reviewOpen, setReviewOpen] = useState(false);
  if (!issueHasLegacyP4AssessmentSignal(issue) && !record) return null;

  return (
    <span className={cn("inline-flex min-w-0 shrink-0 items-center gap-1", className)}>
      <Badge variant="outline" className="h-5 border-sky-200 bg-sky-50 text-sky-700">
        <GitBranch className="size-3" />
        {t(($) => $.p4_assessment.p4)}
      </Badge>
      <Badge variant="outline" className="hidden h-5 border-violet-200 bg-violet-50 text-violet-700 sm:inline-flex">
        <Bot className="size-3" />
        {t(($) => $.p4_assessment.ai)}
      </Badge>
      <button
        type="button"
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          setReviewOpen(true);
        }}
        onMouseDown={(event) => event.stopPropagation()}
        onPointerDown={(event) => event.stopPropagation()}
        className="inline-flex min-w-0 rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <Badge
          variant="outline"
          className="h-5 border-emerald-200 bg-emerald-50 text-emerald-700"
        >
          <ShieldCheck className="size-3" />
          {outcome
            ? agentFixEnumLabel(tUsage as unknown as UsageT, "review", outcome)
            : t(($) => $.p4_assessment.review)}
        </Badge>
      </button>
      <IssueP4ReviewDialog
        issue={issue}
        record={record}
        open={reviewOpen}
        onOpenChange={setReviewOpen}
      />
    </span>
  );
}

export function IssueP4AssessmentButton({
  issue,
  className,
}: {
  issue: Issue;
  className?: string;
}) {
  const { t } = useT("issues");
  const p = useWorkspacePaths();
  const record = useIssueP4AssessmentRecord(issue);
  if (!issueHasLegacyP4AssessmentSignal(issue) && !record) return null;

  return (
    <AppLink
      href={p.operations()}
      className={cn(
        "inline-flex h-7 shrink-0 items-center justify-center gap-1.5 rounded-lg border border-border bg-background px-2 text-xs font-medium text-foreground transition-colors hover:bg-muted",
        className,
      )}
      onClick={(event) => event.stopPropagation()}
      onMouseDown={(event) => event.stopPropagation()}
      onPointerDown={(event) => event.stopPropagation()}
    >
      {t(($) => $.p4_assessment.open)}
      <ArrowUpRight className="size-3" />
    </AppLink>
  );
}

function useIssueP4AssessmentRecord(issue: Issue): AgentFixRecord | undefined {
  const wsId = useWorkspaceId();
  const { data: records = [] } = useQuery(
    operationsFixesOptions(wsId, ISSUE_ASSESSMENT_LOOKBACK_DAYS, ""),
  );
  return records.find((record) => isSameIssueAssessmentRecord(record, issue));
}

function isSameIssueAssessmentRecord(record: AgentFixRecord, issue: Issue): boolean {
  return (
    (record.issue_id === issue.id && record.issue_id !== "") ||
    (record.issue_identifier === issue.identifier && record.issue_identifier !== "")
  );
}

function IssueP4ReviewDialog({
  issue,
  record,
  open,
  onOpenChange,
}: {
  issue: Issue;
  record?: AgentFixRecord;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("usage");
  const updateReview = useUpdateAgentFixReview();
  const fixIssueId = record?.issue_id || issue.id;
  const fixBindingId = record?.external?.binding_id;

  return (
    <AgentFixReviewDialog
      open={open}
      description={`${issue.identifier} · ${issue.title}`}
      initialReview={record?.human_review}
      saving={updateReview.isPending}
      maxWidthClassName="max-w-2xl"
      stopPropagation
      onOpenChange={onOpenChange}
      onSave={(data) => {
        updateReview.mutate(
          { issueId: fixIssueId, bindingId: fixBindingId, data },
          {
            onSuccess: () => onOpenChange(false),
            onError: (err) => {
              toast.error(
                err instanceof Error && err.message
                  ? err.message
                  : t(($) => $.operations.review_modal.save_failed),
              );
            },
          },
        );
      }}
    />
  );
}
