"use client";

import { MoveDownRight, MoveUpRight } from "lucide-react";
import { useT } from "../../i18n";
import type { OperationsKpis, OperationsRate } from "../operations-metrics";

// Headline KPI band for the operations page: the north-star rates (pass rate
// split by AI-delivered vs AI-assisted) with a period-over-period delta, plus
// the nested delivery funnel. `previous` carries the KPIs of the equal-length
// window before the selected one; null deltas (either window's denominator is
// 0) render nothing instead of a fake 0pt.

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
  const deliveredDelta = deltaPoints(
    kpis.aiDeliveredPassRate,
    previous.aiDeliveredPassRate,
  );
  const assistedDelta = deltaPoints(
    kpis.aiAssistedPassRate,
    previous.aiAssistedPassRate,
  );
  const shareDelta = deltaPoints(kpis.deliveryShare, previous.deliveryShare);
  const noOutputDelta = deltaPoints(kpis.noOutputRate, previous.noOutputRate);
  const unjudgedDelta = deltaPoints(kpis.unjudgedRate, previous.unjudgedRate);
  return (
    <section className="grid gap-3">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
        <RateCard
          label={t(($) => $.operations.summary.delivered_pass_rate)}
          hint={t(($) => $.operations.summary.delivered_pass_rate_hint, {
            num: kpis.aiDeliveredPassRate.numerator,
            den: kpis.aiDeliveredPassRate.denominator,
          })}
          rate={kpis.aiDeliveredPassRate}
          delta={deliveredDelta}
          deltaLabel={deltaLabel}
          deltaText={deltaText(deliveredDelta)}
          upIsGood
        />
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
        <RateCard
          label={t(($) => $.operations.summary.unjudged_rate)}
          hint={t(($) => $.operations.summary.unjudged_rate_hint, {
            num: kpis.unjudgedRate.numerator,
            den: kpis.unjudgedRate.denominator,
          })}
          rate={kpis.unjudgedRate}
          delta={unjudgedDelta}
          deltaLabel={deltaLabel}
          deltaText={deltaText(unjudgedDelta)}
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
