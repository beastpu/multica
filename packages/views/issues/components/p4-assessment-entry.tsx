"use client";

import { useEffect, useState } from "react";
import { ArrowUpRight, Bot, GitBranch, ShieldCheck } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  operationsFixesOptions,
  useUpdateAgentFixReview,
} from "@multica/core/dashboard";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import type {
  AgentFixRecord,
  Issue,
  UpdateAgentFixReviewRequest,
} from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";

const ISSUE_ASSESSMENT_LOOKBACK_DAYS = 90;
const REVIEW_OUTCOMES = [
  "unreviewed",
  "accepted",
  "needs_changes",
  "rejected",
  "not_applicable",
] as const;

const REVIEW_REASONS: Record<string, string[]> = {
  accepted: ["complete_usable", "small_fix", "human_assisted"],
  needs_changes: [
    "coverage_incomplete",
    "edge_case_missing",
    "test_insufficient",
    "wrong_location",
    "integration_incomplete",
    "quality_insufficient",
    "compatibility",
  ],
  rejected: [
    "wrong_direction",
    "root_cause_missing",
    "regression",
    "risk_high",
    "unusable_output",
    "unverifiable",
    "architecture_violation",
  ],
  not_applicable: [
    "not_fix_task",
    "duplicate",
    "environment_data",
    "cancelled_requirement",
    "no_change_needed",
    "misfire",
  ],
  unreviewed: [],
};

