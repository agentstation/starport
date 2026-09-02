import { useInfiniteQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { type ReactNode } from "react";

import { accessMessage, ApiError, type AuditRecord } from "@/lib/api";
import { queries } from "@/lib/queries";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

import { DataTable, DataTableFooter, dataColumns } from "@/components/ui/DataTable";
import { TableSkeleton } from "@/components/ui/skeleton";
import { RelativeTime } from "@/components/ui/RelativeTime";

// The audit log is the durable answer to "who changed what": every admin
// mutation leaves one record naming its actor, its action, its subject, and
// whether the store accepted it. The trail never holds a credential value.

export const Route = createFileRoute("/audit")({
  component: AuditLog,
});

const PAGE_LIMIT = 100;

const helper = dataColumns<AuditRecord>();
const MONO = "font-mono text-xs text-text-2";
const columns = helper.columns([
  helper.accessor("time", {
    id: "time",
    header: "Time",
    sortFn: "alphanumeric",
    size: 120,
    minSize: 100,
    cell: ({ getValue }) => (
      <RelativeTime iso={getValue()} className="text-xs text-text-3" />
    ),
  }),
  helper.accessor("actor", {
    id: "actor",
    header: "Actor",
    sortFn: "alphanumeric",
    size: 180,
    minSize: 120,
    meta: { className: MONO },
  }),
  helper.accessor("action", {
    id: "action",
    header: "Action",
    sortFn: "alphanumeric",
    size: 200,
    minSize: 120,
    meta: { className: MONO },
  }),
  helper.accessor("subject", {
    id: "subject",
    header: "Subject",
    sortFn: "alphanumeric",
    size: 240,
    minSize: 160,
    meta: { flex: true, className: MONO },
  }),
  helper.accessor("outcome", {
    id: "outcome",
    header: "Outcome",
    sortFn: "alphanumeric",
    size: 110,
    minSize: 90,
    cell: ({ getValue }) => (
      <span
        className={
          getValue() === "ok" ? "text-xs text-text-3" : "text-xs font-medium text-error"
        }
      >
        {getValue()}
      </span>
    ),
  }),
]);

function AuditLog() {
  const access = useGatewayAccess();

  const trail = useInfiniteQuery({
    ...queries.audit(PAGE_LIMIT),
    enabled: access,
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
    body = <TableSkeleton columns={5} rows={8} />;
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
        <DataTable
          aria-label="Audit log"
          columns={columns}
          data={rows}
          getRowId={(record, index) => String(record.id ?? index)}
          rowTestId="audit-row"
        />
        <DataTableFooter
          loaded={rows.length}
          unit={{ one: "record", other: "records" }}
          bound={PAGE_LIMIT}
          hasMore={trail.hasNextPage}
          loading={trail.isFetchingNextPage}
          onLoadMore={() => void trail.fetchNextPage()}
          loadLabel="Load older records"
        />
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
