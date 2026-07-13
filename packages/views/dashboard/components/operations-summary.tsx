"use client";

import { ChevronRight } from "lucide-react";
import { useT } from "../../i18n";
import type { OperationsKpis, OperationsRate } from "../operations-metrics";
import type { OperationsCardKey } from "./operations-drawers";

// Four operator-facing KPIs. Contribution measures pickup coverage over every
// external-done item; quality, automatic, and assisted share the same
// evidence-backed fixable denominator so the outcome cards reconcile.

function formatPercent(rate: OperationsRate): string {
  if (rate.value == null) return "—";
  return `${Math.round(rate.value * 1000) / 10}%`;
}

function RateCard({
  label,
  hint,
  rate,
  // When set, the whole card is a button opening the breakdown drawer.
  onClick,
  clickHint,
  // Optional extra datum under the hint (e.g. the independent-submission
  // count on the assisted card).
  footer,
}: {
  label: string;
  hint: string;
  rate: OperationsRate;
  onClick?: () => void;
  clickHint?: string;
  footer?: string;
}) {
  const inner = (
    <>
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      <div className="flex items-baseline gap-2">
        <span className="text-3xl font-semibold leading-none tabular-nums">
          {formatPercent(rate)}
        </span>
      </div>
      <div className="text-xs text-muted-foreground">{hint}</div>
      {footer ? (
        <div className="border-t pt-2 text-xs text-muted-foreground">
          {footer}
        </div>
      ) : null}
    </>
  );
  if (onClick) {
    return (
      <button
        type="button"
        onClick={onClick}
        title={clickHint}
        className="group relative flex flex-col gap-2 rounded-lg border bg-card p-4 text-left transition-[transform,border-color,box-shadow] hover:-translate-y-px hover:border-border hover:shadow-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none"
      >
        <ChevronRight className="absolute right-3 top-3 h-4 w-4 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
        {inner}
      </button>
    );
  }
  return (
    <div className="flex flex-col gap-2 rounded-lg border bg-card p-4">
      {inner}
    </div>
  );
}

export function OperationsSummary({
  kpis,
  days,
  onCardClick,
}: {
  kpis: OperationsKpis;
  days: number;
  onCardClick?: (card: OperationsCardKey) => void;
}) {
  const { t } = useT("usage");
  const { funnel } = kpis;
  return (
    <section className="grid gap-3">
      <p className="text-sm text-muted-foreground">
        {t(($) => $.operations.summary.overview, {
          days,
          done: funnel.externalDone,
          handled: kpis.contributionRate.numerator,
          fixable: kpis.fixableCount,
        })}
      </p>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <RateCard
          label={t(($) => $.operations.summary.contribution_rate)}
          hint={t(($) => $.operations.summary.contribution_rate_hint, {
            num: kpis.contributionRate.numerator,
            den: kpis.contributionRate.denominator,
          })}
          rate={kpis.contributionRate}
          onClick={onCardClick ? () => onCardClick("contribution") : undefined}
          clickHint={t(($) => $.operations.drawer.card_hint)}
        />
        <RateCard
          label={t(($) => $.operations.summary.quality_rate)}
          hint={t(($) => $.operations.summary.quality_rate_hint, {
            num: kpis.qualityRate.numerator,
            den: kpis.qualityRate.denominator,
          })}
          rate={kpis.qualityRate}
          onClick={onCardClick ? () => onCardClick("quality") : undefined}
          clickHint={t(($) => $.operations.drawer.card_hint)}
        />
        <RateCard
          label={t(($) => $.operations.summary.automatic_rate)}
          hint={t(($) => $.operations.summary.automatic_rate_hint, {
            num: kpis.automaticRate.numerator,
            den: kpis.automaticRate.denominator,
          })}
          rate={kpis.automaticRate}
          onClick={onCardClick ? () => onCardClick("automatic") : undefined}
          clickHint={t(($) => $.operations.drawer.card_hint)}
        />
        <RateCard
          label={t(($) => $.operations.summary.assisted_rate)}
          hint={t(($) => $.operations.summary.assisted_rate_hint, {
            num: kpis.assistedRate.numerator,
            den: kpis.assistedRate.denominator,
          })}
          rate={kpis.assistedRate}
          onClick={onCardClick ? () => onCardClick("assisted") : undefined}
          clickHint={t(($) => $.operations.drawer.card_hint)}
        />
      </div>
      <div
        className="w-fit text-xs text-muted-foreground tabular-nums"
        title={t(($) => $.operations.summary.health_footnote_hint)}
      >
        {t(($) => $.operations.summary.health_footnote, {
          unassessed: kpis.unassessed,
          missingCl: kpis.missingExternalCl,
        })}
      </div>
    </section>
  );
}
