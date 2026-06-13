"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Archive,
  CheckCircle2,
  CircleDashed,
  GitMerge,
  GitPullRequestArrow,
  TriangleAlert,
  XCircle,
} from "lucide-react";
import { issuePerforceReviewsOptions } from "@multica/core/perforce";
import type { PerforceReview } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

type IssuesT = ReturnType<typeof useT<"issues">>["t"];

const REVIEWS_LIMIT_BEFORE_COLLAPSE = 4;

const STATE_ICON: Record<
  string,
  { icon: React.ComponentType<{ className?: string }>; className: string }
> = {
  needsReview: { icon: GitPullRequestArrow, className: "text-sky-600 dark:text-sky-400" },
  needsRevision: { icon: TriangleAlert, className: "text-amber-600 dark:text-amber-400" },
  approved: { icon: CheckCircle2, className: "text-emerald-600 dark:text-emerald-400" },
  rejected: { icon: XCircle, className: "text-rose-600 dark:text-rose-400" },
  archived: { icon: Archive, className: "text-muted-foreground" },
};

const UNKNOWN_ICON = { icon: CircleDashed, className: "text-muted-foreground" };

function stateLabel(t: IssuesT, state: string): string {
  switch (state) {
    case "needsReview":
      return t(($) => $.detail.review_state_needs_review);
    case "needsRevision":
      return t(($) => $.detail.review_state_needs_revision);
    case "approved":
      return t(($) => $.detail.review_state_approved);
    case "rejected":
      return t(($) => $.detail.review_state_rejected);
    case "archived":
      return t(($) => $.detail.review_state_archived);
    default:
      return t(($) => $.detail.review_state_unknown);
  }
}

export function PerforceReviewList({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const [expanded, setExpanded] = useState(false);
  const { data, isLoading } = useQuery(issuePerforceReviewsOptions(issueId));
  const reviews = data?.reviews ?? [];

  if (isLoading) {
    return <p className="text-xs text-muted-foreground px-2">{t(($) => $.detail.reviews_loading)}</p>;
  }
  if (reviews.length === 0) {
    return <p className="text-xs text-muted-foreground px-2">{t(($) => $.detail.reviews_empty)}</p>;
  }

  const collapsed = reviews.length >= REVIEWS_LIMIT_BEFORE_COLLAPSE && !expanded;
  const visible = collapsed ? reviews.slice(0, REVIEWS_LIMIT_BEFORE_COLLAPSE - 1) : reviews;

  return (
    <div className="space-y-1">
      {visible.map((review) => (
        <ReviewRow key={review.review_id} review={review} t={t} />
      ))}
      {reviews.length >= REVIEWS_LIMIT_BEFORE_COLLAPSE && (
        <button
          type="button"
          className="px-2 text-xs text-muted-foreground hover:text-foreground"
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded
            ? t(($) => $.detail.reviews_show_less)
            : t(($) => $.detail.reviews_show_more, { count: reviews.length - (REVIEWS_LIMIT_BEFORE_COLLAPSE - 1) })}
        </button>
      )}
    </div>
  );
}

function ReviewRow({ review, t }: { review: PerforceReview; t: IssuesT }) {
  const committed = typeof review.committed_cl === "number";
  const visual = committed
    ? { icon: GitMerge, className: "text-violet-600 dark:text-violet-400" }
    : STATE_ICON[review.state] ?? UNKNOWN_ICON;
  const Icon = visual.icon;

  return (
    <a
      href={review.html_url}
      target="_blank"
      rel="noopener noreferrer"
      className="flex items-start gap-2 rounded-md px-2 py-1.5 hover:bg-accent/70"
    >
      <Icon className={cn("mt-0.5 h-4 w-4 shrink-0", visual.className)} />
      <div className="min-w-0 flex-1">
        <p className="truncate text-xs font-medium">{review.title || `#${review.review_id}`}</p>
        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground">
          <span>#{review.review_id}</span>
          <span>·</span>
          <span>{committed ? t(($) => $.detail.review_state_committed) : stateLabel(t, review.state)}</span>
          {review.author && <span>· {review.author}</span>}
          {committed ? (
            <span>· {t(($) => $.detail.review_committed_cl, { cl: review.committed_cl! })}</span>
          ) : (
            typeof review.shelved_cl === "number" && (
              <span>· {t(($) => $.detail.review_shelved_cl, { cl: review.shelved_cl })}</span>
            )
          )}
        </div>
      </div>
    </a>
  );
}