export function issueHasP4AssessmentSignal(issue: Issue): boolean {
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
  if (!issueHasP4AssessmentSignal(issue) && !record) return null;

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
            ? enumLabel(tUsage as unknown as UsageT, "review", outcome)
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
  if (!issueHasP4AssessmentSignal(issue) && !record) return null;

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

type UsageT = (selector: (resource: any) => string) => string;

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

function enumLabel(t: UsageT, kind: "review", value: string): string {
  const key = value.trim();
  if (!key) return "";
  const known: Record<typeof kind, Record<string, (resource: any) => string>> = {
    review: {
      unreviewed: ($) => $.operations.enums.review.unreviewed,
      accepted: ($) => $.operations.enums.review.accepted,
      needs_changes: ($) => $.operations.enums.review.needs_changes,
      rejected: ($) => $.operations.enums.review.rejected,
      not_applicable: ($) => $.operations.enums.review.not_applicable,
    },
  };
  return known[kind][key] ? t(known[kind][key]) : key;
}

function reviewReasonLabel(t: UsageT, value: string): string {
  const key = value.trim();
  if (!key) return "";
  const known: Record<string, (resource: any) => string> = {
    complete_usable: ($) => $.operations.enums.review_reason.complete_usable,
    small_fix: ($) => $.operations.enums.review_reason.small_fix,
    human_assisted: ($) => $.operations.enums.review_reason.human_assisted,
    coverage_incomplete: ($) =>
      $.operations.enums.review_reason.coverage_incomplete,
    edge_case_missing: ($) =>
      $.operations.enums.review_reason.edge_case_missing,
    test_insufficient: ($) =>
      $.operations.enums.review_reason.test_insufficient,
    wrong_location: ($) => $.operations.enums.review_reason.wrong_location,
    integration_incomplete: ($) =>
      $.operations.enums.review_reason.integration_incomplete,
    quality_insufficient: ($) =>
      $.operations.enums.review_reason.quality_insufficient,
    compatibility: ($) => $.operations.enums.review_reason.compatibility,
    wrong_direction: ($) => $.operations.enums.review_reason.wrong_direction,
    root_cause_missing: ($) =>
      $.operations.enums.review_reason.root_cause_missing,
    regression: ($) => $.operations.enums.review_reason.regression,
    risk_high: ($) => $.operations.enums.review_reason.risk_high,
    unusable_output: ($) => $.operations.enums.review_reason.unusable_output,
    unverifiable: ($) => $.operations.enums.review_reason.unverifiable,
    architecture_violation: ($) =>
      $.operations.enums.review_reason.architecture_violation,
    not_fix_task: ($) => $.operations.enums.review_reason.not_fix_task,
    duplicate: ($) => $.operations.enums.review_reason.duplicate,
    environment_data: ($) => $.operations.enums.review_reason.environment_data,
    cancelled_requirement: ($) =>
      $.operations.enums.review_reason.cancelled_requirement,
    no_change_needed: ($) => $.operations.enums.review_reason.no_change_needed,
    misfire: ($) => $.operations.enums.review_reason.misfire,
  };
  return known[key] ? t(known[key]) : key;
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
  const tx = t as unknown as UsageT;
  const updateReview = useUpdateAgentFixReview();
  const [outcome, setOutcome] = useState("unreviewed");
  const [reasons, setReasons] = useState<string[]>([]);
  const [note, setNote] = useState("");

  useEffect(() => {
    if (!open) return;
    setOutcome(record?.human_review?.outcome || "unreviewed");
    setReasons(record?.human_review?.reasons ?? []);
    setNote(record?.human_review?.note ?? "");
  }, [open, record]);

  const reasonOptions = REVIEW_REASONS[outcome] ?? [];
  const fixIssueId = record?.issue_id || issue.id;

  const toggleReason = (reason: string) => {
    setReasons((prev) =>
      prev.includes(reason)
        ? prev.filter((item) => item !== reason)
        : [...prev, reason],
    );
  };

  const save = (data: UpdateAgentFixReviewRequest) => {
    updateReview.mutate(
      { issueId: fixIssueId, data },
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
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-w-2xl"
        onClick={(event) => event.stopPropagation()}
      >
        <DialogHeader>
          <DialogTitle>{t(($) => $.operations.review_modal.title)}</DialogTitle>
          <DialogDescription>
            {issue.identifier} · {issue.title}
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <div className="grid gap-2">
            <div className="text-xs font-medium text-muted-foreground">
              {t(($) => $.operations.review_modal.outcome)}
            </div>
            <div className="flex flex-wrap gap-2">
              {REVIEW_OUTCOMES.map((value) => (
                <button
                  key={value}
                  type="button"
                  onClick={() => {
                    setOutcome(value);
                    setReasons([]);
                  }}
                  className={cn(
                    "rounded-lg border px-3 py-1.5 text-sm font-medium transition-colors",
                    outcome === value
                      ? "border-primary bg-primary text-primary-foreground"
                      : "border-border bg-background hover:bg-muted",
                  )}
                >
                  {enumLabel(tx, "review", value)}
                </button>
              ))}
            </div>
          </div>

          <div className="grid gap-2">
            <div className="text-xs font-medium text-muted-foreground">
              {t(($) => $.operations.review_modal.reasons)}
            </div>
            {reasonOptions.length ? (
              <div className="flex flex-wrap gap-2">
                {reasonOptions.map((reason) => (
                  <button
                    key={reason}
                    type="button"
                    onClick={() => toggleReason(reason)}
                    className={cn(
                      "rounded-lg border px-3 py-1.5 text-sm transition-colors",
                      reasons.includes(reason)
                        ? "border-primary bg-primary/10 text-primary"
                        : "border-border bg-background hover:bg-muted",
                    )}
                  >
                    {reviewReasonLabel(tx, reason)}
                  </button>
                ))}
              </div>
            ) : (
              <div className="text-sm text-muted-foreground">
                {t(($) => $.operations.review_modal.no_reasons)}
              </div>
            )}
            <div className="text-xs text-muted-foreground">
              {t(($) => $.operations.review_modal.reason_hint)}
            </div>
          </div>

          <div className="grid gap-2">
            <div className="text-xs font-medium text-muted-foreground">
              {t(($) => $.operations.review_modal.note)}
            </div>
            <Textarea
              value={note}
              onChange={(event) => setNote(event.target.value)}
              placeholder={t(($) => $.operations.review_modal.note_placeholder)}
              className="min-h-24"
            />
          </div>
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={updateReview.isPending}
          >
            {t(($) => $.operations.review_modal.cancel)}
          </Button>
          <Button
            type="button"
            disabled={updateReview.isPending}
            onClick={() => save({ outcome, reasons, note })}
          >
            {updateReview.isPending
              ? t(($) => $.operations.review_modal.saving)
              : t(($) => $.operations.review_modal.save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
