import { useQuery } from "@tanstack/react-query";

import { LoadFailed } from "@/components/ui/LoadFailed";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { ApiError, type ProviderUsage } from "@/lib/api";
import { formatCount, formatNanoUSD } from "@/lib/format";
import { queries } from "@/lib/queries";

// ProviderSpendPanel shows where one account's spend went: its recorded
// requests in the gateway's rollup window, grouped by the provider that
// served them. The rollup walks a bounded number of records, so a window
// it could not finish says so under the table rather than passing a short
// count off as the whole.
export function ProviderSpendPanel({ accountId }: { accountId: string }) {
  const usage = useQuery(queries.accountProviderUsage(accountId));

  let body: React.ReactNode;
  if (usage.isPending) {
    body = <span className="text-sm text-text-3">Reading provider spend…</span>;
  } else if (usage.error) {
    body =
      usage.error instanceof ApiError && usage.error.needsKey ? (
        <span className="text-sm text-text-3">
          Reading provider spend needs an admin-scoped key.
        </span>
      ) : (
        <LoadFailed
          what="provider spend"
          error={usage.error}
          onRetry={() => void usage.refetch()}
        />
      );
  } else {
    const rows = [...(usage.data.data ?? [])].sort(
      (a, b) => b.spend_nano_usd - a.spend_nano_usd || a.provider.localeCompare(b.provider),
    );
    const withoutCost = rows.reduce((sum, row) => sum + (row.requests_without_cost ?? 0), 0);
    body =
      rows.length === 0 ? (
        <span className="text-sm text-text-3">
          No request reached a provider for this account in the window.
        </span>
      ) : (
        <div className="overflow-x-auto rounded-sm border border-border-1">
          <table className="w-full text-sm" aria-label="Provider spend">
            <thead className="text-xs text-text-3">
              <tr className="border-b border-border-1">
                <th scope="col" className="px-3 py-2 text-left font-medium">Provider</th>
                <th scope="col" className="px-3 py-2 text-right font-medium">Requests</th>
                <th scope="col" className="px-3 py-2 text-right font-medium">Errors</th>
                <th scope="col" className="px-3 py-2 text-right font-medium">Tokens</th>
                <th scope="col" className="px-3 py-2 text-right font-medium">Spend</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <ProviderRow key={row.provider} row={row} />
              ))}
            </tbody>
          </table>
        </div>
      );
    const notes: string[] = [];
    if (usage.data.truncated) {
      notes.push("The rollup stopped at its record bound, so these rows understate the window.");
    }
    if (withoutCost > 0) {
      notes.push(`${formatCount(withoutCost)} requests without a price are not in the spend.`);
    }
    body = (
      <>
        {body}
        <span className="text-xs text-text-4">
          {usage.data.window?.since ? (
            <>
              Requests since <RelativeTime iso={usage.data.window.since} />.
            </>
          ) : null}
          {notes.map((note) => (
            <span key={note} className="block">
              {note}
            </span>
          ))}
        </span>
      </>
    );
  }

  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-xs font-medium text-text-2">Provider spend</span>
      {body}
    </div>
  );
}

function ProviderRow({ row }: { row: ProviderUsage }) {
  return (
    <tr className="border-b border-border-1 last:border-b-0">
      <td className="px-3 py-1.5 font-mono text-xs text-text-1">{row.provider}</td>
      <td className="px-3 py-1.5 text-right tabular-nums text-text-2">
        {formatCount(row.requests)}
      </td>
      <td
        className={`px-3 py-1.5 text-right tabular-nums ${row.errors > 0 ? "text-error" : "text-text-3"}`}
      >
        {formatCount(row.errors)}
      </td>
      <td className="px-3 py-1.5 text-right tabular-nums text-text-2">
        {formatCount(row.tokens?.total ?? 0)}
      </td>
      <td className="px-3 py-1.5 text-right tabular-nums text-text-1">
        {formatNanoUSD(row.spend_nano_usd)}
      </td>
    </tr>
  );
}
