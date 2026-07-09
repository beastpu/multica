"use client";

import { ArrowLeft, ExternalLink } from "lucide-react";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { Button } from "@multica/ui/components/ui/button";
import { paths } from "@multica/core/paths";
import type { AgentFixRecord } from "@multica/core/types";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";
import {
  ToneBadge,
  agentFixEnumLabel,
  agentFixEnumTone,
  agentFixReviewReasonLabels,
  type UsageT,
} from "./agent-fix-review";
import {
  deliveryRole,
  deriveAttribution,
  derivedEvidence,
  firstSwarmReviewUrl,
  fixDayIso,
  hasP4Signal,
  isVerifiableOutput,
  qualityJudgement,
  swarmReviewUrl,
} from "../operations-metrics";

// ---------------------------------------------------------------------------
// Right-side drawers for the operations page, mirroring the repair-assessment
// demo's exploration flow:
//   rate card → branch breakdown → ticket list → single-issue detail
// One Sheet, three panels, driven by a single state union owned by the page.
// Branch counts reuse the exact metrics predicates (deliveryRole /
// isVerifiableOutput / qualityJudgement) so the drawer always reconciles with
// the KPI cards it was opened from.
// ---------------------------------------------------------------------------

export type OperationsCardKey = "contribution" | "coverage" | "pass";

export type OperationsSheetState =
  | { kind: "card"; card: OperationsCardKey }
  | { kind: "branch"; card: OperationsCardKey; branch: string }
  | {
      kind: "issue";
      fix: AgentFixRecord;
      // Where the issue panel was opened from, for the back affordance.
      // Absent when opened from a detail-table row.
      from?: { card: OperationsCardKey; branch: string };
    }
  | null;

// The detail-table filter a branch's "view all" jump applies.
export interface OperationsDrillFilter {
  attribution?: string;
  quality?: string;
  pendingOnly?: boolean;
}

interface BranchDef {
  key: string;
  label: string;
  rows: AgentFixRecord[];
  drill?: OperationsDrillFilter;
}

// Ticket-card list cap inside the drawer — beyond it, "view all" (or a
// narrower filter) is the intended path, mirroring the detail table's paging.
const LIST_CAP = 60;

function cardTitle(card: OperationsCardKey, t: ReturnType<typeof useT<"usage">>["t"]): string {
  return card === "contribution"
    ? t(($) => $.operations.summary.contribution_rate)
    : card === "coverage"
      ? t(($) => $.operations.summary.coverage_rate)
      : t(($) => $.operations.summary.pass_rate);
}

// The branch partition of one card's pool. Contribution splits the AI-involved
// deliveries by role; coverage splits them by whether a verdict exists; pass
// splits the judged pool by verdict.
function cardBranches(
  card: OperationsCardKey,
  rows: AgentFixRecord[],
  t: ReturnType<typeof useT<"usage">>["t"],
  tx: UsageT,
): BranchDef[] {
  if (card === "contribution") {
    return [
      {
        key: "direct",
        label: t(($) => $.operations.summary.composition_direct),
        rows: rows.filter((f) => deliveryRole(f) === "direct"),
        drill: { attribution: "ai_delivered" },
      },
      {
        key: "assisted",
        label: t(($) => $.operations.summary.composition_assisted),
        rows: rows.filter((f) => deliveryRole(f) === "assisted"),
        drill: { attribution: "ai_assisted" },
      },
      {
        key: "unconverted",
        label: t(($) => $.operations.summary.composition_unconverted),
        rows: rows.filter((f) => deliveryRole(f) === "unconverted"),
      },
    ];
  }
  const participated = rows.filter((f) => deliveryRole(f) !== "none");
  if (card === "coverage") {
    const judged = (f: AgentFixRecord) =>
      isVerifiableOutput(f) && qualityJudgement(f) !== "";
    return [
      {
        key: "judged",
        label: t(($) => $.operations.summary.stage_judged),
        rows: participated.filter(judged),
      },
      {
        key: "unjudged",
        label: t(($) => $.operations.drawer.unjudged),
        rows: participated.filter((f) => !judged(f)),
        drill: { pendingOnly: true },
      },
    ];
  }
  const judgedRows = participated.filter(
    (f) => isVerifiableOutput(f) && qualityJudgement(f) !== "",
  );
  return ["likely_correct", "likely_needs_changes", "likely_wrong"].map(
    (quality) => ({
      key: quality,
      label: agentFixEnumLabel(tx, "quality", quality),
      rows: judgedRows.filter((f) => qualityJudgement(f) === quality),
      drill: { quality },
    }),
  );
}

