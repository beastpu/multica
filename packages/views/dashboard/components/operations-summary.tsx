"use client";

import { MoveDownRight, MoveUpRight } from "lucide-react";
import { useT } from "../../i18n";
import type { OperationsKpis, OperationsRate } from "../operations-metrics";

// Headline KPI band for the operations page: four cards that read left to
// right as one causal chain — scale (participation) → conversion
// (contribution) → quality (plan pass rate) → confidence (assessment
// coverage). The first two share the 外部完成 base and nest (contribution ⊆
// participation, their gap = plans that never converted); the last two sit on
// their own bases, spelled out in each card's hint. Contribution is a floor
// value — it needs attribution, which evidence blocks suppress — so it must be
// read against coverage: low coverage means the real contribution is higher.
// The per-role breakdown (direct/assisted/unconverted) lives in the analysis
// tab's attribution distribution, not here; the funnel below carries absolute
// counts and stage drop-offs.
// `previous` carries the KPIs of the equal-length window before the selected
// one; deltas are null (render nothing) when a window's denominator is 0 or the
// sample is too small to be anything but noise.

function formatPercent(rate: OperationsRate): string {
  if (rate.value == null) return "—";
  return `${Math.round(rate.value * 1000) / 10}%`;
}

// Delta in percentage points between two rates, or null when either side has
// no denominator to stand on.
function deltaPoints(
  current: OperationsRate,
  previous: OperationsRate,
): number | null {
  if (current.value == null || previous.value == null) return null;
  return Math.round((current.value - previous.value) * 1000) / 10;
}

function RateCard({
  label,
  hint,
  rate,
  delta,
  deltaLabel,
  deltaText,
  // Whether an increase is good news (pass rate ↑ good) or bad (no-output ↑ bad).
  upIsGood,
  // Optional extra datum under the hint (e.g. the independent-submission
  // count on the assisted card).
  footer,
}: {
  label: string;
  hint: string;
  rate: OperationsRate;
  delta: number | null;
  deltaLabel: string;
  deltaText: string;
  upIsGood: boolean;
  footer?: string;
}) {
  const showDelta = delta != null && delta !== 0;
  const good = delta != null && (delta > 0 ? upIsGood : !upIsGood);
  return (
    <div className="flex flex-col gap-2 rounded-lg border bg-card p-4">
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      <div className="flex items-baseline gap-2">
        <span className="text-3xl font-semibold leading-none tabular-nums">
          {formatPercent(rate)}
        </span>
        {showDelta ? (
          <span
            className={`flex items-center gap-0.5 text-xs font-medium tabular-nums ${
              good ? "text-success" : "text-destructive"
            }`}
            title={deltaLabel}
          >
            {delta > 0 ? (
              <MoveUpRight className="h-3 w-3" />
            ) : (
              <MoveDownRight className="h-3 w-3" />
            )}
            {deltaText}
          </span>
        ) : null}
      </div>
      <div className="text-xs text-muted-foreground">{hint}</div>
      {footer ? (
        <div className="border-t pt-2 text-xs text-muted-foreground">
          {footer}
        </div>
      ) : null}
    </div>
  );
}

