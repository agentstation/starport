import type { ActivityRecord } from "@/lib/api";
import { formatCount } from "@/lib/format";

// The usage charts fold the loaded records into time slices of one round
// interval. The interval follows the span, and every slice starts on a
// boundary of that interval in the reader's time zone, so a tick reads 14:00
// and a daily bucket starts at midnight. A fixed slice count produced
// 22.5-hour buckets and ticks at 13:47.

const MINUTE = 60_000;
const HOUR = 3_600_000;
export const DAY = 86_400_000;

export type Interval = { ms: number; label: string };

const INTERVALS: readonly Interval[] = [
  { ms: 5 * MINUTE, label: "5m" },
  { ms: HOUR, label: "1h" },
  { ms: 6 * HOUR, label: "6h" },
  { ms: DAY, label: "1d" },
];

// intervalFor picks the widest interval that still cuts the span into at
// least twelve slices: an hour reads in five-minute slices, a day by the
// hour, a week in quarter days, and a month by the day.
export function intervalFor(spanMs: number): Interval {
  let chosen = INTERVALS[0]!;
  for (const interval of INTERVALS) {
    if (spanMs / interval.ms >= 12) chosen = interval;
  }
  return chosen;
}

// floorTo returns the start of the slice that holds t. Slices align to the
// local midnight, so a daily bucket starts at 00:00 wherever the reader sits.
export function floorTo(t: number, interval: Interval): number {
  const at = new Date(t);
  const midnight = new Date(at.getFullYear(), at.getMonth(), at.getDate()).getTime();
  if (interval.ms >= DAY) return midnight;
  return midnight + Math.floor((t - midnight) / interval.ms) * interval.ms;
}

// advance steps a slice start forward. A day steps by the calendar, because
// a clock change makes one day 23 or 25 hours long.
function advance(start: number, count: number, interval: Interval): number {
  if (interval.ms < DAY) return start + count * interval.ms;
  const at = new Date(start);
  return new Date(at.getFullYear(), at.getMonth(), at.getDate() + count).getTime();
}

function indexOf(t: number, start: number, interval: Interval): number {
  if (interval.ms < DAY) return Math.floor((t - start) / interval.ms);
  return Math.round((floorTo(t, interval) - start) / DAY);
}

// stamp prints a moment at the precision the interval needs: the day alone
// for daily slices, day and clock time for anything shorter.
export function stamp(t: number, interval: Interval): string {
  const at = new Date(t);
  const day = at.toLocaleDateString("en-US", { month: "numeric", day: "numeric" });
  if (interval.ms >= DAY) return day;
  const clock = at.toLocaleTimeString("en-US", { hour12: false, hour: "2-digit", minute: "2-digit" });
  return `${day} ${clock}`;
}

export type Bucket = {
  // The axis tick.
  label: string;
  // The tooltip heading: the slice's start and end.
  title: string;
  start: number;
  ok: number;
  error: number;
  cancelled: number;
  tokens: number;
  // Spend stays in integer nano-dollars until the render boundary, so the
  // chart and the headline sum the same numbers.
  spendNano: number;
  latency: number | null;
};

export type Buckets = {
  buckets: Bucket[];
  interval: Interval;
  start: number;
  end: number;
  // True when the loaded records are the newest slice of a longer window.
  truncated: boolean;
};

export type BucketOptions = {
  now?: number;
  truncated?: boolean;
};

function oldestOf(records: ActivityRecord[]): number {
  let min = Number.POSITIVE_INFINITY;
  for (const record of records) {
    const t = new Date(record.timestamp).getTime();
    if (Number.isFinite(t) && t < min) min = t;
  }
  return min;
}

// bucketize slices the window and folds the records into it. A truncated
// sample starts at its oldest record rather than at the window start, so the
// chart never draws a zero where it holds no data.
export function bucketize(
  records: ActivityRecord[],
  rangeSeconds: number | undefined,
  { now = Date.now(), truncated = false }: BucketOptions = {},
): Buckets {
  const end = now;
  let from: number;
  if (rangeSeconds && !truncated) {
    from = end - rangeSeconds * 1000;
  } else {
    const oldest = oldestOf(records);
    if (!Number.isFinite(oldest)) {
      return { buckets: [], interval: INTERVALS[0]!, start: end, end, truncated };
    }
    from = Math.min(oldest, end - MINUTE);
  }

  const interval = intervalFor(end - from);
  const start = floorTo(from, interval);
  const count = indexOf(end, start, interval) + 1;
  const raw = Array.from({ length: count }, (_, index) => {
    const at = advance(start, index, interval);
    const next = advance(start, index + 1, interval);
    return {
      label: stamp(at, interval),
      title: `${stamp(at, interval)} to ${stamp(next, interval)}`,
      start: at,
      ok: 0,
      error: 0,
      cancelled: 0,
      tokens: 0,
      spendNano: 0,
      latencyTotal: 0,
      latencyCount: 0,
    };
  });

  for (const record of records) {
    const t = new Date(record.timestamp).getTime();
    if (!Number.isFinite(t)) continue;
    const index = indexOf(t, start, interval);
    const bucket = raw[index];
    if (!bucket) continue;
    if (record.status === "ok") bucket.ok++;
    else if (record.status === "error") bucket.error++;
    else bucket.cancelled++;
    bucket.tokens += record.tokens?.total ?? 0;
    bucket.spendNano += record.cost?.nano_usd ?? 0;
    if (Number.isFinite(record.latency_ms)) {
      bucket.latencyTotal += record.latency_ms ?? 0;
      bucket.latencyCount++;
    }
  }

  return {
    buckets: raw.map(({ latencyTotal, latencyCount, ...bucket }) => ({
      ...bucket,
      latency: latencyCount ? latencyTotal / latencyCount : null,
    })),
    interval,
    start,
    end,
    truncated,
  };
}

// describeBuckets is the caption under each chart: the covered range, the
// slice width, and the truncation when the sample is only the newest part.
export function describeBuckets({ start, end, interval, truncated }: Buckets, loaded: number): string {
  const range = `${stamp(start, interval)} to ${stamp(end, interval)} · ${interval.label} buckets`;
  if (!truncated) return range;
  return `Newest ${formatCount(loaded)} requests only · ${range}`;
}
