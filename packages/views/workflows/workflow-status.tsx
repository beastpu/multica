"use client";

import {
  AlertTriangle,
  Archive,
  CheckCircle2,
  Circle,
  CircleDashed,
  Clock3,
  PauseCircle,
  PlayCircle,
  SkipForward,
  XCircle,
} from "lucide-react";
import { Badge } from "@multica/ui/components/ui/badge";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";

const statusStyle: Record<string, string> = {
  needs_setup: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  running: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  active: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  waiting: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  in_review: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  paused: "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300",
  completed: "border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300",
  blocked: "border-destructive/30 bg-destructive/10 text-destructive",
  failed: "border-destructive/30 bg-destructive/10 text-destructive",
  cancelled: "border-muted-foreground/20 bg-muted text-muted-foreground",
  skipped: "border-muted-foreground/20 bg-muted text-muted-foreground",
  superseded: "border-muted-foreground/20 bg-muted text-muted-foreground",
  pending: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  pending_materialization: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  materializing: "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300",
  materialized: "border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300",
  detached: "border-muted-foreground/20 bg-muted text-muted-foreground",
  approved: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  pass: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  rejected: "border-destructive/30 bg-destructive/10 text-destructive",
  fail: "border-destructive/30 bg-destructive/10 text-destructive",
  // Workflow lifecycle. Published is live and archived is retired — the
  // latter reads as muted on purpose, since an archived workflow is shown
  // only when the user asked to see retired ones.
  published: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  archived: "border-muted-foreground/20 bg-muted text-muted-foreground",
  valid: "border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300",
  invalid: "border-destructive/30 bg-destructive/10 text-destructive",
};

/**
 * Maps a node instance's status to what the run views should show.
 *
 * A rollback supersedes the target's downstream nodes so their old submissions
 * and verdicts stay behind on the old attempt. But nothing replaced those
 * nodes — they are back to not having run, and that is what the canvas has to
 * say. "Superseded" is only the truth for a submission revision, which is why
 * this maps node status rather than the badge itself.
 *
 * Who rolled back, when, and why is in the operation log; the canvas does not
 * repeat it.
 */
export function workflowNodeDisplayStatus(status: string): string {
  return status === "superseded" ? "pending" : status;
}

/**
 * The node panel's one heading rule. Section labels are xs and muted so the
 * content under them is what the eye lands on; four copies of this string had
 * drifted across the workbench and the issue list.
 */
export const WORKFLOW_SECTION_HEADING = "text-xs font-medium text-muted-foreground";

/**
 * Whether a node is still in play — the flow has reached it and has not left.
 *
 * Mirrors workflowNodeIsOpen on the server. It is a function rather than the
 * status list written out at each call site because the list grew an entry
 * (in_review) and six hand-copied versions of it did not, which hid the
 * verdict form from the reviewer the node was waiting on.
 */
export function isWorkflowNodeOpen(status: string): boolean {
  return status === "active" || status === "in_review" ||
    status === "waiting" || status === "blocked";
}

function StatusIcon({ status }: { status: string }) {
  const className = "size-3";
  switch (status) {
    case "running":
    case "active":
      return <PlayCircle className={className} />;
    case "waiting":
    case "in_review":
    case "pending":
    case "pending_materialization":
      return <Clock3 className={className} />;
    case "materializing":
      return <CircleDashed className={className} />;
    case "paused":
      return <PauseCircle className={className} />;
    case "completed":
    case "materialized":
    case "approved":
    case "pass":
    case "valid":
      return <CheckCircle2 className={className} />;
    case "blocked":
    case "failed":
    case "needs_setup":
    case "rejected":
    case "fail":
    case "invalid":
      return <AlertTriangle className={className} />;
    case "cancelled":
    case "detached":
      return <XCircle className={className} />;
    case "skipped":
    case "superseded":
      return <SkipForward className={className} />;
    case "published":
      return <CheckCircle2 className={className} />;
    case "archived":
      return <Archive className={className} />;
    case "unknown":
      return <CircleDashed className={className} />;
    default:
      return <Circle className={className} />;
  }
}

export function WorkflowStatusBadge({
  status,
  className,
}: {
  status: string;
  className?: string;
}) {
  const { t } = useT("workflows");
  const label = (() => {
    switch (status) {
      case "needs_setup": return t(($) => $.status.needs_setup);
      case "running": return t(($) => $.status.running);
      case "paused": return t(($) => $.status.paused);
      case "completed": return t(($) => $.status.completed);
      case "cancelled": return t(($) => $.status.cancelled);
      case "failed": return t(($) => $.status.failed);
      case "active": return t(($) => $.status.active);
      case "waiting": return t(($) => $.status.waiting);
      case "in_review": return t(($) => $.status.in_review);
      case "blocked": return t(($) => $.status.blocked);
      case "skipped": return t(($) => $.status.skipped);
      case "superseded": return t(($) => $.status.superseded);
      case "valid": return t(($) => $.status.valid);
      case "invalid": return t(($) => $.status.invalid);
      case "pending": return t(($) => $.status.pending);
      case "pending_materialization": return t(($) => $.status.pending_materialization);
      case "materializing": return t(($) => $.status.materializing);
      case "materialized": return t(($) => $.status.materialized);
      case "detached": return t(($) => $.status.detached);
      case "approved": return t(($) => $.status.approved);
      case "rejected": return t(($) => $.status.rejected);
      case "pass": return t(($) => $.status.pass);
      case "fail": return t(($) => $.status.fail);
      case "published": return t(($) => $.templates.published);
      case "archived": return t(($) => $.templates.archived);
      default: return t(($) => $.status.unknown);
    }
  })();

  return (
    <Badge
      variant="outline"
      className={cn("gap-1 font-normal", statusStyle[status], className)}
    >
      <StatusIcon status={status} />
      {label}
    </Badge>
  );
}

export function WorkflowStatusDot({ status }: { status: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "size-2 shrink-0 rounded-full bg-muted-foreground/40 ring-2 ring-background",
        (status === "running" || status === "active") && "bg-emerald-500",
        (status === "waiting" || status === "pending" || status === "needs_setup") && "bg-amber-500",
        status === "completed" && "bg-blue-500",
        status === "paused" && "bg-sky-500",
        (status === "blocked" || status === "failed") && "bg-destructive",
      )}
    />
  );
}
