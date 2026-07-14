"use client";

import { useEffect, useState, type ReactNode } from "react";
import type { AgentFixHumanReview, UpdateAgentFixReviewRequest } from "@multica/core/types";
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
import { useT } from "../../i18n";

export type Tone = "default" | "success" | "warning" | "danger" | "info" | "muted";
export type UsageT = (selector: (resource: any) => string) => string;

export const TONE_CLASS: Record<Tone, string> = {
  default: "border-border bg-background text-foreground",
  success: "border-primary/20 bg-primary/10 text-primary",
  warning: "border-foreground/15 bg-muted text-foreground",
  danger: "border-destructive/20 bg-destructive/10 text-destructive",
  info: "border-primary/20 bg-primary/10 text-primary",
  muted: "border-border bg-muted text-muted-foreground",
};

export const AGENT_FIX_REVIEW_OUTCOMES = [
  "unreviewed",
  "accepted",
  "needs_changes",
  "rejected",
  "not_applicable",
] as const;

export const AGENT_FIX_REVIEW_REASONS: Record<string, string[]> = {
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

export function agentFixEnumLabel(
  t: UsageT,
  group:
    | "assessment"
    | "attribution"
    | "quality"
    | "review"
    | "review_reason",
  value?: string,
): string {
  const key = value?.trim();
  if (!key) return t(($) => $.operations.no_reason);
  const labels: Record<string, string> =
    group === "assessment"
      ? {
          missing: t(($) => $.operations.enums.assessment.missing),
          pending: t(($) => $.operations.enums.assessment.pending),
          running: t(($) => $.operations.enums.assessment.running),
          failed: t(($) => $.operations.enums.assessment.failed),
          completed: t(($) => $.operations.enums.assessment.completed),
          stale: t(($) => $.operations.enums.assessment.stale),
        }
      : group === "attribution"
        ? {
            unassessed: t(($) => $.operations.enums.attribution.unassessed),
            ai_delivered: t(($) => $.operations.enums.attribution.ai_delivered),
            ai_assisted: t(($) => $.operations.enums.attribution.ai_assisted),
            human_delivered: t(
              ($) => $.operations.enums.attribution.human_delivered,
            ),
            conflict: t(($) => $.operations.enums.attribution.conflict),
            unattributed: t(($) => $.operations.enums.attribution.unattributed),
            ai_no_output: t(($) => $.operations.enums.attribution.ai_no_output),
            ai_plan_no_record: t(
              ($) => $.operations.enums.attribution.ai_plan_no_record,
            ),
            unknown: t(($) => $.operations.enums.attribution.unknown),
          }
        : group === "quality"
          ? {
              unassessed: t(($) => $.operations.enums.quality.unassessed),
              likely_correct: t(($) => $.operations.enums.quality.likely_correct),
              likely_needs_changes: t(
                ($) => $.operations.enums.quality.likely_needs_changes,
              ),
              likely_wrong: t(($) => $.operations.enums.quality.likely_wrong),
              unknown: t(($) => $.operations.enums.quality.unknown),
            }
          : group === "review"
            ? {
                unreviewed: t(($) => $.operations.enums.review.unreviewed),
                accepted: t(($) => $.operations.enums.review.accepted),
                needs_changes: t(($) => $.operations.enums.review.needs_changes),
                rejected: t(($) => $.operations.enums.review.rejected),
                not_applicable: t(($) => $.operations.enums.review.not_applicable),
              }
            : {
                  complete_usable: t(
                    ($) => $.operations.enums.review_reason.complete_usable,
                  ),
                  small_fix: t(($) => $.operations.enums.review_reason.small_fix),
                  human_assisted: t(
                    ($) => $.operations.enums.review_reason.human_assisted,
                  ),
                  coverage_incomplete: t(
                    ($) => $.operations.enums.review_reason.coverage_incomplete,
                  ),
                  edge_case_missing: t(
                    ($) => $.operations.enums.review_reason.edge_case_missing,
                  ),
                  test_insufficient: t(
                    ($) => $.operations.enums.review_reason.test_insufficient,
                  ),
                  wrong_location: t(
                    ($) => $.operations.enums.review_reason.wrong_location,
                  ),
                  integration_incomplete: t(
                    ($) => $.operations.enums.review_reason.integration_incomplete,
                  ),
                  quality_insufficient: t(
                    ($) => $.operations.enums.review_reason.quality_insufficient,
                  ),
                  compatibility: t(
                    ($) => $.operations.enums.review_reason.compatibility,
                  ),
                  wrong_direction: t(
                    ($) => $.operations.enums.review_reason.wrong_direction,
                  ),
                  root_cause_missing: t(
                    ($) => $.operations.enums.review_reason.root_cause_missing,
                  ),
                  regression: t(($) => $.operations.enums.review_reason.regression),
                  risk_high: t(($) => $.operations.enums.review_reason.risk_high),
                  unusable_output: t(
                    ($) => $.operations.enums.review_reason.unusable_output,
                  ),
                  unverifiable: t(
                    ($) => $.operations.enums.review_reason.unverifiable,
                  ),
                  architecture_violation: t(
                    ($) => $.operations.enums.review_reason.architecture_violation,
                  ),
                  not_fix_task: t(
                    ($) => $.operations.enums.review_reason.not_fix_task,
                  ),
                  duplicate: t(($) => $.operations.enums.review_reason.duplicate),
                  environment_data: t(
                    ($) => $.operations.enums.review_reason.environment_data,
                  ),
                  cancelled_requirement: t(
                    ($) => $.operations.enums.review_reason.cancelled_requirement,
                  ),
                  no_change_needed: t(
                    ($) => $.operations.enums.review_reason.no_change_needed,
                  ),
                  misfire: t(($) => $.operations.enums.review_reason.misfire),
                };
  return labels[key] ?? key;
}

export function agentFixEnumTone(
  group: "attribution" | "quality" | "review",
  value?: string,
): Tone {
  const key = value?.trim();
  if (!key || key === "unknown" || key === "unreviewed" || key === "unassessed") {
    return "muted";
  }
  if (
    key === "accepted" ||
    key === "ai_delivered" ||
    key === "ai_assisted" ||
    key === "likely_correct"
  ) {
    return "success";
  }
  if (key === "conflict" || key === "likely_wrong" || key === "rejected") {
    return "danger";
  }
  if (
    key === "likely_needs_changes" ||
    key === "needs_changes" ||
    key === "ai_no_output" ||
    key === "ai_plan_no_record" ||
    key === "pending"
  ) {
    return "warning";
  }
  if (group === "attribution" && key === "human_delivered") return "info";
  return "default";
}

export function agentFixReviewReasonLabels(
  t: UsageT,
  reasons: string[] | undefined,
): string[] {
  return (reasons ?? [])
    .map((reason) => agentFixEnumLabel(t, "review_reason", reason))
    .filter(Boolean);
}

export function ToneBadge({
  tone,
  children,
  className,
}: {
  tone: Tone;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Badge
      variant="outline"
      className={cn("max-w-full justify-start truncate", TONE_CLASS[tone], className)}
    >
      <span className="truncate">{children}</span>
    </Badge>
  );
}

export function AgentFixReviewDialog({
  open,
  title,
  description,
  initialReview,
  saving,
  canSave = true,
  evidenceSlot,
  maxWidthClassName = "max-w-3xl",
  stopPropagation = false,
  onOpenChange,
  onSave,
}: {
  open: boolean;
  title?: string;
  description: string;
  initialReview?: AgentFixHumanReview;
  saving: boolean;
  canSave?: boolean;
  evidenceSlot?: ReactNode;
  maxWidthClassName?: string;
  stopPropagation?: boolean;
  onOpenChange: (open: boolean) => void;
  onSave: (data: UpdateAgentFixReviewRequest) => void;
}) {
  const { t } = useT("usage");
  const tx = t as unknown as UsageT;
  const [outcome, setOutcome] = useState("unreviewed");
  const [reasons, setReasons] = useState<string[]>([]);
  const [note, setNote] = useState("");

  useEffect(() => {
    if (!open) return;
    setOutcome(initialReview?.outcome || "unreviewed");
    setReasons(initialReview?.reasons ?? []);
    setNote(initialReview?.note ?? "");
  }, [open, initialReview]);

  const reasonOptions = AGENT_FIX_REVIEW_REASONS[outcome] ?? [];

  const toggleReason = (reason: string) => {
    setReasons((prev) =>
      prev.includes(reason)
        ? prev.filter((item) => item !== reason)
        : [...prev, reason],
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className={maxWidthClassName}
        onClick={stopPropagation ? (event) => event.stopPropagation() : undefined}
      >
        <DialogHeader>
          <DialogTitle>
            {title ?? t(($) => $.operations.review_modal.title)}
          </DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          {evidenceSlot}

          <div className="grid gap-2">
            <div className="text-xs font-medium text-muted-foreground">
              {t(($) => $.operations.review_modal.outcome)}
            </div>
            <div className="flex flex-wrap gap-2">
              {AGENT_FIX_REVIEW_OUTCOMES.map((value) => (
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
                  {agentFixEnumLabel(tx, "review", value)}
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
                    {agentFixEnumLabel(tx, "review_reason", reason)}
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
            disabled={saving}
          >
            {t(($) => $.operations.review_modal.cancel)}
          </Button>
          <Button
            type="button"
            disabled={!canSave || saving}
            onClick={() => onSave({ outcome, reasons, note })}
          >
            {saving
              ? t(($) => $.operations.review_modal.saving)
              : t(($) => $.operations.review_modal.save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
