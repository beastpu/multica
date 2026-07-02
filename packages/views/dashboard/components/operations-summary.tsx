"use client";

import { MoveDownRight, MoveUpRight } from "lucide-react";
import { useT } from "../../i18n";
import type { OperationsKpis, OperationsRate } from "../operations-metrics";

// Headline KPI band for the operations page: the three north-star rates with a
// period-over-period delta, plus the nested delivery funnel. `previous` carries
// the KPIs of the equal-length window before the selected one; null deltas
// (either window's denominator is 0) render nothing instead of a fake 0pt.

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
}: {
  label: string;
  hint: string;
  rate: OperationsRate;
  delta: number | null;
  deltaLabel: string;
  deltaText: string;
  upIsGood: boolean;
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
    </div>
  );
}

export function OperationsSummary({
  kpis,
  previous,
}: {
  kpis: OperationsKpis;
  previous: OperationsKpis;
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
      key: "p4_covered",
      label: t(($) => $.operations.summary.stage_p4_covered),
      count: funnel.p4Covered,
    },
    {
      key: "ai_delivered",
      label: t(($) => $.operations.summary.stage_ai_delivered),
      count: funnel.aiDelivered,
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
  const passDelta = deltaPoints(kpis.passRate, previous.passRate);
  const shareDelta = deltaPoints(kpis.deliveryShare, previous.deliveryShare);
  const noOutputDelta = deltaPoints(kpis.noOutputRate, previous.noOutputRate);
  return (
    <section className="grid gap-3">
      <div className="grid gap-3 sm:grid-cols-3">
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
          label={t(($) => $.operations.summary.delivery_share)}
          hint={t(($) => $.operations.summary.delivery_share_hint, {
            num: kpis.deliveryShare.numerator,
            den: kpis.deliveryShare.denominator,
          })}
          rate={kpis.deliveryShare}
          delta={shareDelta}
          deltaLabel={deltaLabel}
          deltaText={deltaText(shareDelta)}
          upIsGood
        />
        <RateCard
          label={t(($) => $.operations.summary.no_output_rate)}
          hint={t(($) => $.operations.summary.no_output_rate_hint, {
            num: kpis.noOutputRate.numerator,
            den: kpis.noOutputRate.denominator,
          })}
          rate={kpis.noOutputRate}
          delta={noOutputDelta}
          deltaLabel={deltaLabel}
          deltaText={deltaText(noOutputDelta)}
          upIsGood={false}
        />
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
            <div
              key={stage.key}
              className="grid grid-cols-[88px_minmax(0,1fr)_48px] items-center gap-3"
            >
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
          ))}
        </div>
      </div>
    </section>
  );
}
