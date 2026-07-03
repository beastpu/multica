"use client";

import { MoveDownRight, MoveUpRight } from "lucide-react";
import { useT } from "../../i18n";
import type { OperationsKpis, OperationsRate } from "../operations-metrics";

// Headline KPI band for the operations page. Grouped by denominator so unlike
// bases don't masquerade as comparable cards:
//   - a delivery-composition bar over 外部完成 (MECE by AI role) whose read-off
//     is participation + contribution (same base, nested);
//   - two small ratio cards — the assisted pass rate (judged plans with a
//     committed CL, human-shipped; the always-100% direct-delivery count is a
//     footer datum on it) and assessment coverage (over AI participation);
//   - a muted data-health footnote, then the nested delivery funnel.
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
  const assistedDelta = stableDelta(
    kpis.aiAssistedPassRate,
    previous.aiAssistedPassRate,
  );
  const coverageDelta = stableDelta(kpis.coverageRate, previous.coverageRate);
  // MECE composition of 外部完成 by AI role — one segment per ticket. Colour
  // reads hottest→coldest by AI involvement (direct > assisted > unconverted >
  // none). participation = first three segments, contribution = first two.
  const comp = kpis.composition;
  const compBase = Math.max(1, funnel.externalDone);
  const segments = [
    {
      key: "direct",
      label: t(($) => $.operations.summary.comp_direct),
      count: comp.directDelivered,
      cls: "bg-primary",
    },
    {
      key: "assisted",
      label: t(($) => $.operations.summary.comp_assisted),
      count: comp.assisted,
      cls: "bg-primary/55",
    },
    {
      key: "unconverted",
      label: t(($) => $.operations.summary.comp_unconverted),
      count: comp.unconverted,
      cls: "bg-muted-foreground/40",
    },
    {
      key: "not_participated",
      label: t(($) => $.operations.summary.comp_not_participated),
      count: comp.notParticipated,
      cls: "bg-muted",
    },
  ];
  return (
    <section className="grid gap-3">
      <div className="grid gap-3 rounded-lg border bg-card p-4">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-xs font-medium text-muted-foreground">
            {t(($) => $.operations.summary.composition_title)}
          </h2>
          <span className="text-xs text-muted-foreground tabular-nums">
            {t(($) => $.operations.summary.comp_rates, {
              participation: formatPercent(kpis.participationRate),
              contribution: formatPercent(kpis.contributionRate),
            })}
          </span>
        </div>
        <div className="flex h-3 w-full overflow-hidden rounded-full bg-muted">
          {segments.map((s) =>
            s.count > 0 ? (
              <div
                key={s.key}
                className={s.cls}
                style={{ width: `${(s.count / compBase) * 100}%` }}
                title={`${s.label} ${s.count}`}
              />
            ) : null,
          )}
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
          {segments.map((s) => (
            <span key={s.key} className="inline-flex items-center gap-1.5">
              <span className={`h-2 w-2 rounded-full ${s.cls}`} />
              {s.label}
              <span className="tabular-nums text-foreground/80">{s.count}</span>
            </span>
          ))}
        </div>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <RateCard
          label={t(($) => $.operations.summary.assisted_pass_rate)}
          hint={t(($) => $.operations.summary.assisted_pass_rate_hint, {
            num: kpis.aiAssistedPassRate.numerator,
            den: kpis.aiAssistedPassRate.denominator,
          })}
          rate={kpis.aiAssistedPassRate}
          delta={assistedDelta}
          deltaLabel={deltaLabel}
          deltaText={deltaText(assistedDelta)}
          upIsGood
          // Direct deliveries (AI submitted the final CL itself) pass by
          // construction — a plan compared to its own shipped CL — so they get
          // a count here instead of a whole always-100% card.
          footer={t(($) => $.operations.summary.independent_submissions, {
            count: kpis.aiDeliveredPassRate.denominator,
          })}
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
