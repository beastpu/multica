"use client";

import { useT } from "../../i18n";
import type { OperationsKpis, OperationsRate } from "../operations-metrics";

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
  // Optional extra datum under the hint (e.g. the independent-submission
  // count on the assisted card).
  footer,
}: {
  label: string;
  hint: string;
  rate: OperationsRate;
  footer?: string;
}) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border bg-card p-4">
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
    </div>
  );
}

export function OperationsSummary({
  kpis,
  externalDoneBreakdown = [],
}: {
  kpis: OperationsKpis;
  // Which external statuses make up the external-done stage (multiple raw
  // statuses can map to done), largest first, e.g. 测试通过 291 · 已关闭 36.
  externalDoneBreakdown?: Array<{ label: string; count: number }>;
}) {
  const { t } = useT("usage");
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
  return (
    <section className="grid gap-3">
      <div className="grid gap-3 sm:grid-cols-3">
        <RateCard
          label={t(($) => $.operations.summary.contribution_rate)}
          hint={t(($) => $.operations.summary.contribution_rate_hint, {
            num: kpis.contributionRate.numerator,
            den: kpis.contributionRate.denominator,
          })}
          rate={kpis.contributionRate}
        />
        <RateCard
          label={t(($) => $.operations.summary.coverage_rate)}
          hint={t(($) => $.operations.summary.coverage_rate_hint, {
            num: kpis.coverageRate.numerator,
            den: kpis.coverageRate.denominator,
          })}
          rate={kpis.coverageRate}
        />
        <RateCard
          label={t(($) => $.operations.summary.pass_rate)}
          hint={t(($) => $.operations.summary.pass_rate_hint, {
            num: kpis.passRate.numerator,
            den: kpis.passRate.denominator,
          })}
          rate={kpis.passRate}
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
