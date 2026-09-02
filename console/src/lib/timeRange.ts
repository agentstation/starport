// The time window vocabulary the listing pages share. A range names how far
// back a listing reaches from now; "all" places no lower bound. The pages
// keep the selected range in the address, so a bookmark carries it.
export const RANGE_SECONDS: Record<string, number> = {
  "1h": 3600,
  "24h": 86400,
  "7d": 604800,
  "30d": 2592000,
};

export const RANGE_LABELS: Record<string, string> = {
  "1h": "last hour",
  "24h": "last 24 hours",
  "7d": "last 7 days",
  "30d": "last 30 days",
  all: "all time",
};

// rangeOf keeps a search value only when it names a known range.
export function rangeOf(value: unknown): string | undefined {
  return typeof value === "string" && value in RANGE_LABELS ? value : undefined;
}

// sinceOf reads the lower bound a range places, as an ISO instant, or
// undefined for "all".
export function sinceOf(range: string, now = Date.now()): string | undefined {
  const seconds = RANGE_SECONDS[range];
  return seconds ? new Date(now - seconds * 1000).toISOString() : undefined;
}
