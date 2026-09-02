// Display formatters shared across console pages. Every number that reaches a
// page passes through here, so the console has one spelling for a count, a
// price, a context length, and a moment in time. No formatter ever emits
// exponent notation: a value is fixed to decimals, never to precision.

const DASH = "—";

// Compact steps in the DESIGN.md style: a lowercase k, then M, then B. The
// steps run largest first so a value that rounds up to the next step moves
// there: 999,950 reads 1.0M and never 1000.0k.
const STEPS: ReadonlyArray<readonly [number, "B" | "M" | "k"]> = [
  [1_000_000_000, "B"],
  [1_000_000, "M"],
  [1_000, "k"],
];

type Decimals = Record<"B" | "M" | "k", number>;

function compact(value: number, floor: number, decimals: Decimals, trim: boolean): string {
  const magnitude = Math.abs(value);
  const sign = value < 0 ? "-" : "";
  if (magnitude >= floor) {
    for (const [size, suffix] of STEPS) {
      const scaled = (magnitude / size).toFixed(decimals[suffix]);
      if (Number(scaled) < 1) continue;
      const digits = trim ? scaled.replace(/\.0+$/, "") : scaled;
      return `${sign}${digits}${suffix}`;
    }
  }
  return Math.round(value).toLocaleString("en-US");
}

// formatCount renders a measured count: exact with separators below 10k, then
// one fixed decimal per step so a column of counts lines up.
export function formatCount(value: number | null | undefined): string {
  if (value === null || value === undefined) return DASH;
  const number = Number(value);
  if (!Number.isFinite(number)) return String(value);
  return compact(number, 10_000, { B: 1, M: 1, k: 1 }, false);
}

// formatContext renders a context window or an output ceiling. A window is a
// published figure, so a whole step drops its decimal: 128k, 1M, 1.5M.
export function formatContext(tokens: number | null | undefined): string {
  if (!tokens || !Number.isFinite(tokens)) return DASH;
  return compact(tokens, 1_000, { B: 1, M: 1, k: 0 }, true);
}

// formatBytes renders a stored size in the unit a reader thinks in. It steps by
// 1024, which is the unit a filesystem and an object store both report.
export function formatBytes(value: number | null | undefined): string {
  if (value === null || value === undefined) return DASH;
  const bytes = Number(value);
  if (!Number.isFinite(bytes)) return String(value);
  if (bytes < 1024) return `${Math.round(bytes)} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let size = bytes / 1024;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size >= 10 ? Math.round(size) : size.toFixed(1)} ${units[unit]}`;
}

export function formatMs(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms)) return DASH;
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
  return `${Math.round(ms)}ms`;
}

// formatUSD is the one dollar spelling, in the DESIGN.md grammar. A whole
// dollar stands bare ($1, $15). A dollar amount keeps cents ($1.20). A cent
// value keeps two decimals and any exact extra digit ($0.22, $0.80, $0.125).
// A sub-cent value keeps three significant digits written out in full
// ($0.000185), because a price per token scaled to a million and a
// nano-dollar cost both live there and neither may read as scientific
// notation. Null means unknown, and an unknown price is never $0.
export function formatUSD(dollars: number | undefined): string | null {
  if (dollars === undefined || !Number.isFinite(dollars)) return null;
  if (dollars === 0) return "$0";
  const sign = dollars < 0 ? "-" : "";
  const magnitude = Math.abs(dollars);
  const decimals =
    magnitude >= 1
      ? 2
      : magnitude >= 0.01
        ? 4
        : Math.min(12, 2 - Math.floor(Math.log10(magnitude)));
  const [whole, fraction = ""] = magnitude.toFixed(decimals).split(".");
  const digits = fraction.replace(/0+$/, "");
  return digits ? `${sign}$${whole}.${digits.padEnd(2, "0")}` : `${sign}$${whole}`;
}

// formatNanoUSD renders the gateway's exact nano-USD cost unit as dollars.
export function formatNanoUSD(nanoUSD: number | undefined): string {
  return formatUSD(Number(nanoUSD) / 1_000_000_000) ?? DASH;
}

function parsePrice(value: string | undefined): number | undefined {
  if (value === undefined) return undefined;
  const price = Number.parseFloat(value);
  return Number.isFinite(price) ? price : undefined;
}

// formatPricePerM converts a per-token price string into a per-million
// display value. Returns null when the price is absent or unparseable so
// callers can render the dash per DESIGN.md (unknown price is never $0).
export function formatPricePerM(perTokenString: string | undefined): string | null {
  const perToken = parsePrice(perTokenString);
  return perToken === undefined ? null : formatUSD(perToken * 1_000_000);
}

