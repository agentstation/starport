import { useInfiniteQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { type ReactNode } from "react";

import { accessMessage, ApiError, listAuditLog } from "@/lib/api";
import { formatRelativeTime } from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

// The audit log is the durable answer to "who changed what": every admin
// mutation leaves one record naming its actor, its action, its subject, and
// whether the store accepted it. The trail never holds a credential value.

export const Route = createFileRoute("/audit")({
  component: AuditLog,
});

const PAGE_LIMIT = 100;

function AuditLog() {
  const access = useGatewayAccess();

  const trail = useInfiniteQuery({
    queryKey: ["audit"],
    queryFn: ({ pageParam }) =>
      listAuditLog({ limit: PAGE_LIMIT, cursor: pageParam || undefined }),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
    enabled: access,
    retry: false,
  });

  const rows = (trail.data?.pages ?? []).flatMap((page) => page.data ?? []);

  let body: ReactNode;
  if (trail.error) {
    body = (
      <p className="text-base text-text-3">
        {trail.error instanceof ApiError && trail.error.needsKey
          ? accessMessage(trail.error, "admin")
          : `Failed to load the audit log: ${trail.error.message}`}
      </p>
    );
  } else if (trail.isPending) {
    body = <p className="text-base text-text-3">Loading the audit log…</p>;
  } else if (rows.length === 0) {
    body = (
      <p className="text-base text-text-3">
        Nothing is recorded yet. Records appear when an admin mutation lands:
        a key issued, an account changed, a credential stored.
      </p>
    );
  } else {
    body = (
      <div className="flex flex-col gap-3">
        <div className="overflow-x-auto rounded-md border border-border-1 bg-bg-panel">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
                <th className="px-4 py-2.5">Time</th>
                <th className="px-4 py-2.5">Actor</th>
                <th className="px-4 py-2.5">Action</th>
                <th className="px-4 py-2.5">Subject</th>
                <th className="px-4 py-2.5">Outcome</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((record) => (
                <tr
                  key={record.id}
                  data-testid="audit-row"
                  className="border-b border-border-1 last:border-0"
                >
                  <td
                    className="px-4 py-2 text-xs text-text-3"
                    title={record.time}
                  >
                    {formatRelativeTime(record.time)}
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-text-2">
                    {record.actor}
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-text-2">
                    {record.action}
                  </td>
                  <td className="px-4 py-2 font-mono text-xs text-text-2">
                    {record.subject}
                  </td>
                  <td className="px-4 py-2">
                    <span
                      className={
                        record.outcome === "ok"
                          ? "text-xs text-text-3"
                          : "text-xs font-medium text-error"
                      }
                    >
                      {record.outcome}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {trail.hasNextPage && (
          <button
            type="button"
            onClick={() => void trail.fetchNextPage()}
            disabled={trail.isFetchingNextPage}
            className="self-start rounded-md border border-border-1 px-3 py-1.5 text-sm text-text-2 transition-colors duration-150 ease-standard hover:bg-bg-panel disabled:opacity-50"
          >
            {trail.isFetchingNextPage ? "Loading…" : "Load older records"}
          </button>
        )}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-[-0.01em]">Audit log</h1>
        <p className="mt-1 text-sm text-text-3">
          Every admin mutation on this gateway, newest first: who asked, what
          it touched, and how it ended.
        </p>
      </div>
      {body}
    </div>
  );
}
