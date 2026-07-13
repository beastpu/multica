"use client";

import { ChevronRight } from "lucide-react";
import { useT } from "../../i18n";
import type {
  DeliveryComposition,
  OperationsKpis,
  OperationsRate,
} from "../operations-metrics";
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

// A MECE view over every external-done ticket. Unconverted AI plans are part
// of assisted repair by the operating definition, while tickets with no AI
// delivery role remain visible so the bar always reconciles to the full
// external-done denominator.
function CompositionBar({
  composition,
  externalDone,
}: {
  composition: DeliveryComposition;
  externalDone: number;
}) {
  const { t } = useT("usage");
  const title = t(($) => $.operations.summary.composition_title);
  const assisted = composition.assisted + composition.unconverted;
  const participated = composition.directDelivered + assisted;
  const segments = [
    {
      key: "automatic",
      label: t(($) => $.operations.summary.composition_direct),
      count: composition.directDelivered,
      className: "bg-chart-1",
    },
    {
      key: "assisted",
      label: t(($) => $.operations.summary.assisted_with_unconverted),
      count: assisted,
      className: "bg-chart-2",
    },
    {
      key: "not-participated",
      label: t(($) => $.operations.summary.composition_not_participated),
      count: composition.notParticipated,
      className: "bg-muted-foreground/25",
    },
  ];
  const pct = (count: number) =>
    externalDone > 0
      ? `${Math.round((count / externalDone) * 100)}%`
      : "0%";

  return (
    <section
      role="region"
      aria-label={title}
      className="rounded-lg border bg-card p-4"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-xs font-medium text-muted-foreground">{title}</h2>
        <span className="text-xs text-muted-foreground tabular-nums">
          {t(($) => $.operations.summary.composition_total, {
            count: participated,
            total: externalDone,
          })}
        </span>
      </div>
      {externalDone === 0 ? (
        <p className="mt-3 text-xs text-muted-foreground">
          {t(($) => $.operations.summary.composition_empty)}
        </p>
      ) : (
        <>
          <div
            className="mt-3.5 flex h-5 overflow-hidden rounded-md bg-muted"
            aria-hidden="true"
          >
            {segments
              .filter((segment) => segment.count > 0)
              .map((segment) => (
                <span
                  key={segment.key}
                  className={segment.className}
                  style={{ width: `${(segment.count / externalDone) * 100}%` }}
                />
              ))}
          </div>
          <div className="mt-2.5 grid gap-2 sm:grid-cols-3">
            {segments.map((segment) => (
              <span
                key={segment.key}
                className="inline-flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground"
              >
                <span
                  aria-hidden="true"
                  className={`h-2 w-2 shrink-0 rounded-full ${segment.className}`}
                />
                <span className="leading-tight">{segment.label}</span>
                <span className="ml-auto font-medium text-foreground tabular-nums">
                  {segment.count}
                </span>
                <span className="tabular-nums">· {pct(segment.count)}</span>
              </span>
            ))}
          </div>
        </>
      )}
    </section>
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
  const { funnel, composition } = kpis;
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
      <CompositionBar
        composition={composition}
        externalDone={funnel.externalDone}
      />
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
        />
        <RateCard
          label={t(($) => $.operations.summary.assisted_rate)}
          hint={t(($) => $.operations.summary.assisted_rate_hint, {
            num: kpis.assistedRate.numerator,
            den: kpis.assistedRate.denominator,
          })}
          rate={kpis.assistedRate}
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