export function OperationsSummary({
  kpis,
  previous,
  externalDoneBreakdown = [],
}: {
  kpis: OperationsKpis;
  previous: OperationsKpis;
  // Which external statuses make up the external-done stage (multiple raw
  // statuses can map to done), largest first, e.g. 测试通过 291 · 已关闭 36.
  externalDoneBreakdown?: Array<{ label: string; count: number }>;
}) {
  const { t } = useT("usage");
  const deltaLabel = t(($) => $.operations.summary.delta_label);
  const deltaText = (delta: number | null) =>
    delta == null
      ? ""
      : t(($) => $.operations.summary.delta_points, {
          delta: `${delta > 0 ? "+" : ""}${delta}`,
        });
  const { funnel } = kpis;
  const stages = [
    {
      key: "external_done",
      label: t(($) => $.operations.summary.stage_external_done),
      count: funnel.externalDone,
    },
    {
      key: "ai_engaged",
      label: t(($) => $.operations.summary.stage_ai_engaged),
      count: funnel.aiEngaged,
    },
    {
      key: "ai_planned",
      label: t(($) => $.operations.summary.stage_ai_planned),
      count: funnel.aiPlanned,
    },
    {
      key: "verifiable",
      label: t(($) => $.operations.summary.stage_verifiable),
      count: funnel.verifiable,
    },
    {
      key: "judged",
      label: t(($) => $.operations.summary.stage_judged),
      count: funnel.judged,
    },
    {
      key: "passed",
      label: t(($) => $.operations.summary.stage_passed),
      count: funnel.passed,
    },
  ];
  const max = Math.max(1, ...stages.map((s) => s.count));
  // Below this denominator the period-over-period delta is mostly sampling
  // noise (e.g. 7/12 has a ±25pt confidence interval), so we hide it rather
  // than imply a precision the sample can't support.
  const SMALL_SAMPLE = 30;
  const stableDelta = (cur: OperationsRate, prev: OperationsRate) =>
    cur.denominator < SMALL_SAMPLE ? null : deltaPoints(cur, prev);
  const participationDelta = stableDelta(
    kpis.participationRate,
    previous.participationRate,
  );
  const contributionDelta = stableDelta(
    kpis.contributionRate,
    previous.contributionRate,
  );
  const passDelta = stableDelta(kpis.passRate, previous.passRate);
  const coverageDelta = stableDelta(kpis.coverageRate, previous.coverageRate);
  return (
    <section className="grid gap-3">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <RateCard
          label={t(($) => $.operations.summary.participation_rate)}
          hint={t(($) => $.operations.summary.participation_rate_hint, {
            num: kpis.participationRate.numerator,
            den: kpis.participationRate.denominator,
          })}
          rate={kpis.participationRate}
          delta={participationDelta}
          deltaLabel={deltaLabel}
          deltaText={deltaText(participationDelta)}
          upIsGood
        />
        <RateCard
          label={t(($) => $.operations.summary.contribution_rate)}
          hint={t(($) => $.operations.summary.contribution_rate_hint, {
            num: kpis.contributionRate.numerator,
            den: kpis.contributionRate.denominator,
          })}
          rate={kpis.contributionRate}
          delta={contributionDelta}
          deltaLabel={deltaLabel}
          deltaText={deltaText(contributionDelta)}
          upIsGood
        />
        <RateCard
          label={t(($) => $.operations.summary.pass_rate)}
          hint={t(($) => $.operations.summary.pass_rate_hint, {
            num: kpis.passRate.numerator,
            den: kpis.passRate.denominator,
          })}
          rate={kpis.passRate}
          delta={passDelta}
          deltaLabel={deltaLabel}
          deltaText={deltaText(passDelta)}
          upIsGood
        />
        <RateCard
          label={t(($) => $.operations.summary.coverage_rate)}
          hint={t(($) => $.operations.summary.coverage_rate_hint, {
            num: kpis.coverageRate.numerator,
            den: kpis.coverageRate.denominator,
          })}
          rate={kpis.coverageRate}
          delta={coverageDelta}
          deltaLabel={deltaLabel}
          deltaText={deltaText(coverageDelta)}
          upIsGood
        />
      </div>
      <div className="text-xs text-muted-foreground">
        {t(($) => $.operations.summary.health_footnote, {
          noOutput: kpis.noOutput,
          unjudged: kpis.unjudged,
        })}
      </div>
      <div className="rounded-lg border bg-card p-4">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-xs font-medium text-muted-foreground">
            {t(($) => $.operations.summary.funnel_title)}
          </h2>
          <span className="text-xs text-muted-foreground tabular-nums">
            {t(($) => $.operations.summary.funnel_total, {
              count: funnel.total,
            })}
          </span>
        </div>
        <div className="mt-3 grid gap-2">
          {stages.map((stage) => (
            <div key={stage.key} className="grid gap-1">
              <div className="grid grid-cols-[88px_minmax(0,1fr)_48px] items-center gap-3">
                <span className="truncate text-xs text-muted-foreground">
                  {stage.label}
                </span>
                <div className="h-2 overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full rounded-full bg-primary"
                    style={{
                      width: `${Math.max(stage.count > 0 ? 4 : 0, (stage.count / max) * 100)}%`,
                    }}
                  />
                </div>
                <span className="text-right text-xs font-medium tabular-nums">
                  {stage.count}
                </span>
              </div>
              {stage.key === "external_done" &&
              externalDoneBreakdown.length > 0 ? (
                <div className="grid grid-cols-[88px_minmax(0,1fr)] gap-3">
                  <span aria-hidden="true" />
                  <span className="truncate text-xs text-muted-foreground tabular-nums">
                    {externalDoneBreakdown
                      .map((entry) => `${entry.label} ${entry.count}`)
                      .join(" · ")}
                  </span>
                </div>
              ) : null}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
