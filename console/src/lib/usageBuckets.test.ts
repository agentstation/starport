import { describe, expect, it } from "vitest";

import type { ActivityRecord } from "@/lib/api";
import { bucketize, describeBuckets, floorTo, intervalFor } from "@/lib/usageBuckets";

// A fixed local clock keeps the calendar math stable across time zones: the
// midnight the test expects is the runner's own midnight.
const NOW = new Date(2026, 8, 1, 14, 37, 21).getTime();

function record(at: number, extra: Partial<ActivityRecord> = {}): ActivityRecord {
  return {
    request_id: `req_${at}`,
    timestamp: new Date(at).toISOString(),
    model_requested: "acme/test",
    model_used: "acme/test",
    provider: "acme",
    status: "ok",
    ...extra,
  } as ActivityRecord;
}

describe("usage buckets", () => {
  it("a thirty-day range yields daily buckets floored to midnight", () => {
    const series = bucketize([], 30 * 86_400, { now: NOW });
    expect(series.interval.label).toBe("1d");
    expect(series.buckets).toHaveLength(31);
    for (const bucket of series.buckets) {
      const start = new Date(bucket.start);
      expect([start.getHours(), start.getMinutes(), start.getSeconds()]).toEqual([0, 0, 0]);
    }
    expect(series.buckets[0]?.start).toBe(new Date(2026, 7, 2).getTime());
    expect(series.buckets.at(-1)?.start).toBe(new Date(2026, 8, 1).getTime());
    expect(series.truncated).toBe(false);
  });

  it("picks the widest interval that still gives twelve slices", () => {
    expect(intervalFor(3_600_000).label).toBe("5m");
    expect(intervalFor(86_400_000).label).toBe("1h");
    expect(intervalFor(7 * 86_400_000).label).toBe("6h");
    expect(intervalFor(30 * 86_400_000).label).toBe("1d");
  });

  it("a truncated sample starts at the oldest loaded record", () => {
    const oldest = NOW - 5 * 3_600_000 - 17 * 60_000;
    const records = [record(NOW - 60_000), record(oldest)];
    const series = bucketize(records, 7 * 86_400, { now: NOW, truncated: true });
    expect(series.truncated).toBe(true);
    expect(series.start).toBe(floorTo(oldest, series.interval));
    expect(series.buckets[0]?.start).toBe(series.start);
    expect(describeBuckets(series, records.length)).toMatch(/^Newest 2 requests only · /);
  });

  it("sums spend as nano-USD integers and leaves an empty slice without latency", () => {
    const records = [
      record(NOW - 60_000, { cost: { nano_usd: 1_500_000 }, latency_ms: 120 }),
      record(NOW - 90_000, { cost: { nano_usd: 2_500_000 }, latency_ms: 80 }),
    ];
    const series = bucketize(records, 3_600, { now: NOW });
    const last = series.buckets.at(-1)!;
    expect(last.spendNano).toBe(4_000_000);
    expect(last.latency).toBe(100);
    expect(series.buckets[0]?.latency).toBeNull();
    expect(describeBuckets(series, records.length)).toMatch(/5m buckets$/);
  });
});
