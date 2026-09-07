import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { Sparkline } from "@/components/overview/Sparkline";
import { Card } from "@/components/ui/Card";
import { LoadFailed } from "@/components/ui/LoadFailed";
import { StatSkeleton } from "@/components/ui/skeleton";
import {
  ApiError,
  type ActivityRecord,
} from "@/lib/api";
import { ACTIVITY_24H_LIMIT, queries } from "@/lib/queries";
import { formatCount, formatMs, formatNanoUSD } from "@/lib/format";

// bucketize folds activity records into hourly counts for the last day,
// oldest bucket first. The sparkline stays neutral trend data only.
function bucketize(
  records: ActivityRecord[],
  value: (record: ActivityRecord) => number,
): number[] {
  const buckets = new Array<number>(24).fill(0);
  const now = Date.now();
  const hour = 3_600_000;
  for (const record of records) {
    const then = new Date(record.timestamp).getTime();
    if (!Number.isFinite(then)) continue;
    const age = now - then;
    if (age < 0 || age >= 24 * hour) continue;
    const bucket = 23 - Math.floor(age / hour);
    buckets[bucket] = (buckets[bucket] ?? 0) + value(record);
  }
  return buckets;
}

function sum(records: ActivityRecord[], value: (record: ActivityRecord) => number): number {
  let total = 0;
  for (const record of records) total += value(record);
  return total;
}

// describeDelta compares one day against the day before it. A prior day of
// nothing gives no rate to compare against, so the stat stays silent rather
// than claiming an infinite rise.
export function describeDelta(current: number, prior: number): string | null {
  if (prior <= 0) return null;
  const percent = Math.round(((current - prior) / prior) * 100);
  if (percent === 0) return "±0%";
  return `${percent > 0 ? "+" : "−"}${Math.abs(percent)}%`;
}

function Stat({
  label,
  value,
  detail,
  trend,
  max,
  delta,
}: {
  label: string;
  value: string;
  detail?: string | null;
  trend?: number[];
  max?: number;
  delta?: string | null;
}) {
  return (
    <div className="flex flex-col gap-1">
      <div className="text-xs text-text-3">{label}</div>
      <div className="flex items-baseline gap-2">
        <span className="font-mono text-xl font-medium tabular-nums text-text-1">{value}</span>
        {delta && (
          <span
            className="text-xs tabular-nums text-text-3"
            title="Against the 24 hours before this window"
          >
            {delta}
          </span>
        )}
      </div>
      <div className="flex h-5 items-center gap-2">
        {trend && <Sparkline points={trend} max={max} />}
        {detail && <span className="text-xs text-text-3">{detail}</span>}
      </div>
    </div>
  );
}

function LockedCard({ children }: { children: ReactNode }) {
  return (
    <Card>
      <p className="text-base text-text-3">{children}</p>
    </Card>
  );
}

// StatsRow shows the last 24 hours of recorded traffic. Spend the gateway
// could not price is surfaced as "without cost", never as zero. The
// sparklines and the day-over-day deltas come from a bounded sample of the
// request log. When the sample fills its bound, the newest requests alone
// would draw a trend that contradicts the headline above it, so the card
// drops the trend and says why.
export function StatsRow() {
  const metrics = useQuery({
    ...queries.systemMetrics(),
  });
  const activity = useQuery(queries.adminActivity24h());
  const prior = useQuery(queries.adminActivityPrior24h());

  if (metrics.isPending) return <StatSkeleton />;
  if (metrics.error) {
    if (metrics.error instanceof ApiError && metrics.error.needsKey) {
      return <LockedCard>Gateway metrics need an admin-scoped key.</LockedCard>;
    }
    return (
      <LoadFailed
        what="gateway metrics"
        error={metrics.error}
        onRetry={() => void metrics.refetch()}
      />
    );
  }

  const requests = metrics.data?.requests ?? {};
  const latency = metrics.data?.latency ?? {};
  const overhead = metrics.data?.overhead ?? {};
  const tokens = metrics.data?.tokens ?? {};
  const spend = metrics.data?.spend ?? {};
  const records = activity.data?.data ?? [];
  const priorRecords = prior.data?.data ?? [];
  const capped = records.length >= ACTIVITY_24H_LIMIT;
  const comparable =
    Boolean(activity.data && prior.data) && !capped && priorRecords.length < ACTIVITY_24H_LIMIT;

  const countTokens = (record: ActivityRecord) => record.tokens?.total ?? 0;
  const countErrors = (record: ActivityRecord) => (record.status === "error" ? 1 : 0);
  const requestTrend = capped ? undefined : bucketize(records, () => 1);
  const tokenTrend = capped ? undefined : bucketize(records, countTokens);
  const errorTrend = capped ? undefined : bucketize(records, countErrors);
  const requestPeak = requestTrend ? Math.max(...requestTrend) : undefined;
  const delta = (value: (record: ActivityRecord) => number) =>
    comparable ? describeDelta(sum(records, value), sum(priorRecords, value)) : null;

  const total = requests.total ?? 0;
  const errors = requests.errors ?? 0;
  return (
    <Card>
      <div className="grid grid-cols-2 gap-x-6 gap-y-5 @2xl:grid-cols-3 @4xl:grid-cols-6">
        <Stat
          label="Requests 24h"
          value={formatCount(total)}
          detail={`${formatCount(requests.rate_1min ?? 0)}/min now`}
          trend={requestTrend}
          delta={delta(() => 1)}
        />
        <Stat
          label="Errors"
          value={formatCount(errors)}
          detail={total ? `${((errors / total) * 100).toFixed(1)}% of total` : null}
          trend={errorTrend}
          max={requestPeak}
          delta={delta(countErrors)}
        />
        <Stat
          label="Tokens"
          value={formatCount(tokens.total ?? 0)}
          trend={tokenTrend}
          delta={delta(countTokens)}
        />
        <Stat
          label="Spend"
          value={
            spend.nano_usd || !spend.requests_without_cost
              ? formatNanoUSD(spend.nano_usd ?? 0)
              : "—"
          }
          detail={
            spend.requests_without_cost
              ? `${formatCount(spend.requests_without_cost)} without cost`
              : null
          }
        />
        <Stat
          label="Latency p50"
          value={formatMs(latency.p50)}
          detail={`p95 ${formatMs(latency.p95)}`}
        />
        <Stat
          label="Starport overhead p50"
          value={formatMs(overhead.p50)}
          detail={`p99 ${formatMs(overhead.p99)}`}
        />
      </div>
      {capped && (
        <div className="mt-4 flex justify-end border-t border-border-1 pt-3">
          <span className="text-xs text-text-3">
            {`Trends hidden: the sample holds only the newest ${formatCount(ACTIVITY_24H_LIMIT)} requests. Usage has the full window.`}
          </span>
        </div>
      )}
    </Card>
  );
}
