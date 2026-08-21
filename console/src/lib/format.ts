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
