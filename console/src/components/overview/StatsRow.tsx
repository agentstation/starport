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
import { queries } from "@/lib/queries";
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

function Stat({
  label,
  value,
  detail,
  trend,
}: {
  label: string;
  value: string;
  detail?: string | null;
  trend?: number[];
}) {
  return (
    <div className="flex flex-col gap-1">
      <div className="text-xs text-text-3">{label}</div>
      <div className="font-mono text-xl font-medium tabular-nums text-text-1">{value}</div>
      <div className="flex h-5 items-center gap-2">
        {trend && <Sparkline points={trend} />}
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
// could not price is surfaced as "without cost", never as zero.
export function StatsRow() {
  const metrics = useQuery({
    ...queries.systemMetrics(),
  });
  const activity = useQuery(queries.adminActivity24h());

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
  const requestTrend = bucketize(records, () => 1);
  const tokenTrend = bucketize(records, (record) => record.tokens?.total ?? 0);
  const errorTrend = bucketize(records, (record) =>
    record.status === "error" ? 1 : 0,
  );

  const total = requests.total ?? 0;
  const errors = requests.errors ?? 0;
  return (
    <Card>
      <div className="grid grid-cols-2 gap-x-6 gap-y-5 md:grid-cols-3 xl:grid-cols-6">
        <Stat
          label="Requests 24h"
          value={formatCount(total)}
          detail={`${formatCount(requests.rate_1min ?? 0)}/min now`}
          trend={requestTrend}
        />
        <Stat
          label="Errors"
          value={formatCount(errors)}
          detail={total ? `${((errors / total) * 100).toFixed(1)}% of total` : null}
          trend={errorTrend}
        />
        <Stat label="Tokens" value={formatCount(tokens.total ?? 0)} trend={tokenTrend} />
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
      <div className="mt-4 flex justify-end border-t border-border-1 pt-3">
        <span className="text-xs text-text-3">
          Per-request detail arrives with the usage page
        </span>
      </div>
    </Card>
  );
}
