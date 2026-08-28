import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useWindowVirtualizer } from "@tanstack/react-virtual";
import { BarChart3, Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { AXIS_TICK, CHART, ChartCard, ChartTip } from "@/components/ui/Chart";
import { Select } from "@/components/ui/Select";
import { SidePanel } from "@/components/ui/SidePanel";
import {
  ApiError,
  listActivity,
  listAdminActivity,
  listProviderCatalog,
  type ActivityFilters,
  type ActivityRecord,
} from "@/lib/api";
import {
  formatCount,
  formatMs,
  formatNanoUSD,
  formatRelativeTime,
  providerLabel,
} from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

const PAGE_LIMIT = 200;
// Pages fetched eagerly so the charts and counts cover the window, not
// just the first screen. Beyond this the counts report themselves as
// partial with a "+" suffix.
const AUTO_PAGES = 5;
const BUCKETS = 32;
const ROW_HEIGHT = 40;

const RANGE_SECONDS: Record<string, number> = {
  "1h": 3600,
  "24h": 86400,
  "7d": 604800,
  "30d": 2592000,
};
const RANGE_LABELS: Record<string, string> = {
  "1h": "last hour",
  "24h": "last 24 hours",
  "7d": "last 7 days",
  "30d": "last 30 days",
  all: "all time",
};
const STATUSES = ["ok", "error", "cancelled"] as const;

// Recorded cost-unavailability reasons: an absent cost always carries
// its reason, never a silent zero.
const COST_REASONS: Record<string, string> = {
  no_pricing: "no pricing",
  no_route: "no route",
  no_usage: "no usage",
  // The turn produced a picture or a spoken answer the offering does not
  // price. Its token half is priced, and showing that half alone would read
  // as the bill, so the whole cost is withheld and named here instead.
  media_unpriced: "media unpriced",
};

type UsageSearch = {
  model?: string;
  provider?: string;
  key?: string;
  status?: string;
  range?: string;
};

export const Route = createFileRoute("/usage")({
  component: UsagePage,
  validateSearch: (search: Record<string, unknown>): UsageSearch => {
    const str = (value: unknown) =>
      typeof value === "string" && value !== "" ? value : undefined;
    const status = str(search.status);
    const range = str(search.range);
    return {
      model: str(search.model),
      provider: str(search.provider),
      key: str(search.key),
      status: status && (STATUSES as readonly string[]).includes(status) ? status : undefined,
      range: range && range in RANGE_LABELS ? range : undefined,
    };
  },
});

// --- Scope: admin keys read the cross-key listing, other keys fall back
// to their own activity, and the page reports the distinction.

type Scope = "admin" | "own" | "locked" | "unconfigured";

function useActivityScope(enabled: boolean) {
  return useQuery<Scope>({
    queryKey: ["activity-scope"],
    queryFn: async () => {
      try {
        await listAdminActivity({ limit: 1 });
        return "admin";
      } catch (error) {
        if (error instanceof ApiError && error.status === 503) return "unconfigured";
        if (!(error instanceof ApiError) || !error.needsKey) throw error;
      }
      try {
        await listActivity({ limit: 1 });
        return "own";
      } catch (error) {
        if (error instanceof ApiError && error.status === 503) return "unconfigured";
        if (error instanceof ApiError && error.needsKey) return "locked";
        throw error;
      }
    },
    enabled,
    retry: false,
  });
}

// --- Chart buckets: fixed-width time slices over the selected window,
// aggregated from the loaded records only.

type Bucket = {
  label: string;
  iso: string;
  ok: number;
  error: number;
  cancelled: number;
  tokens: number;
  spend: number;
  latency: number | null;
};

function bucketize(records: ActivityRecord[], rangeSeconds: number | undefined): Bucket[] {
  const end = Date.now();
  let start: number;
  if (rangeSeconds) {
    start = end - rangeSeconds * 1000;
  } else {
    let min = Number.POSITIVE_INFINITY;
    for (const record of records) {
      const t = new Date(record.timestamp).getTime();
      if (Number.isFinite(t) && t < min) min = t;
    }
    if (!Number.isFinite(min)) return [];
    start = Math.min(min, end - 60_000);
  }

  const width = (end - start) / BUCKETS;
  const shortWindow = end - start <= 26 * 3600 * 1000;
  const raw = Array.from({ length: BUCKETS }, (_, index) => {
    const at = new Date(start + index * width);
    return {
      label: shortWindow
        ? at.toLocaleTimeString("en-US", { hour12: false, hour: "2-digit", minute: "2-digit" })
        : at.toLocaleDateString("en-US", { month: "numeric", day: "numeric" }),
      iso: at.toISOString(),
      ok: 0,
      error: 0,
      cancelled: 0,
      tokens: 0,
      spend: 0,
      latencyTotal: 0,
      latencyCount: 0,
    };
  });

  for (const record of records) {
    const t = new Date(record.timestamp).getTime();
    if (!Number.isFinite(t)) continue;
    let index = Math.floor((t - start) / width);
    if (index === BUCKETS) index = BUCKETS - 1;
    if (index < 0 || index >= BUCKETS) continue;
    const bucket = raw[index]!;
    if (record.status === "ok") bucket.ok++;
    else if (record.status === "error") bucket.error++;
    else bucket.cancelled++;
    bucket.tokens += record.tokens?.total ?? 0;
    bucket.spend += (record.cost?.nano_usd ?? 0) / 1_000_000_000;
    if (Number.isFinite(record.latency_ms)) {
      bucket.latencyTotal += record.latency_ms ?? 0;
      bucket.latencyCount++;
    }
  }

  return raw.map(({ latencyTotal, latencyCount, ...bucket }) => ({
    ...bucket,
    latency: latencyCount ? latencyTotal / latencyCount : null,
  }));
}

// --- Cells ---

function StatusPill({ record }: { record: ActivityRecord }) {
  const status = record.status ?? "—";
  const title = record.status_code ? `HTTP ${record.status_code}` : undefined;
  if (status === "ok") {
    return (
      <span className="inline-flex h-5 items-center rounded-full bg-success-tint px-2 text-xs font-medium text-success" title={title}>
        ok
      </span>
    );
  }
  if (status === "error") {
    // Name the two enforcement rejections instead of a bare code.
    let label =
      record.error_class?.replaceAll("_", " ") ?? "error";
    if (record.status_code === 402) label = "budget exhausted";
    if (record.status_code === 429) label = "rate limited";
    // The pill can still run out of column; the tooltip always carries
    // the whole phrase so a clipped label never reads as a shorter one.
    return (
      <span
        className="inline-flex h-5 max-w-full items-center rounded-full bg-error-tint px-2 text-xs font-medium text-error"
        title={title ? `${label} · ${title}` : label}
      >
        <span className="truncate">{label}</span>
      </span>
    );
  }
  return (
    <span className="inline-flex h-5 items-center rounded-full bg-bg-raised px-2 text-xs font-medium text-text-3" title={title}>
      {status}
    </span>
  );
}

function CostCell({ record }: { record: ActivityRecord }) {
  if (record.cost) {
    return (
      <span className="font-mono text-xs tabular-nums text-text-2">
        {formatNanoUSD(record.cost.nano_usd)}
      </span>
    );
  }
  const reason =
    COST_REASONS[record.cost_unavailable_reason ?? ""] ?? record.cost_unavailable_reason;
  if (!reason) return <span className="text-text-4">—</span>;
  return (
    <span
      className="inline-flex h-5 items-center whitespace-nowrap rounded-full bg-warning-tint px-2 text-xs text-warning"
      title="Cost is unavailable for this request"
    >
      {reason}
    </span>
  );
}

function CacheCell({ status }: { status?: string }) {
  if (status === "HIT") {
    return (
      <span className="inline-flex h-5 items-center rounded-full bg-info-tint px-2 text-xs font-medium text-info">
        hit
      </span>
    );
  }
  return <span className="text-xs text-text-4">{status ? status.toLowerCase() : "—"}</span>;
}

function truncateKeyId(id: string | undefined): string {
  if (!id) return "—";
  return id.length > 20 ? `${id.slice(0, 13)}…${id.slice(-4)}` : id;
}

// resolutionDiffers says whether the routed model is worth a second line.
// Routing often only prefixes the provider (groq/compound-mini →
// groq/groq/compound-mini); repeating that under every row reads as a
// different model when it is the same one.
function resolutionDiffers(record: ActivityRecord): boolean {
  const used = record.model_used;
  const requested = record.model_requested;
  if (!used || !requested || used === requested) return false;
  if (used === `${record.provider}/${requested}`) return false;
  if (used.endsWith(`/${requested}`)) return false;
  return true;
}

// Usage rows show absolute times when a range filter is active
// (DESIGN.md); the all-time view falls back to relative times.
function rowTime(record: ActivityRecord, rangeSeconds: number | undefined): string {
  if (!rangeSeconds) return formatRelativeTime(record.timestamp);
  const at = new Date(record.timestamp);
  if (Number.isNaN(at.getTime())) return "—";
  if (rangeSeconds <= 86400) {
    return at.toLocaleTimeString("en-US", {
      hour12: false,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  }
  return `${at.toLocaleDateString("en-US", { month: "numeric", day: "numeric" })} ${at.toLocaleTimeString("en-US", { hour12: false, hour: "2-digit", minute: "2-digit" })}`;
}

// --- Detail panel ---

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-border-1 py-2 text-sm last:border-b-0">
      <dt className="shrink-0 text-text-3">{label}</dt>
      <dd className="min-w-0 text-right text-text-1">{children}</dd>
    </div>
  );
}

function RequestDetail({
  record,
  admin,
  onClose,
}: {
  record: ActivityRecord;
  admin: boolean;
  onClose: () => void;
}) {
  // Display names come from the shared provider-catalog query; react-query
  // dedupes this against the Providers page fetch.
  const catalog = useQuery({
    queryKey: ["provider-catalog"],
    queryFn: listProviderCatalog,
    retry: false,
  });
  const providerName = catalog.data?.find(
    (entry) => entry.id === record.provider,
  )?.name;
  const tokens = record.tokens ?? {};
  const tokenParts = (
    [
      ["input", tokens.input],
      ["output", tokens.output],
      ["total", tokens.total],
      ["reasoning", tokens.reasoning],
      ["cache read", tokens.cache_read],
      ["cache write", tokens.cache_write],
      ["audio in", tokens.audio_input],
      ["audio out", tokens.audio_output],
    ] as const
  ).filter(([, count]) => count);
  const mediaParts = (
    [["images", record.media?.generated_images]] as const
  ).filter(([, count]) => count);
  const at = record.timestamp ? new Date(record.timestamp) : null;
  return (
    <SidePanel title={record.model_requested ?? "Request"} onClose={onClose}>
      <dl>
        <DetailRow label="Request">
          <code className="break-all font-mono text-xs text-text-2">
            {record.request_id ?? "—"}
          </code>
        </DetailRow>
        {admin && (
          <DetailRow label="Key">
            <code className="font-mono text-xs text-text-2" title={record.key_id}>
              {truncateKeyId(record.key_id)}
            </code>
          </DetailRow>
        )}
        <DetailRow label="Time">
          <span title={record.timestamp}>
            {at && !Number.isNaN(at.getTime()) ? at.toLocaleString() : "—"}
          </span>
        </DetailRow>
        {record.protocol && <DetailRow label="Protocol">{record.protocol}</DetailRow>}
        {record.operation && (
          <DetailRow label="Operation">
            {record.operation}
            {record.streaming ? " (streaming)" : ""}
          </DetailRow>
        )}
        {record.model_requested && (
          <DetailRow label="Model requested">
            <code className="break-all font-mono text-xs text-text-2">
              {record.model_requested}
            </code>
          </DetailRow>
        )}
        {resolutionDiffers(record) && (
          <DetailRow label="Model used">
            <code className="break-all font-mono text-xs text-text-2">{record.model_used}</code>
          </DetailRow>
        )}
        <DetailRow label="Provider">
          {record.provider ? providerLabel(record.provider, providerName) : "unrouted"}
        </DetailRow>
        <DetailRow label="Status">
          <StatusPill record={record} />
        </DetailRow>
        {record.error_class && (
          <DetailRow label="Error class">{record.error_class.replaceAll("_", " ")}</DetailRow>
        )}
        {record.attempts ? <DetailRow label="Attempts">{record.attempts}</DetailRow> : null}
        {Number.isFinite(record.routing_ms) && record.routing_ms ? (
          <DetailRow label="Routing">{formatMs(record.routing_ms)}</DetailRow>
        ) : null}
        <DetailRow label="Latency">{formatMs(record.latency_ms)}</DetailRow>
        {record.overhead_ms !== undefined && (
          <DetailRow label="Starport overhead">{formatMs(record.overhead_ms)}</DetailRow>
        )}
        {record.streaming && record.ttft_ms !== undefined && (
          <DetailRow label="TTFT">{formatMs(record.ttft_ms)}</DetailRow>
        )}
        {record.cache_status && (
          <DetailRow label="Cache">
            <CacheCell status={record.cache_status} />
          </DetailRow>
        )}
        <DetailRow label="Cost">
          {record.cost ? (
            <span className="tabular-nums">
              {formatNanoUSD(record.cost.nano_usd)} {record.cost.currency ?? "USD"}
            </span>
          ) : (
            <span className="text-warning">
              unavailable —{" "}
              {COST_REASONS[record.cost_unavailable_reason ?? ""] ??
                record.cost_unavailable_reason ??
                "unknown"}
            </span>
          )}
        </DetailRow>
        <DetailRow label="Tokens">
          {tokenParts.length ? (
            <span className="tabular-nums">
              {tokenParts.map(([name, count]) => `${name} ${formatCount(count)}`).join(" · ")}
              {record.tokens_estimated ? " (estimated)" : ""}
            </span>
          ) : (
            "—"
          )}
        </DetailRow>
        {mediaParts.length > 0 && (
          <DetailRow label="Media">
            <span className="tabular-nums">
              {mediaParts.map(([name, count]) => `${name} ${formatCount(count)}`).join(" · ")}
            </span>
          </DetailRow>
        )}
      </dl>
    </SidePanel>
  );
}

// --- Page ---

const INPUT_CLASS =
  "h-8 rounded-sm border border-border-1 bg-bg-panel px-2 text-sm text-text-1 outline-none transition-colors duration-150 ease-standard placeholder:text-text-4 hover:border-border-2 focus:border-accent";

function Header() {
  return (
    <div>
      <h1 className="text-xl font-semibold text-text-1">Usage</h1>
      <p className="mt-1 text-base text-text-3">
        Every request through the gateway: models, providers, tokens, latency, gateway overhead, and cost.
      </p>
    </div>
  );
}

function UsagePage() {
  const keyUsable = useGatewayAccess();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const scope = useActivityScope(keyUsable);
  const admin = scope.data === "admin";

  const range = search.range ?? "24h";
  const rangeSeconds = RANGE_SECONDS[range];

  const setSearch = (patch: Partial<UsageSearch>) => {
    void navigate({
      search: (previous: UsageSearch) => ({ ...previous, ...patch }),
      replace: true,
    });
  };

  // Text filters debounce into the URL so the server-side query refires
  // once per pause, not per keystroke.
  const [modelDraft, setModelDraft] = useState(search.model ?? "");
  const [providerDraft, setProviderDraft] = useState(search.provider ?? "");
  const [keyDraft, setKeyDraft] = useState(search.key ?? "");
  useEffect(() => {
    const timer = setTimeout(() => {
      setSearch({
        model: modelDraft.trim() || undefined,
        provider: providerDraft.trim() || undefined,
        key: keyDraft.trim() || undefined,
      });
    }, 300);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modelDraft, providerDraft, keyDraft]);

  const modelRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.tagName === "SELECT" ||
          target.isContentEditable)
      ) {
        return;
      }
      event.preventDefault();
      modelRef.current?.focus();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  // The since bound is pinned per window selection; refetching with the
  // same key reuses it, changing any filter recomputes it.
  const sinceISO = useMemo(
    () => (rangeSeconds ? new Date(Date.now() - rangeSeconds * 1000).toISOString() : undefined),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [rangeSeconds, search.model, search.provider, search.status, search.key],
  );

  const activity = useInfiniteQuery({
    queryKey: [
      "activity",
      scope.data,
      search.model,
      search.provider,
      search.status,
      admin ? search.key : undefined,
      range,
    ],
    enabled: scope.data === "admin" || scope.data === "own",
    initialPageParam: "",
    queryFn: ({ pageParam }) => {
      const filters: ActivityFilters = {
        model: search.model,
        provider: search.provider,
        status: search.status,
        since: sinceISO,
        limit: PAGE_LIMIT,
        cursor: pageParam || undefined,
      };
      return admin
        ? listAdminActivity({ ...filters, key_id: search.key })
        : listActivity(filters);
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
    retry: false,
  });

  // Eagerly page in up to AUTO_PAGES so charts cover the window.
  useEffect(() => {
    if (!activity.hasNextPage || activity.isFetchingNextPage) return;
    if ((activity.data?.pages.length ?? 0) >= AUTO_PAGES) return;
    void activity.fetchNextPage();
  }, [activity]);

  const records = useMemo(
    () => activity.data?.pages.flatMap((page) => page.data ?? []) ?? [],
    [activity.data],
  );
  const partial = Boolean(activity.hasNextPage);
  const suffix = partial ? "+" : "";

  const [selected, setSelected] = useState<ActivityRecord | null>(null);

  const buckets = useMemo(
    () => bucketize(records, rangeSeconds),
    [records, rangeSeconds],
  );

  const totals = useMemo(() => {
    let tokens = 0;
    let spendNano = 0;
    let priced = 0;
    let withoutCost = 0;
    let errors = 0;
    let latencyTotal = 0;
    let latencyCount = 0;
    for (const record of records) {
      tokens += record.tokens?.total ?? 0;
      if (record.cost) {
        spendNano += record.cost.nano_usd ?? 0;
        priced++;
      } else {
        withoutCost++;
      }
      if (record.status === "error") errors++;
      if (Number.isFinite(record.latency_ms)) {
        latencyTotal += record.latency_ms ?? 0;
        latencyCount++;
      }
    }
    return {
      tokens,
      spendNano,
      priced,
      withoutCost,
      errors,
      avgLatency: latencyCount ? latencyTotal / latencyCount : undefined,
    };
  }, [records]);

  const listRef = useRef<HTMLDivElement>(null);
  const virtualizer = useWindowVirtualizer({
    count: records.length,
    estimateSize: () => ROW_HEIGHT,
    overscan: 12,
    scrollMargin: listRef.current?.offsetTop ?? 0,
  });


  if (scope.data === "locked") {
    return (
      <div className="flex flex-col gap-4">
        <Header />
        <p className="text-base text-text-3">
          Usage needs a key with the activity scope. Update it in Settings.
        </p>
      </div>
    );
  }

  if (scope.data === "unconfigured") {
    return (
      <div className="flex flex-col gap-4">
        <Header />
        <p className="text-base text-text-3">
          Usage accounting is not configured on this gateway.
        </p>
      </div>
    );
  }

  const grid = admin
    ? "grid grid-cols-[120px_minmax(180px,1fr)_150px_110px_130px_80px_85px_70px_80px_90px_70px] items-center"
    : "grid grid-cols-[120px_minmax(180px,1fr)_110px_130px_80px_85px_70px_80px_90px_70px] items-center";
  const hasFilters = Boolean(search.model || search.provider || search.status || search.key);
  const loading = scope.isPending || activity.isPending;

  return (
    <div className="flex flex-col gap-4">
      <Header />

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-text-4" />
          <input
            ref={modelRef}
            type="search"
            placeholder="Filter by model  /"
            aria-label="Filter by model"
            value={modelDraft}
            onChange={(event) => setModelDraft(event.target.value)}
            className={`${INPUT_CLASS} w-56 pl-8`}
          />
        </div>
        <input
          type="search"
          placeholder="provider"
          aria-label="Filter by provider"
          value={providerDraft}
          onChange={(event) => setProviderDraft(event.target.value)}
          className={`${INPUT_CLASS} w-32`}
        />
        {admin && (
          <input
            type="search"
            placeholder="key ID"
            aria-label="Filter by key ID"
            value={keyDraft}
            onChange={(event) => setKeyDraft(event.target.value)}
            className={`${INPUT_CLASS} w-40 font-mono`}
          />
        )}
        <Select
          uiSize="sm"
          aria-label="Filter by status"
          value={search.status ?? ""}
          onChange={(event) => setSearch({ status: event.target.value || undefined })}
        >
          <option value="">Any status</option>
          {STATUSES.map((status) => (
            <option key={status} value={status}>
              {status}
            </option>
          ))}
        </Select>
        <Select
          uiSize="sm"
          aria-label="Time range"
          value={range}
          onChange={(event) =>
            setSearch({ range: event.target.value === "24h" ? undefined : event.target.value })
          }
        >
          {Object.entries(RANGE_LABELS).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </Select>
        <span className="ml-auto text-xs tabular-nums text-text-3">
          {loading
            ? "loading…"
            : `${formatCount(records.length)}${suffix} requests · ${scope.data === "admin" ? "all keys" : "your key"}`}
        </span>
      </div>

      {activity.error ? (
        <p className="text-base text-text-3">
          Failed to load usage: {(activity.error as Error).message}
        </p>
      ) : loading ? (
        <p className="text-base text-text-3">Loading requests…</p>
      ) : records.length === 0 ? (
        <div className="flex flex-col items-center gap-3 rounded-md border border-border-1 bg-bg-panel px-6 py-14 text-center">
          <BarChart3 className="size-6 text-text-4" aria-hidden="true" />
          <p className="text-base text-text-3">
            {hasFilters
              ? "No requests match these filters."
              : "No traffic in this window yet. Send a request through the gateway:"}
          </p>
          {!hasFilters && (
            <pre className="mt-1 max-w-full overflow-x-auto rounded-sm border border-border-1 bg-bg-raised px-4 py-3 text-left font-mono text-xs leading-5 text-text-2">
              {`curl ${window.location.origin}/v1/chat/completions \\
  -H "Authorization: Bearer $STARPORT_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"<model id from /models>","messages":[{"role":"user","content":"hello"}]}'`}
            </pre>
          )}
        </div>
      ) : (
        <>
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <ChartCard
              title="Requests"
              value={
                <>
                  {formatCount(records.length)}
                  {suffix}
                  {totals.errors > 0 && (
                    <>
                      <span className="mx-1.5 font-normal text-text-4">·</span>
                      <span className="text-error">
                        {formatCount(totals.errors)} errors
                      </span>
                    </>
                  )}
                </>
              }
            >
              <BarChart data={buckets} margin={{ top: 4, right: 12, bottom: 0, left: 0 }}>
                <CartesianGrid stroke={CHART.grid} vertical={false} />
                <XAxis
                  dataKey="label"
                  tick={AXIS_TICK}
                  tickLine={false}
                  axisLine={false}
                  interval="preserveStartEnd"
                  minTickGap={32}
                />
                <YAxis
                  width={36}
                  tick={AXIS_TICK}
                  tickLine={false}
                  axisLine={false}
                  allowDecimals={false}
                  tickFormatter={(value: number) => formatCount(value)}
                />
                <Tooltip
                  cursor={{ fill: "var(--bg-hover)" }}
                  content={({ active, payload, label }) => (
                    <ChartTip
                      active={active}
                      label={label}
                      rows={(payload ?? [])
                        .filter((item) => Number(item.value) > 0)
                        .map((item) => ({
                          name: String(item.name),
                          value: formatCount(Number(item.value)),
                          color: item.color as string | undefined,
                        }))}
                    />
                  )}
                />
                <Bar dataKey="ok" stackId="req" name="ok" fill={CHART.neutral} fillOpacity={0.55} />
                <Bar dataKey="cancelled" stackId="req" name="cancelled" fill={CHART.neutralSoft} fillOpacity={0.5} />
                <Bar dataKey="error" stackId="req" name="error" fill={CHART.error} fillOpacity={0.8} />
              </BarChart>
            </ChartCard>

            <ChartCard title="Tokens" value={`${formatCount(totals.tokens)}${suffix}`}>
              <AreaChart data={buckets} margin={{ top: 4, right: 12, bottom: 0, left: 0 }}>
                <CartesianGrid stroke={CHART.grid} vertical={false} />
                <XAxis
                  dataKey="label"
                  tick={AXIS_TICK}
                  tickLine={false}
                  axisLine={false}
                  interval="preserveStartEnd"
                  minTickGap={32}
                />
                <YAxis
                  width={44}
                  tick={AXIS_TICK}
                  tickLine={false}
                  axisLine={false}
                  tickFormatter={(value: number) => formatCount(value)}
                />
                <Tooltip
                  cursor={{ stroke: CHART.grid }}
                  content={({ active, payload, label }) => (
                    <ChartTip
                      active={active}
                      label={label}
                      rows={(payload ?? []).map((item) => ({
                        name: "tokens",
                        value: formatCount(Number(item.value)),
                      }))}
                    />
                  )}
                />
                <Area
                  dataKey="tokens"
                  stroke={CHART.neutral}
                  strokeWidth={1.5}
                  fill={CHART.neutral}
                  fillOpacity={0.12}
                  dot={false}
                  activeDot={{ r: 2.5, fill: CHART.neutral, stroke: "none" }}
                />
              </AreaChart>
            </ChartCard>

            <ChartCard
              title="Spend"
              value={
                totals.priced ? (
                  <>
                    {formatNanoUSD(totals.spendNano)}
                    {suffix}
                    {totals.withoutCost > 0 && (
                      <span className="ml-2 font-normal text-text-4">
                        {formatCount(totals.withoutCost)} w/o cost
                      </span>
                    )}
                  </>
                ) : (
                  "—"
                )
              }
            >
              <AreaChart data={buckets} margin={{ top: 4, right: 12, bottom: 0, left: 0 }}>
                <CartesianGrid stroke={CHART.grid} vertical={false} />
                <XAxis
                  dataKey="label"
                  tick={AXIS_TICK}
                  tickLine={false}
                  axisLine={false}
                  interval="preserveStartEnd"
                  minTickGap={32}
                />
                <YAxis
                  width={44}
                  tick={AXIS_TICK}
                  tickLine={false}
                  axisLine={false}
                  tickFormatter={(value: number) =>
                    value === 0 ? "$0" : formatNanoUSD(value * 1_000_000_000)
                  }
                />
                <Tooltip
                  cursor={{ stroke: CHART.grid }}
                  content={({ active, payload, label }) => (
                    <ChartTip
                      active={active}
                      label={label}
                      rows={(payload ?? []).map((item) => ({
                        name: "spend",
                        value: formatNanoUSD(Number(item.value) * 1_000_000_000),
                      }))}
                    />
                  )}
                />
                <Area
                  dataKey="spend"
                  stroke={CHART.neutral}
                  strokeWidth={1.5}
                  fill={CHART.neutral}
                  fillOpacity={0.12}
                  dot={false}
                  activeDot={{ r: 2.5, fill: CHART.neutral, stroke: "none" }}
                />
              </AreaChart>
            </ChartCard>

            <ChartCard
              title="Latency"
              value={totals.avgLatency !== undefined ? `${formatMs(totals.avgLatency)} avg` : "—"}
            >
              <LineChart data={buckets} margin={{ top: 4, right: 12, bottom: 0, left: 0 }}>
                <CartesianGrid stroke={CHART.grid} vertical={false} />
                <XAxis
                  dataKey="label"
                  tick={AXIS_TICK}
                  tickLine={false}
                  axisLine={false}
                  interval="preserveStartEnd"
                  minTickGap={32}
                />
                <YAxis
                  width={40}
                  tick={AXIS_TICK}
                  tickLine={false}
                  axisLine={false}
                  tickFormatter={(value: number) => formatMs(value)}
                />
                <Tooltip
                  cursor={{ stroke: CHART.grid }}
                  content={({ active, payload, label }) => (
                    <ChartTip
                      active={active}
                      label={label}
                      rows={(payload ?? [])
                        .filter((item) => item.value !== null && item.value !== undefined)
                        .map((item) => ({
                          name: "avg latency",
                          value: formatMs(Number(item.value)),
                        }))}
                    />
                  )}
                />
                <Line
                  dataKey="latency"
                  stroke={CHART.neutral}
                  strokeWidth={1.5}
                  connectNulls
                  dot={{ r: 1.5, fill: CHART.neutral, strokeWidth: 0 }}
                  activeDot={{ r: 2.5, fill: CHART.neutral, stroke: "none" }}
                />
              </LineChart>
            </ChartCard>
          </div>

          <div role="table" aria-rowcount={records.length} className="text-sm">
            <div role="rowgroup" className="sticky top-0 z-10 bg-bg-canvas">
              <div role="row" className={`${grid} h-8 border-b border-border-1`}>
                <div role="columnheader" className="px-2.5 text-xs font-medium text-text-3">Time</div>
                <div role="columnheader" className="px-2.5 text-xs font-medium text-text-3">Model</div>
                {admin && (
                  <div role="columnheader" className="px-2.5 text-xs font-medium text-text-3">Key</div>
                )}
                <div role="columnheader" className="px-2.5 text-xs font-medium text-text-3">Provider</div>
                <div role="columnheader" className="px-2.5 text-xs font-medium text-text-3">Status</div>
                <div role="columnheader" className="px-2.5 text-right text-xs font-medium text-text-3">Tokens</div>
                <div role="columnheader" className="px-2.5 text-right text-xs font-medium text-text-3" title="Gateway-added latency: total handling minus provider time">Overhead</div>
                <div role="columnheader" className="px-2.5 text-right text-xs font-medium text-text-3" title="Time to first token (streamed requests)">TTFT</div>
                <div role="columnheader" className="px-2.5 text-right text-xs font-medium text-text-3">Latency</div>
                <div role="columnheader" className="px-2.5 text-right text-xs font-medium text-text-3">Cost</div>
                <div role="columnheader" className="px-2.5 text-xs font-medium text-text-3">Cache</div>
              </div>
            </div>
            <div
              ref={listRef}
              role="rowgroup"
              className="relative"
              style={{ height: virtualizer.getTotalSize() }}
            >
              {virtualizer.getVirtualItems().map((item) => {
                const record = records[item.index];
                if (!record) return null;
                return (
                  <div
                    role="row"
                    key={`${record.request_id ?? "req"}-${item.index}`}
                    tabIndex={0}
                    onClick={() => setSelected(record)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        setSelected(record);
                      }
                    }}
                    className={`${grid} absolute inset-x-0 cursor-pointer border-b border-border-1 transition-colors duration-150 ease-standard hover:bg-bg-hover`}
                    style={{
                      height: ROW_HEIGHT,
                      transform: `translateY(${item.start - (listRef.current?.offsetTop ?? 0)}px)`,
                    }}
                  >
                    <div role="cell" className="px-2.5 font-mono text-xs tabular-nums text-text-3" title={record.timestamp}>
                      {rowTime(record, rangeSeconds)}
                    </div>
                    <div role="cell" className="min-w-0 px-2.5">
                      <div className="truncate text-text-1">{record.model_requested ?? "—"}</div>
                      {resolutionDiffers(record) && (
                        <div className="truncate font-mono text-xs text-text-4">
                          {record.model_used}
                        </div>
                      )}
                    </div>
                    {admin && (
                      <div role="cell" className="px-2.5">
                        <code className="font-mono text-xs text-text-3" title={record.key_id}>
                          {truncateKeyId(record.key_id)}
                        </code>
                      </div>
                    )}
                    <div role="cell" className="truncate px-2.5 text-text-2">
                      {record.provider ?? <span className="text-text-4">—</span>}
                    </div>
                    <div role="cell" className="min-w-0 px-2.5">
                      <StatusPill record={record} />
                    </div>
                    <div role="cell" className="px-2.5 text-right font-mono text-xs tabular-nums text-text-2">
                      {record.tokens?.total ? formatCount(record.tokens.total) : "—"}
                    </div>
                    <div role="cell" className="px-2.5 text-right font-mono text-xs tabular-nums text-text-2">
                      {record.overhead_ms !== undefined ? formatMs(record.overhead_ms) : "—"}
                    </div>
                    <div role="cell" className="px-2.5 text-right font-mono text-xs tabular-nums text-text-2">
                      {record.streaming && record.ttft_ms !== undefined
                        ? formatMs(record.ttft_ms)
                        : "—"}
                    </div>
                    <div role="cell" className="px-2.5 text-right font-mono text-xs tabular-nums text-text-2">
                      {formatMs(record.latency_ms)}
                    </div>
                    <div role="cell" className="px-2.5 text-right">
                      <CostCell record={record} />
                    </div>
                    <div role="cell" className="px-2.5">
                      <CacheCell status={record.cache_status} />
                    </div>
                  </div>
                );
              })}
            </div>
          </div>

          {partial && (activity.data?.pages.length ?? 0) >= AUTO_PAGES && (
            <div>
              <button
                type="button"
                disabled={activity.isFetchingNextPage}
                onClick={() => void activity.fetchNextPage()}
                className="inline-flex h-8 items-center rounded-sm px-3 text-sm text-text-2 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1 disabled:opacity-50"
              >
                {activity.isFetchingNextPage ? "Loading…" : "Load older requests"}
              </button>
            </div>
          )}
        </>
      )}

      {selected && (
        <RequestDetail record={selected} admin={admin} onClose={() => setSelected(null)} />
      )}
    </div>
  );
}
