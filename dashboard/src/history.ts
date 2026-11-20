import { useEffect, useState } from "react";
import { ApiError, fetchMetrics, type MetricsPoint } from "./api";
import { useSession } from "./session";

// The ranges a chart can show. The hub keeps an hour of reports, so 60 minutes
// is the longest. The first one is drawn from the live stream; the others from
// the hub's averaged buckets (#330).
export const CHART_RANGES = [10, 60] as const;
export type ChartRange = (typeof CHART_RANGES)[number];

// How many buckets a long chart asks for: enough for the width of a chart, few
// enough that one answer stays small (the hub caps a series at 3600 reports).
export const BUCKETS = 450;

// stepSeconds is the bucket length for a window: about BUCKETS buckets, at
// least the shortest step the hub accepts (1 s).
export const stepSeconds = (windowMs: number): number => Math.max(1, Math.ceil(windowMs / 1000 / BUCKETS));

// How often a long chart asks again: once per bucket, and not more than every
// 10 seconds.
const refreshMs = (windowMs: number): number => Math.max(10, stepSeconds(windowMs)) * 1000;

export interface BucketedSeries {
  points: MetricsPoint[];
  times: number[];
}

// useBucketedSeries loads the last windowMs of an agent as averaged buckets
// and loads them again every bucket. It never mixes in the live stream: a live
// report is one sample and a bucket is a mean, so putting them in one series
// would draw the newest minute at a different resolution (and, with a stale
// bucket, twice). The series is null until the first answer; with enabled
// false nothing is requested.
export function useBucketedSeries(agentId: string, windowMs: number, enabled: boolean): BucketedSeries | null {
  const [loaded, setLoaded] = useState<{ key: string; series: BucketedSeries } | null>(null);
  const session = useSession();
  const expired = session?.expired;
  const key = `${agentId}/${windowMs}`;

  useEffect(() => {
    if (!enabled) return undefined;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = () => {
      fetchMetrics(agentId, undefined, controller.signal, BUCKETS + 50, `${stepSeconds(windowMs)}s`).then(
        (points) => {
          if (controller.signal.aborted) return;
          const newest = points.length > 0 ? Date.parse(points[points.length - 1].at) : 0;
          // Only the window asked for: the hub may hold a little more.
          const from = newest - windowMs;
          const inWindow = points.filter((p) => Date.parse(p.at) >= from);
          setLoaded({ key, series: { points: inWindow, times: inWindow.map((p) => Date.parse(p.at)) } });
          timer = setTimeout(load, refreshMs(windowMs));
        },
        (err: unknown) => {
          if (controller.signal.aborted) return;
          if (err instanceof ApiError && err.status === 401) expired?.();
          timer = setTimeout(load, refreshMs(windowMs)); // keep what is shown, try again
        },
      );
    };
    load();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [agentId, windowMs, enabled, key, expired]);

  return enabled && loaded?.key === key ? loaded.series : null;
}