// formatPricePerK converts a per-unit price string into a per-thousand
// display value, the magnitude page pricing is quoted in. A raw catalog
// string like "2.58e-05" would otherwise reach the page as scientific
// notation. Returns null when absent or unparseable (unknown is never $0).
export function formatPricePerK(perUnitString: string | undefined): string | null {
  const perUnit = parsePrice(perUnitString);
  return perUnit === undefined ? null : formatUSD(perUnit * 1_000);
}

// formatUnitPrice renders a price that already covers one whole unit: a
// document page, or a rerank search unit. Unlike a token price it needs no
// scaling. Like one it answers null rather than zero when the catalog
// published nothing, because an unknown price is never free.
export function formatUnitPrice(value: string | undefined): string | null {
  return formatUSD(parsePrice(value));
}

// formatPricePair renders a prompt and completion price in the DESIGN.md
// grammar: "$0.22 / M in · $0.88 / M out". One unknown side shows the dash
// beside the known one. Both unknown returns null so a cell can render the
// dash alone.
export function formatPricePair(
  prompt: string | undefined,
  completion: string | undefined,
): string | null {
  const input = formatPricePerM(prompt);
  const output = formatPricePerM(completion);
  if (input === null && output === null) return null;
  return `${input ?? DASH} / M in · ${output ?? DASH} / M out`;
}

// formatWindow names a rate-limit window the way a person would say it. Both
// planes of limits — a key's and the account's — read the same seconds, so the
// two say "req/min" identically rather than each inventing a spelling.
export function formatWindow(seconds: number | undefined): string {
  if (seconds === 60) return "min";
  if (seconds === 3600) return "hr";
  if (seconds === 86400) return "day";
  return `${seconds}s`;
}

// formatRetention says how long a record lives, in the largest whole unit
// that fits. A zero window means the sweep never runs, and the reader
// learns that in words rather than as "0 days".
export function formatRetention(seconds: number | undefined): string {
  if (seconds === undefined) return "unavailable";
  if (seconds <= 0) return "no expiry";
  const units: [number, string][] = [
    [86400, "day"],
    [3600, "hour"],
    [60, "minute"],
  ];
  for (const [size, name] of units) {
    if (seconds % size === 0) {
      const count = seconds / size;
      return `${formatCount(count)} ${name}${count === 1 ? "" : "s"}`;
    }
  }
  return `${formatCount(seconds)} seconds`;
}

// instant parses an ISO stamp into epoch milliseconds, or undefined. A
// zero-value Go time serializes as "0001-01-01T00:00:00Z", which parses to a
// finite pre-epoch instant. An absent stamp renders as absent, never as a
// first-century date.
function instant(iso: string | null | undefined): number | undefined {
  if (!iso) return undefined;
  const then = new Date(iso).getTime();
  return Number.isFinite(then) && then > 0 ? then : undefined;
}

// A future instant reads "in 3h". A budget window resets ahead of now, and
// an elapsed phrase there would read backwards.
export function formatRelativeTime(iso: string | null | undefined): string {
  const then = instant(iso);
  if (then === undefined) return DASH;
  const seconds = Math.round((Date.now() - then) / 1000);
  const distance = Math.abs(seconds);
  if (distance < 60) return "just now";
  const phrase = (count: number, unit: string) =>
    seconds < 0 ? `in ${count}${unit}` : `${count}${unit} ago`;
  const minutes = Math.round(distance / 60);
  if (minutes < 60) return phrase(minutes, "m");
  const hours = Math.round(minutes / 60);
  if (hours < 24) return phrase(hours, "h");
  const days = Math.round(hours / 24);
  if (days < 30) return phrase(days, "d");
  return new Date(then).toLocaleDateString();
}

// utcTooltip is the exact instant behind a relative phrase, in UTC, for a
// title attribute. RelativeTime in components/ui renders both together.
export function utcTooltip(iso: string | null | undefined): string | undefined {
  const then = instant(iso);
  return then === undefined ? undefined : new Date(then).toISOString();
}

// formatUnixTime renders a Unix second stamp as a local date and time. A file
// expiry is a future instant, so an elapsed-time phrase would read backwards
// and an absolute stamp is what an operator plans against.
export function formatUnixTime(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) return DASH;
  const at = new Date(seconds * 1000);
  if (!Number.isFinite(at.getTime())) return DASH;
  return at.toLocaleString();
}

// shortGenerationID keeps catalog generation ULIDs scannable.
export function shortGenerationID(id: string | undefined): string {
  if (!id) return DASH;
  return id.length > 22 ? `${id.slice(0, 20)}…` : id;
}

// providerLabel is the one place provider display names resolve. The
// catalog name wins; the raw provider id is the fallback so unknown or
// unfetched ids still render. Never case-transform the id: casing is
// a catalog fact, not a console guess.
export function providerLabel(
  id: string | undefined,
  name?: string | null,
): string {
  const display = name?.trim();
  if (display) return display;
  return id ?? DASH;
}
