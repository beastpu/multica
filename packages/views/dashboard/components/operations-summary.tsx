"use client";

import { useT } from "../../i18n";
import type {
  DeliveryComposition,
  OperationsKpis,
  OperationsRate,
} from "../operations-metrics";
import type { OperationsCardKey } from "./operations-drawers";

// Headline KPI band for the operations page: three cards forming one strict
// nesting chain — contribution (AI produced a plan / 外部完成) → coverage
// (judged / AI produced) → pass rate (correct / judged). Each card's
// denominator IS the previous card's numerator, so the numerators read as a
// single shrinking pipeline (e.g. 149 → 80 → 50) and can never appear to
// contradict each other across cards. Delivery attribution (direct/assisted)
// lives in the analysis tab's attribution distribution; the funnel below
// carries absolute counts and stage drop-offs.

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
        className="flex flex-col gap-2 rounded-lg border bg-card p-4 text-left transition-colors hover:border-border hover:bg-muted/30 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
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

// The delivery-composition stacked bar over AI-involved shipped tickets only
// — pure human fixes are deliberately out of scope (they're context, not a
// reference point for AI effectiveness). Chart tokens run dark→light so the
// segments read as "AI's work fading out of the delivery".
function CompositionBar({
  composition,
  externalDone,
  externalDoneBreakdown = [],
}: {
  composition: DeliveryComposition;
  externalDone: number;
  // Which external statuses make up 外部完成 — shown on hover over the total.
  externalDoneBreakdown?: Array<{ label: string; count: number }>;
}) {
  const { t } = useT("usage");
  const segments = [
    {
      key: "direct",
      label: t(($) => $.operations.summary.composition_direct),
      count: composition.directDelivered,
      className: "bg-chart-1",
    },
    {
      key: "assisted",
      label: t(($) => $.operations.summary.composition_assisted),
      count: composition.assisted,
      className: "bg-chart-2",
    },
    {
      key: "unconverted",
      label: t(($) => $.operations.summary.composition_unconverted),
      count: composition.unconverted,
      className: "bg-chart-4",
    },
  ];
  const participated =
    composition.directDelivered + composition.assisted + composition.unconverted;
  const pct = (count: number) =>
    participated > 0 ? `${Math.round((count / participated) * 100)}%` : "0%";
  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-xs font-medium text-muted-foreground">
          {t(($) => $.operations.summary.composition_title)}
        </h2>
        <span
          className="text-xs text-muted-foreground tabular-nums"
          // Hover reveals which external statuses sum to 外部完成 (multiple
          // raw statuses can map to done), e.g. 测试通过 291 · 已关闭 36.
          title={
            externalDoneBreakdown.length > 0
              ? externalDoneBreakdown
                  .map((entry) => `${entry.label} ${entry.count}`)
                  .join(" · ")
              : undefined
          }
        >
          {t(($) => $.operations.summary.composition_total, {
            count: participated,
            total: externalDone,
          })}
        </span>
      </div>
      {participated === 0 ? (
        <p className="mt-3 text-xs text-muted-foreground">
          {t(($) => $.operations.summary.composition_empty)}
        </p>
      ) : (
        <>
          <div className="mt-3 flex h-3 overflow-hidden rounded-full">
            {segments
              .filter((s) => s.count > 0)
              .map((s) => (
                <div
                  key={s.key}
                  className={`${s.className} min-w-1`}
                  style={{ flexGrow: s.count }}
                  title={`${s.label} ${s.count} · ${pct(s.count)}`}
                />
              ))}
          </div>
          <div className="mt-2.5 flex flex-wrap gap-x-4 gap-y-1">
            {segments.map((s) => (
              <span
                key={s.key}
                className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"
              >
                <span
                  className={`h-2 w-2 shrink-0 rounded-full ${s.className}`}
                />
                {s.label}
                <span className="font-medium text-foreground tabular-nums">
                  {s.count}
                </span>
                <span className="tabular-nums">· {pct(s.count)}</span>
              </span>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

export function OperationsSummary({
  kpis,
  days,
  onCardClick,
  externalDoneBreakdown = [],
}: {
  kpis: OperationsKpis;
  // Trailing-window length, for the plain-language overview line.
  days: number;
  // When set, each headline card opens its breakdown drawer on click.
  onCardClick?: (card: OperationsCardKey) => void;
  // Which external statuses make up the external-done stage (multiple raw
  // statuses can map to done), largest first, e.g. 测试通过 291 · 已关闭 36.
  externalDoneBreakdown?: Array<{ label: string; count: number }>;
}) {
  const { t } = useT("usage");
  const { funnel, composition } = kpis;
  return (
    <section className="grid gap-3">
      {/* Plain-language overview: AI-involved deliveries only (pure human
          fixes are context, not a reference point), reconciling exactly with
          the bar below, plus the judged pass rate. */}
      <p className="text-sm text-muted-foreground">
        {t(($) => $.operations.summary.overview, {
          days,
          done: funnel.externalDone,
          participated:
            composition.directDelivered +
            composition.assisted +
            composition.unconverted,
          direct: composition.directDelivered,
          assisted: composition.assisted,
          unconverted: composition.unconverted,
          passRate: formatPercent(kpis.passRate),
        })}
      </p>
      <CompositionBar
        composition={composition}
        externalDone={funnel.externalDone}
        externalDoneBreakdown={externalDoneBreakdown}
      />
      <div className="grid gap-3 sm:grid-cols-3">
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
          label={t(($) => $.operations.summary.coverage_rate)}
          hint={t(($) => $.operations.summary.coverage_rate_hint, {
            num: kpis.coverageRate.numerator,
            den: kpis.coverageRate.denominator,
          })}
          rate={kpis.coverageRate}
          onClick={onCardClick ? () => onCardClick("coverage") : undefined}
          clickHint={t(($) => $.operations.drawer.card_hint)}
        />
        <RateCard
          label={t(($) => $.operations.summary.pass_rate)}
          hint={t(($) => $.operations.summary.pass_rate_hint, {
            num: kpis.passRate.numerator,
            den: kpis.passRate.denominator,
          })}
          rate={kpis.passRate}
          onClick={onCardClick ? () => onCardClick("pass") : undefined}
          clickHint={t(($) => $.operations.drawer.card_hint)}
        />
      </div>
      <div className="text-xs text-muted-foreground">
        {t(($) => $.operations.summary.health_footnote, {
          noOutput: kpis.noOutput,
          unjudged: kpis.unjudged,
        })}
      </div>
    </section>
  );
}
