// Display formatters shared across console pages. Behavior matches the
// legacy console so numbers read identically during the migration.

export function formatCount(value: number | null | undefined): string {
  if (value === null || value === undefined) return "—";
  const number = Number(value);
  if (!Number.isFinite(number)) return String(value);
  if (number >= 1_000_000) return `${(number / 1_000_000).toFixed(1)}M`;
  if (number >= 10_000) return `${(number / 1_000).toFixed(1)}K`;
  return number.toLocaleString("en-US");
}

export function formatMs(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms)) return "—";
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
  return `${Math.round(ms)}ms`;
}

// formatNanoUSD renders the gateway's exact nano-USD cost unit as dollars.
export function formatNanoUSD(nanoUSD: number | undefined): string {
  const dollars = Number(nanoUSD) / 1_000_000_000;
  if (!Number.isFinite(dollars)) return "—";
  if (dollars === 0) return "$0";
  if (dollars >= 1) return `$${dollars.toFixed(2)}`;
  if (dollars >= 0.001) {
    return `$${dollars.toFixed(4).replace(/0+$/, "").replace(/\.$/, "")}`;
  }
  return `$${dollars.toPrecision(2)}`;
}

export function formatRelativeTime(iso: string | undefined): string {
  if (!iso) return "—";
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return "—";
  const seconds = Math.round((Date.now() - then) / 1000);
  if (seconds < 60) return "just now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  if (days < 30) return `${days}d ago`;
  return new Date(iso).toLocaleDateString();
}

// shortGenerationID keeps catalog generation ULIDs scannable.
export function shortGenerationID(id: string | undefined): string {
  if (!id) return "—";
  return id.length > 22 ? `${id.slice(0, 20)}…` : id;
}

// formatPricePerM converts a per-token price string into a per-million
// display value. Returns null when the price is absent or unparseable so
// callers can render "—" per DESIGN.md (unknown price is never $0).
export function formatPricePerM(perTokenString: string | undefined): string | null {
  if (perTokenString === undefined) return null;
  const perToken = Number.parseFloat(perTokenString);
  if (!Number.isFinite(perToken)) return null;
  const perMillion = perToken * 1_000_000;
  if (perMillion === 0) return "$0";
  if (perMillion >= 100) return `$${perMillion.toFixed(0)}`;
  if (perMillion >= 1) return `$${perMillion.toFixed(2).replace(/\.?0+$/, "")}`;
  return `$${perMillion.toPrecision(2).replace(/\.?0+$/, "")}`;
}

// providerLabel is the one place provider display names resolve. The
// catalog name wins; the raw provider id is the fallback so unknown or
// unfetched slugs still render. Never case-transform the id: casing is
// a catalog fact, not a console guess.
export function providerLabel(
  id: string | undefined,
  name?: string | null,
): string {
  const display = name?.trim();
  if (display) return display;
  return id ?? "—";
}

export function formatContext(tokens: number | null | undefined): string {
  if (!tokens) return "—";
  if (tokens >= 1_000_000) {
    return `${(tokens / 1_000_000).toFixed(tokens % 1_000_000 ? 1 : 0)}M`;
  }
  if (tokens >= 1_000) return `${Math.round(tokens / 1_000)}K`;
  return String(tokens);
}
