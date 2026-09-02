import { keepPreviousData, useInfiniteQuery } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { ScrollText } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";

import { accessMessage, ApiError, type AuditFilters, type AuditRecord } from "@/lib/api";
import { queries } from "@/lib/queries";
import { RANGE_LABELS, rangeOf, sinceOf } from "@/lib/timeRange";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

import { DataTable, DataTableFooter, dataColumns } from "@/components/ui/DataTable";
import { GhostButton, INPUT_CLASS } from "@/components/ui/Form";
import { Pill } from "@/components/ui/Pill";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Select } from "@/components/ui/Select";
import { TableSkeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";

// The audit log is the durable answer to "who changed what": every admin
// mutation leaves one record naming its actor, its action, its subject, the
// request that carried it, and whether the store accepted it. The trail
// never holds a credential value.
//
// The filters live in the address, so an investigation is a link: a
// reviewer hands over "this actor, this action, before this instant" and
// the page opens on it.

type AuditSearch = {
  actor?: string;
  action?: string;
  range?: string;
  // until is an ISO instant that bounds the window from above, so a reader
  // can walk back from the moment something went wrong.
  until?: string;
};

// DEFAULT_RANGE keeps the first paint to the last 30 days. An audit trail
// is sparse, and a month covers a review cycle; "all time" stays one
// select away.
const DEFAULT_RANGE = "30d";

export const Route = createFileRoute("/audit")({
  component: AuditLog,
  validateSearch: (search: Record<string, unknown>): AuditSearch => {
    const str = (value: unknown) =>
      typeof value === "string" && value !== "" ? value : undefined;
    const until = str(search.until);
    return {
      actor: str(search.actor),
      action: str(search.action),
      range: rangeOf(search.range),
      until: until && !Number.isNaN(Date.parse(until)) ? until : undefined,
    };
  },
});

const PAGE_LIMIT = 100;

// actorName reads the readable half of an actor: "key:ci-deployer" names
// the key, "user:auth0|5f7c" names the identity subject, and "anonymous"
// stands alone. The raw actor stays one hover away.
function actorName(actor: string): string {
  const colon = actor.indexOf(":");
  return colon > 0 && colon < actor.length - 1 ? actor.slice(colon + 1) : actor;
}

function ActorCell({ actor }: { actor: string }) {
  const name = actorName(actor);
  if (name === actor) {
    return <span className="font-mono text-xs text-text-2">{actor}</span>;
  }
  return (
    <Tooltip>
      <TooltipTrigger
        render={<span tabIndex={0} className="cursor-default font-mono text-xs text-text-2" />}
      >
        {name}
      </TooltipTrigger>
      <TooltipContent>
        <span className="font-mono">{actor}</span>
      </TooltipContent>
    </Tooltip>
  );
}

// toLocalInput renders an ISO instant in the form a datetime-local input
// reads, in the reader's zone and to the minute.
function toLocalInput(iso: string | undefined): string {
  if (!iso) return "";
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}T${pad(at.getHours())}:${pad(at.getMinutes())}`;
}

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
    cell: ({ getValue }) => <ActorCell actor={getValue() ?? ""} />,
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
  helper.accessor("request_id", {
    id: "request",
    header: "Request",
    sortFn: "alphanumeric",
    size: 200,
    minSize: 140,
    cell: ({ getValue }) => {
      const id = getValue();
      if (!id) return <span className="text-xs text-text-4">{"\u2014"}</span>;
      return (
        <Link
          to="/usage"
          search={{ request: id, range: "all" }}
          className="block truncate font-mono text-xs text-accent hover:underline"
          title={`Open request ${id} on the usage page`}
        >
          {id}
        </Link>
      );
    },
  }),
  helper.accessor("outcome", {
    id: "outcome",
    header: "Outcome",
    sortFn: "alphanumeric",
    size: 110,
    minSize: 90,
    cell: ({ getValue }) => {
      const outcome = getValue() ?? "";
      if (outcome === "ok") return <Pill tone="success">ok</Pill>;
      if (outcome === "error") return <Pill tone="error">refused</Pill>;
      return <Pill tone="neutral">{outcome || "\u2014"}</Pill>;
    },
  }),
]);

function AuditLog() {
  const access = useGatewayAccess();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const range = search.range ?? DEFAULT_RANGE;

  const setSearch = (patch: Partial<AuditSearch>) => {
    void navigate({
      search: (previous: AuditSearch) => ({ ...previous, ...patch }),
      replace: true,
    });
  };

  // Text filters debounce into the address so the listing refires once per
  // pause, not per keystroke.
  const [actorDraft, setActorDraft] = useState(search.actor ?? "");
  const [actionDraft, setActionDraft] = useState(search.action ?? "");
  useEffect(() => {
    const timer = setTimeout(() => {
      void navigate({
        search: (previous: AuditSearch) => ({
          ...previous,
          actor: actorDraft.trim() || undefined,
          action: actionDraft.trim() || undefined,
        }),
        replace: true,
      });
    }, 250);
    return () => clearTimeout(timer);
  }, [actorDraft, actionDraft, navigate]);

  // The lower bound is read once per filter change, so a refetch of the
  // same listing reuses its key instead of moving the window every render.
  const sinceISO = useMemo(
    () => sinceOf(range),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [range, search.actor, search.action, search.until],
  );
  const filters: AuditFilters = {
    actor: search.actor,
    action: search.action,
    since: sinceISO,
    until: search.until,
    limit: PAGE_LIMIT,
  };

  const trail = useInfiniteQuery({
    ...queries.audit(filters),
    enabled: access,
    placeholderData: keepPreviousData,
  });

  const rows = (trail.data?.pages ?? []).flatMap((page) => page.data ?? []);
  const hasFilters = Boolean(search.actor || search.action || search.until);
  const bounded = range !== "all";

  const clearFilters = () => {
    setActorDraft("");
    setActionDraft("");
    setSearch({ actor: undefined, action: undefined, until: undefined });
  };

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
    body = <TableSkeleton columns={6} rows={8} />;
  } else if (rows.length === 0) {
    body = (
      <div className="flex flex-col items-center gap-3 rounded-md border border-border-1 bg-bg-panel px-6 py-14 text-center">
        <ScrollText className="size-6 text-text-4" aria-hidden="true" />
        {hasFilters || bounded ? (
          <>
            <p className="text-base text-text-3">
              {hasFilters
                ? `No records in the ${RANGE_LABELS[range]} match these filters.`
                : `Nothing was recorded in the ${RANGE_LABELS[range]}.`}
            </p>
            <div className="flex flex-wrap justify-center gap-2">
              {bounded && (
                <GhostButton onClick={() => setSearch({ range: "all" })}>Show all time</GhostButton>
              )}
              {hasFilters && <GhostButton onClick={clearFilters}>Clear filters</GhostButton>}
            </div>
          </>
        ) : (
          <p className="text-base text-text-3">
            Nothing is recorded yet. Records appear when an admin mutation lands:
            a key issued, an account changed, a credential stored.
          </p>
        )}
      </div>
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
          it touched, which request carried it, and how it ended.
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <input
          type="search"
          placeholder="actor"
          aria-label="Filter by actor"
          value={actorDraft}
          onChange={(event) => setActorDraft(event.target.value)}
          className={`${INPUT_CLASS} w-44 font-mono`}
        />
        <input
          type="search"
          placeholder="action, like key.create"
          aria-label="Filter by action"
          value={actionDraft}
          onChange={(event) => setActionDraft(event.target.value)}
          className={`${INPUT_CLASS} w-60 font-mono`}
        />
        <Select
          uiSize="sm"
          aria-label="Time range"
          value={range}
          onChange={(event) =>
            setSearch({
              range: event.target.value === DEFAULT_RANGE ? undefined : event.target.value,
            })
          }
        >
          {Object.entries(RANGE_LABELS).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </Select>
        <label className="flex items-center gap-1.5 text-xs text-text-3">
          until
          <input
            type="datetime-local"
            aria-label="Until"
            value={toLocalInput(search.until)}
            onChange={(event) => {
              const value = event.target.value;
              setSearch({ until: value ? new Date(value).toISOString() : undefined });
            }}
            className={`${INPUT_CLASS} w-52`}
          />
        </label>
        {hasFilters && (
          <GhostButton onClick={clearFilters} className="text-xs">
            Clear filters
          </GhostButton>
        )}
      </div>

      {body}
    </div>
  );
}
