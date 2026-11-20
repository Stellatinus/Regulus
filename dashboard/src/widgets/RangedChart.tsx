import { useState } from "react";
import { Chart, type ChartSeries } from "../Chart";
import type { MetricsPoint } from "../api";
import { CHART_RANGES, useBucketedSeries, type ChartRange } from "../history";
import { useT } from "../i18n";

interface Props {
  agentId: string;
  // The live window (the last 10 minutes, one point per report).
  live: { points: MetricsPoint[]; times: number[] };
  title: string;
  // The series for a window of reports: the live one or an averaged one.
  series: (points: MetricsPoint[]) => ChartSeries[];
  max?: number;
  minTop?: number;
  formatValue: (value: number) => string;
}

// RangedChart is a Chart with a choice of range: 10 minutes come from the live
// stream, longer ranges from the hub's averaged buckets (?step=, #330).
export function RangedChart({ agentId, live, title, series, max, minTop, formatValue }: Props) {
  const t = useT();
  const [range, setRange] = useState<ChartRange>(CHART_RANGES[0]);
  const long = range !== CHART_RANGES[0];
  const bucketed = useBucketedSeries(agentId, range * 60_000, long);
  const shown = long ? bucketed : live;

  return (
    <>
      <label className="chart-range">
        <span className="visually-hidden">{t("servers.chart.range")}</span>
        <select value={range} onChange={(e) => setRange(Number(e.target.value) as ChartRange)}>
          {CHART_RANGES.map((r) => (
            <option key={r} value={r}>
              {t(r === 10 ? "servers.chart.range10" : "servers.chart.range60")}
            </option>
          ))}
        </select>
      </label>
      {shown ? (
        <Chart title={title} times={shown.times} series={series(shown.points)} max={max} minTop={minTop} formatValue={formatValue} windowMs={range * 60_000} />
      ) : (
        <p className="chart-empty">{t("servers.chart.collecting")}</p>
      )}
    </>
  );
}
