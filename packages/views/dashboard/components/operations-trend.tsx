"use client";

import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@multica/ui/components/ui/chart";
import { useT } from "../../i18n";
import type { OperationsTrendPoint } from "../operations-metrics";

// Weekly trend of the three headline rates. Weeks with no denominator carry
// null and render as gaps (connectNulls is off) — a week with no reviews has
// no pass rate, not a 0% one. The in-progress week is annotated via the shared
// `weekly.partial_label` copy in the tooltip.
export function OperationsTrend({ data }: { data: OperationsTrendPoint[] }) {
  const { t } = useT("usage");
  const config = {
    passRate: {
      label: t(($) => $.operations.trend.pass_rate),
      color: "var(--chart-1)",
    },
    deliveryShare: {
      label: t(($) => $.operations.trend.delivery_share),
      color: "var(--chart-2)",
    },
    noOutputRate: {
      label: t(($) => $.operations.trend.no_output_rate),
      color: "var(--chart-5)",
    },
  } satisfies ChartConfig;
  return (
    <section className="rounded-lg border bg-card">
      <div className="flex items-center justify-between gap-3 border-b px-4 py-3">
        <h2 className="text-sm font-medium">
          {t(($) => $.operations.trend.title)}
        </h2>
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          {(Object.keys(config) as Array<keyof typeof config>).map((key) => (
            <span key={key} className="flex items-center gap-1.5">
              <span
                className="h-0.5 w-3 rounded-full"
                style={{ background: config[key].color }}
              />
              {config[key].label}
            </span>
          ))}
        </div>
      </div>
      <div className="p-4">
        <ChartContainer config={config} className="aspect-[4/1] w-full">
          <LineChart data={data} margin={{ left: 0, right: 12, top: 4, bottom: 0 }}>
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey="label"
              tickLine={false}
              axisLine={false}
              tickMargin={8}
              interval="preserveStartEnd"
            />
            <YAxis
              tickLine={false}
              axisLine={false}
              tickMargin={8}
              width={44}
              domain={[0, 100]}
              tickFormatter={(v: number) => `${v}%`}
            />
            <ChartTooltip
              content={
                <ChartTooltipContent
                  labelKey="rangeLabel"
                  labelFormatter={(_label, payload) => {
                    const row = payload[0]?.payload as
                      | OperationsTrendPoint
                      | undefined;
                    if (!row) return "";
                    return row.partial
                      ? t(($) => $.weekly.partial_label, {
                          range: row.rangeLabel,
                          covered: row.daysCovered,
                        })
                      : row.rangeLabel;
                  }}
                  formatter={(value, name) => `${value}% ${name}`}
                />
              }
            />
            <Line
              dataKey="passRate"
              stroke="var(--color-passRate)"
              strokeWidth={2}
              type="monotone"
              dot={false}
              connectNulls={false}
            />
            <Line
              dataKey="deliveryShare"
              stroke="var(--color-deliveryShare)"
              strokeWidth={2}
              type="monotone"
              dot={false}
              connectNulls={false}
            />
            <Line
              dataKey="noOutputRate"
              stroke="var(--color-noOutputRate)"
              strokeWidth={2}
              type="monotone"
              dot={false}
              connectNulls={false}
            />
          </LineChart>
        </ChartContainer>
      </div>
    </section>
  );
}
