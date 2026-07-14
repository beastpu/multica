"use client";

import { ChevronRight } from "lucide-react";
import { useT } from "../../i18n";
import type {
  DeliveryComposition,
  OperationsKpis,
  OperationsRate,
} from "../operations-metrics";
import type { OperationsCardKey } from "./operations-drawers";

// Four operator-facing KPIs. Contribution measures pickup coverage over the
// eligible reporting pool; quality, automatic, and assisted use their defined
// AI assessment denominators so the outcome cards reconcile.

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
  className = "",
  // Optional extra datum under the hint (e.g. the independent-submission
  // count on the assisted card).
  footer,
}: {
  label: string;
  hint: string;
  rate: OperationsRate;
  onClick?: () => void;
  clickHint?: string;
  className?: string;
  footer?: string;
}) {
  const inner = (
    <>
      <div className="text-sm font-semibold text-foreground">{label}</div>
      <div className="text-xs leading-relaxed text-muted-foreground">
        {hint}
      </div>
      <div className="mt-1 flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <span className="text-3xl font-semibold leading-none tabular-nums">
          {formatPercent(rate)}
        </span>
        <span className="text-sm text-muted-foreground tabular-nums">
          {rate.numerator} / {rate.denominator}
        </span>
      </div>
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
        className={`group relative flex min-w-0 flex-col gap-1.5 p-3.5 text-left transition-colors hover:bg-muted/25 focus-visible:z-10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring ${className}`}
      >
        <ChevronRight className="absolute right-3 top-3 h-4 w-4 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
        {inner}
      </button>
    );
  }
  return (
    <div className={`flex min-w-0 flex-col gap-1.5 p-3.5 ${className}`}>
      {inner}
    </div>
  );
}

// A MECE view over every eligible ticket. Unconverted AI plans are part of
// assisted repair by the operating definition, while tickets with no AI
// delivery role remain visible so the bar reconciles to the full reporting
// denominator.
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
      className="rounded-lg border bg-card p-3.5"
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
            className="mt-2.5 flex h-2.5 overflow-hidden rounded-full bg-muted"
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
          <div className="mt-2 grid gap-x-6 gap-y-1.5 sm:grid-cols-3">
            {segments.map((segment) => (
              <div
                key={segment.key}
                className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground"
              >
                <span className="inline-flex min-w-0 items-center gap-1.5">
                  <span
                    aria-hidden="true"
                    className={`h-2 w-2 shrink-0 rounded-full ${segment.className}`}
                  />
                  <span className="leading-tight">{segment.label}</span>
                </span>
                <span className="shrink-0 tabular-nums">
                  <span className="font-medium text-foreground">
                    {segment.count}
                  </span>{" "}
                  · {pct(segment.count)}
                </span>
              </div>
            ))}
          </div>
        </>
      )}
    </section>
  );
}

export function OperationsSummary({
  kpis,
  onCardClick,
}: {
  kpis: OperationsKpis;
  onCardClick?: (card: OperationsCardKey) => void;
}) {
  const { t } = useT("usage");
  const { funnel, composition } = kpis;
  return (
    <section className="grid gap-2.5">
      <div className="grid overflow-hidden rounded-lg border bg-card sm:grid-cols-2 xl:grid-cols-4">
        <RateCard
          label={t(($) => $.operations.summary.contribution_rate)}
          hint={t(($) => $.operations.summary.contribution_rate_hint)}
          rate={kpis.contributionRate}
          onClick={onCardClick ? () => onCardClick("contribution") : undefined}
          clickHint={t(($) => $.operations.drawer.card_hint)}
          className="border-b sm:border-r xl:border-b-0"
        />
        <RateCard
          label={t(($) => $.operations.summary.quality_rate)}
          hint={t(($) => $.operations.summary.quality_rate_hint)}
          rate={kpis.qualityRate}
          onClick={onCardClick ? () => onCardClick("quality") : undefined}
          clickHint={t(($) => $.operations.drawer.card_hint)}
          className="border-b xl:border-b-0 xl:border-r"
        />
        <RateCard
          label={t(($) => $.operations.summary.automatic_rate)}
          hint={t(($) => $.operations.summary.automatic_rate_hint)}
          rate={kpis.automaticRate}
          className="border-b sm:border-b-0 sm:border-r"
        />
        <RateCard
          label={t(($) => $.operations.summary.assisted_rate)}
          hint={t(($) => $.operations.summary.assisted_rate_hint)}
          rate={kpis.assistedRate}
        />
      </div>
      <CompositionBar
        composition={composition}
        externalDone={funnel.externalDone}
      />
      <div
        className="justify-self-end text-[11px] text-muted-foreground tabular-nums"
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