export function OperationsSheet({
  state,
  onStateChange,
  rows,
  slug,
  swarmBase,
  viewTZ,
  issueStatusLabel,
  externalStatusLabel,
  onDrill,
}: {
  state: OperationsSheetState;
  onStateChange: (state: OperationsSheetState) => void;
  // The page's current KPI pool (filtered rows) — the same numbers the cards
  // show, so the breakdown never contradicts the card it explains.
  rows: AgentFixRecord[];
  slug: string | null;
  swarmBase: string;
  viewTZ: string;
  issueStatusLabel: (status: string) => string;
  externalStatusLabel: (fix: AgentFixRecord) => string;
  onDrill: (filter: OperationsDrillFilter) => void;
}) {
  const { t } = useT("usage");
  const tx = t as unknown as UsageT;
  return (
    <Sheet
      open={state !== null}
      onOpenChange={(open) => {
        if (!open) onStateChange(null);
      }}
    >
      <SheetContent
        side="right"
        className="w-[440px] gap-0 overflow-y-auto sm:max-w-md"
      >
        {state?.kind === "card" ? (
          <CardPanel
            card={state.card}
            rows={rows}
            t={t}
            tx={tx}
            onStateChange={onStateChange}
          />
        ) : state?.kind === "branch" ? (
          <BranchPanel
            card={state.card}
            branch={state.branch}
            rows={rows}
            t={t}
            tx={tx}
            onStateChange={onStateChange}
            onDrill={onDrill}
          />
        ) : state?.kind === "issue" ? (
          <IssuePanel
            fix={state.fix}
            from={state.from}
            slug={slug}
            swarmBase={swarmBase}
            viewTZ={viewTZ}
            issueStatusLabel={issueStatusLabel}
            externalStatusLabel={externalStatusLabel}
            t={t}
            tx={tx}
            onStateChange={onStateChange}
          />
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

type UsageTFn = ReturnType<typeof useT<"usage">>["t"];

function BackButton({
  label,
  onClick,
}: {
  label: string;
  onClick: () => void;
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      onClick={onClick}
      className="h-7 w-fit gap-1 px-1.5 text-xs text-muted-foreground"
    >
      <ArrowLeft className="h-3.5 w-3.5" />
      {label}
    </Button>
  );
}

function CardPanel({
  card,
  rows,
  t,
  tx,
  onStateChange,
}: {
  card: OperationsCardKey;
  rows: AgentFixRecord[];
  t: UsageTFn;
  tx: UsageT;
  onStateChange: (state: OperationsSheetState) => void;
}) {
  const branches = cardBranches(card, rows, t, tx);
  const total = branches.reduce((sum, b) => sum + b.rows.length, 0);
  const max = Math.max(1, ...branches.map((b) => b.rows.length));
  return (
    <>
      <SheetHeader>
        <SheetTitle>{cardTitle(card, t)}</SheetTitle>
        <SheetDescription>
          {t(($) => $.operations.caption, { count: total })}
        </SheetDescription>
      </SheetHeader>
      <div className="grid gap-1 px-4 pb-6">
        {branches.map((branch) => (
          <button
            key={branch.key}
            type="button"
            onClick={() =>
              onStateChange({ kind: "branch", card, branch: branch.key })
            }
            className="grid gap-1.5 rounded-md p-2 text-left transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <div className="flex items-center justify-between gap-3">
              <span className="min-w-0 truncate text-sm">{branch.label}</span>
              <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                {branch.rows.length}
                {total > 0
                  ? ` · ${Math.round((branch.rows.length / total) * 100)}%`
                  : ""}
              </span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary"
                style={{
                  width: `${Math.max(branch.rows.length > 0 ? 6 : 0, (branch.rows.length / max) * 100)}%`,
                }}
              />
            </div>
          </button>
        ))}
      </div>
    </>
  );
}

function BranchPanel({
  card,
  branch,
  rows,
  t,
  tx,
  onStateChange,
  onDrill,
}: {
  card: OperationsCardKey;
  branch: string;
  rows: AgentFixRecord[];
  t: UsageTFn;
  tx: UsageT;
  onStateChange: (state: OperationsSheetState) => void;
  onDrill: (filter: OperationsDrillFilter) => void;
}) {
  const def = cardBranches(card, rows, t, tx).find((b) => b.key === branch);
  if (!def) return null;
  const shown = def.rows.slice(0, LIST_CAP);
  return (
    <>
      <SheetHeader className="gap-1">
        <BackButton
          label={t(($) => $.operations.drawer.back)}
          onClick={() => onStateChange({ kind: "card", card })}
        />
        <SheetTitle>{def.label}</SheetTitle>
        <SheetDescription>
          {t(($) => $.operations.caption, { count: def.rows.length })}
        </SheetDescription>
      </SheetHeader>
      <div className="grid gap-2 px-4 pb-4">
        {def.rows.length === 0 ? (
          <p className="py-4 text-center text-xs text-muted-foreground">
            {t(($) => $.operations.drawer.empty)}
          </p>
        ) : (
          shown.map((fix) => (
            <TicketCard
              key={fix.issue_id || fix.task_id}
              fix={fix}
              tx={tx}
              onClick={() =>
                onStateChange({ kind: "issue", fix, from: { card, branch } })
              }
            />
          ))
        )}
        {def.rows.length > LIST_CAP ? (
          <p className="py-1 text-center text-xs text-muted-foreground">
            {t(($) => $.operations.drawer.more, {
              count: def.rows.length - LIST_CAP,
            })}
          </p>
        ) : null}
      </div>
      {def.drill ? (
        <div className="sticky bottom-0 border-t bg-popover px-4 py-3">
          <button
            type="button"
            onClick={() => onDrill(def.drill!)}
            className="text-xs font-medium text-muted-foreground underline underline-offset-2 transition-colors hover:text-foreground"
          >
            {t(($) => $.operations.drawer.view_all)}
          </button>
        </div>
      ) : null}
    </>
  );
}

function TicketCard({
  fix,
  tx,
  onClick,
}: {
  fix: AgentFixRecord;
  tx: UsageT;
  onClick: () => void;
}) {
  const attribution = deriveAttribution(fix);
  const snippet = (fix.p4_assessment?.summary || fix.last_comment || "").trim();
  return (
    <button
      type="button"
      onClick={onClick}
      className="grid gap-1 rounded-md border p-2.5 text-left transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <div className="flex items-center justify-between gap-2">
        <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">
          {fix.issue_identifier || "—"}
        </span>
        <ToneBadge tone={agentFixEnumTone("attribution", attribution)}>
          {agentFixEnumLabel(tx, "attribution", attribution)}
        </ToneBadge>
      </div>
      <span className="truncate text-sm font-medium">
        {fix.issue_title || "—"}
      </span>
      {snippet ? (
        <span className="line-clamp-2 text-xs text-muted-foreground">
          {snippet}
        </span>
      ) : null}
    </button>
  );
}

function KvRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start gap-3">
      <span className="w-20 shrink-0 pt-0.5 text-xs text-muted-foreground">
        {label}
      </span>
      <div className="min-w-0 flex-1 text-sm">{children}</div>
    </div>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div className="mt-1 text-xs font-medium text-muted-foreground">
      {children}
    </div>
  );
}

function IssuePanel({
  fix,
  from,
  slug,
  swarmBase,
  viewTZ,
  issueStatusLabel,
  externalStatusLabel,
  t,
  tx,
  onStateChange,
}: {
  fix: AgentFixRecord;
  from?: { card: OperationsCardKey; branch: string };
  slug: string | null;
  swarmBase: string;
  viewTZ: string;
  issueStatusLabel: (status: string) => string;
  externalStatusLabel: (fix: AgentFixRecord) => string;
  t: UsageTFn;
  tx: UsageT;
  onStateChange: (state: OperationsSheetState) => void;
}) {
  const p4 = fix.p4_assessment;
  const evidence = derivedEvidence(fix);
  const summary = (p4?.summary ?? "").trim();
  const reasons = agentFixReviewReasonLabels(tx, p4?.prediction_reasons);
  const warnings = (p4?.warnings ?? []).map((w) => String(w)).filter(Boolean);
  const swarmUrl =
    firstSwarmReviewUrl(fix) || swarmReviewUrl(swarmBase, evidence.swarm);
  const externalDone = fix.external?.done;
  const workItemUrl = fix.external?.url ?? "";
  const evidenceRows = [
    evidence.finalCl
      ? { label: t(($) => $.operations.p4.final_cl), value: evidence.finalCl }
      : null,
    evidence.shelve
      ? { label: t(($) => $.operations.p4.shelve), value: evidence.shelve }
      : null,
    evidence.swarm
      ? {
          label: t(($) => $.operations.p4.swarm),
          value: evidence.swarm,
          href: swarmUrl,
        }
      : null,
    evidence.workstream
      ? {
          label: t(($) => $.operations.p4.workstream),
          value: evidence.workstream,
        }
      : null,
  ].filter(Boolean) as Array<{ label: string; value: string; href?: string }>;
  return (
    <>
      <SheetHeader className="gap-1">
        {from ? (
          <BackButton
            label={t(($) => $.operations.drawer.back)}
            onClick={() =>
              onStateChange({ kind: "branch", card: from.card, branch: from.branch })
            }
          />
        ) : null}
        <SheetTitle className="font-mono text-sm">
          {fix.issue_identifier || "—"}
        </SheetTitle>
        <SheetDescription className="text-sm text-foreground">
          {fix.issue_title || "—"}
        </SheetDescription>
      </SheetHeader>
      <div className="grid gap-3 px-4 pb-6">
        <KvRow label={t(($) => $.operations.drawer.status)}>
          {issueStatusLabel(fix.issue_status) || "—"}
        </KvRow>
        <KvRow label={t(($) => $.operations.table.external_status)}>
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <ToneBadge
              tone={
                externalDone === true
                  ? "success"
                  : externalDone === false
                    ? "warning"
                    : "muted"
              }
            >
              {externalStatusLabel(fix) || t(($) => $.operations.no_reason)}
            </ToneBadge>
            {fix.external?.work_item_id ? (
              workItemUrl ? (
                <a
                  href={workItemUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex min-w-0 items-center gap-1 font-mono text-xs text-muted-foreground hover:text-foreground hover:underline"
                >
                  <span className="truncate">{fix.external.work_item_id}</span>
                  <ExternalLink className="h-3 w-3 shrink-0" />
                </a>
              ) : (
                <span className="truncate font-mono text-xs text-muted-foreground">
                  {fix.external.work_item_id}
                </span>
              )
            ) : null}
          </div>
        </KvRow>
        <KvRow label={t(($) => $.operations.table.agent)}>
          {fix.agent_name || "—"}
        </KvRow>
        <KvRow label={t(($) => $.operations.table.time)}>
          <span className="tabular-nums">{fixDayIso(fix, viewTZ) || "—"}</span>
        </KvRow>
        <KvRow label={t(($) => $.operations.table.ai_attribution)}>
          <div className="flex flex-wrap items-center gap-2">
            <ToneBadge
              tone={agentFixEnumTone("attribution", deriveAttribution(fix))}
            >
              {agentFixEnumLabel(tx, "attribution", deriveAttribution(fix))}
            </ToneBadge>
            {typeof p4?.confidence === "number" && !Number.isNaN(p4.confidence) ? (
              <span className="text-xs text-muted-foreground">
                {t(($) => $.operations.p4.confidence, {
                  value: `${Math.round(p4.confidence * 100)}%`,
                })}
              </span>
            ) : null}
          </div>
        </KvRow>
        <KvRow label={t(($) => $.operations.table.ai_quality)}>
          <ToneBadge
            tone={agentFixEnumTone(
              "quality",
              p4?.quality_prediction?.trim() || "unassessed",
            )}
          >
            {agentFixEnumLabel(
              tx,
              "quality",
              p4?.quality_prediction?.trim() || "unassessed",
            )}
          </ToneBadge>
        </KvRow>

        <SectionLabel>{t(($) => $.operations.drawer.summary)}</SectionLabel>
        {summary ? (
          <p className="whitespace-pre-wrap rounded-md border bg-muted/30 p-3 text-sm leading-relaxed">
            {summary}
          </p>
        ) : (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.operations.drawer.no_summary)}
          </p>
        )}

        {reasons.length > 0 ? (
          <>
            <SectionLabel>{t(($) => $.operations.drawer.reasons)}</SectionLabel>
            <div className="flex flex-wrap gap-1.5">
              {reasons.map((reason) => (
                <ToneBadge key={reason} tone="muted">
                  {reason}
                </ToneBadge>
              ))}
            </div>
          </>
        ) : null}

        {warnings.length > 0 ? (
          <>
            <SectionLabel>{t(($) => $.operations.p4.warnings)}</SectionLabel>
            <ul className="grid gap-1">
              {warnings.map((warning) => (
                <li
                  key={warning}
                  className="break-all font-mono text-xs text-muted-foreground"
                >
                  {warning}
                </li>
              ))}
            </ul>
          </>
        ) : null}

        {hasP4Signal(fix) && evidenceRows.length > 0 ? (
          <>
            <SectionLabel>
              {t(($) => $.operations.table.p4_evidence)}
            </SectionLabel>
            <div className="grid gap-1.5">
              {evidenceRows.map(({ label, value, href }) => (
                <div key={label} className="flex items-start gap-3">
                  <span className="w-20 shrink-0 text-xs uppercase text-muted-foreground">
                    {label}
                  </span>
                  {href ? (
                    <a
                      href={href}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="flex min-w-0 items-center gap-1 break-all font-mono text-xs hover:underline"
                    >
                      {value}
                      <ExternalLink className="h-3 w-3 shrink-0" />
                    </a>
                  ) : (
                    <span className="min-w-0 break-all font-mono text-xs">
                      {value}
                    </span>
                  )}
                </div>
              ))}
            </div>
          </>
        ) : null}

        {slug && fix.issue_identifier ? (
          <AppLink
            href={paths.workspace(slug).issueDetail(fix.issue_identifier)}
            className="mt-1 w-fit text-xs font-medium text-muted-foreground underline underline-offset-2 transition-colors hover:text-foreground"
          >
            {t(($) => $.operations.drawer.open_issue)}
          </AppLink>
        ) : null}
      </div>
    </>
  );
}
