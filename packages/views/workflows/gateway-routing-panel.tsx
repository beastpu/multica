"use client";

import { ArrowRight, Check, Minus, X } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { WORKFLOW_SECTION_HEADING } from "./workflow-status";
import { useT } from "../i18n";
import type { GatewayCaseOutcome, GatewayRouting } from "./gateway-routing";

function formatValue(value: unknown): string {
  if (value === undefined) return "—";
  if (typeof value === "string") return value;
  if (Array.isArray(value)) return value.join("、");
  return JSON.stringify(value);
}

function CaseMark({ outcome }: { outcome: GatewayCaseOutcome }) {
  if (outcome.selected) {
    return <Check className="size-4 shrink-0 text-primary" aria-hidden />;
  }
  if (outcome.matched === false) {
    return <X className="size-4 shrink-0 text-muted-foreground" aria-hidden />;
  }
  return <Minus className="size-4 shrink-0 text-muted-foreground/60" aria-hidden />;
}

/**
 * What a decision node did, and why.
 *
 * Every case is listed, not just the winner: "why not that branch" is the
 * question people actually arrive with, and it is unanswerable from a panel
 * that only names the path taken. The values come from the routing event
 * rather than from the current submissions, so a node reworked afterwards
 * does not silently rewrite the explanation of a branch already taken.
 */
export function GatewayRoutingPanel({ routing }: { routing: GatewayRouting }) {
  const { t } = useT("workflows");
  const winners = routing.cases.filter((item) => item.selected);

  return (
    <div className="space-y-4">
      <div>
        <p className={WORKFLOW_SECTION_HEADING}>
          {t(($) => $.workbench.routing_result)}
        </p>
        <p className="mt-1 text-sm">
          {routing.decided
            ? winners.length > 0
              ? t(($) => $.workbench.routing_taken, {
                targets: winners.map((item) => item.target || item.label)
                  .join("、"),
              })
              : t(($) => $.workbench.routing_unknown)
            : t(($) => $.workbench.routing_pending)}
        </p>
      </div>

      <ul className="space-y-1.5">
        {routing.cases.map((outcome) => (
          <li
            key={outcome.id}
            className={cn(
              "flex items-start gap-2.5 rounded-xl border px-3 py-2.5",
              outcome.selected
                ? "border-primary/40 bg-primary/5"
                : "border-border/60",
            )}
          >
            <CaseMark outcome={outcome} />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-1.5">
                <span
                  className={cn(
                    "text-sm",
                    outcome.selected
                      ? "font-medium"
                      : "text-muted-foreground",
                  )}
                >
                  {outcome.label}
                </span>
                {outcome.target && (
                  <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                    <ArrowRight className="size-3" aria-hidden />
                    {outcome.target}
                  </span>
                )}
              </div>
              {outcome.when
                ? (
                  <code className="mt-1 block break-all font-mono text-xs text-muted-foreground">
                    {outcome.when}
                  </code>
                )
                : (
                  <p className="mt-1 text-xs text-muted-foreground">
                    {t(($) => $.workbench.routing_else_hint)}
                  </p>
                )}
              {/* Only worth saying about a case the run never looked at. */}
              {routing.decided && outcome.matched === null &&
                outcome.id !== "else" && (
                <p className="mt-1 text-xs text-muted-foreground/80">
                  {t(($) => $.workbench.routing_not_evaluated)}
                </p>
              )}
            </div>
          </li>
        ))}
      </ul>

      {routing.decided && routing.evidence.length > 0 && (
        <div>
          <p className={WORKFLOW_SECTION_HEADING}>
            {t(($) => $.workbench.routing_evidence)}
          </p>
          <dl className="mt-2 divide-y rounded-xl border text-sm">
            {routing.evidence.map((entry) => (
              <div
                key={entry.path}
                className="flex items-baseline justify-between gap-3 px-3 py-2"
              >
                <dt className="min-w-0 truncate font-mono text-xs text-muted-foreground">
                  {entry.path}
                </dt>
                <dd
                  className={cn(
                    "shrink-0 text-xs",
                    entry.value === undefined && "text-muted-foreground",
                  )}
                >
                  {entry.value === undefined
                    ? t(($) => $.workbench.routing_evidence_missing)
                    : formatValue(entry.value)}
                </dd>
              </div>
            ))}
          </dl>
        </div>
      )}
    </div>
  );
}
