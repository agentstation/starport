import type { ActivityRecord } from "@/lib/api";
import { formatCount } from "@/lib/format";

// --- Served credential source: which credential plane actually paid for the
// requests this provider handled in the window.
//
// The provider credential card beside this one reports what is configured.
// This one reports what was spent, and the difference is the point: an
// operator who applied a gateway credential expecting accounts to bring
// their own can see, here, that every request is still drawing on the
// deployment's money.
//
// The sources are the gateway's own (`internal/providers/keyring`), read from
// the activity log's `credential_source`; the labels wear the console's
// ownership words — the two shared sources are the operator's money, the
// account's own is not. A record written before the gateway recorded planes
// carries none, and is counted as unrecorded rather than folded into a plane
// it may not have used.

const SOURCE_LABELS: Record<string, string> = {
  environment: "Shared · environment",
  gateway: "Shared · stored",
  byok: "Account's own",
  anonymous: "No credential",
};

const SOURCE_ORDER = ["environment", "gateway", "byok", "anonymous"];

const UNRECORDED = "unrecorded";

export function credentialSourceBreakdown(
  records: ActivityRecord[],
): Array<[string, number]> {
  const counts = new Map<string, number>();
  for (const record of records) {
    const source = record.credential_source || UNRECORDED;
    counts.set(source, (counts.get(source) ?? 0) + 1);
  }
  return [...counts.entries()].sort(
    ([a], [b]) =>
      (SOURCE_ORDER.indexOf(a) + 1 || SOURCE_ORDER.length + 1) -
      (SOURCE_ORDER.indexOf(b) + 1 || SOURCE_ORDER.length + 1),
  );
}

export function sourceLabel(source: string): string {
  return SOURCE_LABELS[source] ?? "Unrecorded";
}

export function ServedCredentialPanel({
  records,
}: {
  records: ActivityRecord[] | undefined;
}) {
  const breakdown = records ? credentialSourceBreakdown(records) : [];
  const total = breakdown.reduce((sum, [, count]) => sum + count, 0);
  return (
    <section
      data-testid="served-credential-panel"
      className="flex flex-col gap-2 rounded-md border border-border-1 bg-bg-panel p-4"
    >
      <h2 className="text-xs font-medium uppercase tracking-wide text-text-3">
        Paid by (1h)
      </h2>
      {total === 0 ? (
        <p className="text-sm text-text-3">
          No requests through this provider in the last hour.
        </p>
      ) : (
        <ul className="flex flex-col gap-1.5">
          {breakdown.map(([source, count]) => (
            <li key={source} className="flex flex-col gap-1">
              <div className="flex items-baseline justify-between gap-3 text-sm">
                <span className="truncate text-text-2">
                  {sourceLabel(source)}
                </span>
                <span className="shrink-0 tabular-nums text-text-1">
                  {formatCount(count)}
                </span>
              </div>
              <div className="h-1 overflow-hidden rounded-xs bg-bg-raised">
                <div
                  className="h-full bg-accent-link"
                  style={{ width: `${(count / total) * 100}%` }}
                />
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
