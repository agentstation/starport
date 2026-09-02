import { keepPreviousData, useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useWindowVirtualizer } from "@tanstack/react-virtual";
import { BarChart3, Download, Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ReferenceDot,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import {
  AXIS_TICK,
  CHART,
  ChartCard,
  ChartTip,
  CURSOR,
  USAGE_SYNC_ID,
  endpointOf,
  useChartMotion,
} from "@/components/ui/Chart";
import { GhostButton } from "@/components/ui/Form";
import { Select } from "@/components/ui/Select";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { TableSkeleton } from "@/components/ui/skeleton";
import { RelativeTime } from "@/components/ui/RelativeTime";
import {
  ApiError,
  exportActivity,
  type ActivityExportFormat,
  type ActivityFilters,
  type ActivityRecord,
} from "@/lib/api";
import { queries } from "@/lib/queries";
import {
  formatCount,
  formatMs,
  formatNanoUSD,
  providerLabel,
} from "@/lib/format";
import { RANGE_LABELS, RANGE_SECONDS, rangeOf } from "@/lib/timeRange";
import { useGatewayAccess } from "@/lib/useGatewayAccess";
import { bucketize, describeBuckets, type Bucket } from "@/lib/usageBuckets";

const PAGE_LIMIT = 200;
// Pages fetched eagerly so the charts and counts cover the window, not
// just the first screen. Beyond this the counts report themselves as
// partial with a "+" suffix.
const AUTO_PAGES = 5;
const ROW_HEIGHT = 40;

const STATUSES = ["ok", "error", "cancelled"] as const;
// The two verdicts a guardrail leaves on a record it closed. An allowed
// turn carries no facet: it is the ordinary case.
const GUARDRAIL_VERDICTS = ["refuse", "redact"] as const;
const GUARDRAIL_LABELS: Record<(typeof GUARDRAIL_VERDICTS)[number], string> = {
  refuse: "refused",
  redact: "redacted",
};
// The status select carries both facets under one value, so a reader picks
// "refused" beside "error" instead of hunting a second control. A guardrail
// value wears this prefix to stay apart from a status.
const GUARDRAIL_OPTION = "guardrail:";

// The request stack: three steps a reader can tell apart. The legend under
// the chart carries the same three, so the encoding is on the card.
const REQUEST_SERIES = [
  { name: "ok", color: CHART.neutralStrong, opacity: 0.45 },
  { name: "cancelled", color: CHART.neutralSoft, opacity: 0.7 },
  { name: "error", color: CHART.error, opacity: 0.8 },
] as const;

const requestsOf = (bucket: Bucket) => bucket.ok + bucket.cancelled + bucket.error;

// titleOf reads the slice's own start-to-end heading from the hovered
// point, so a tooltip names the exact slice and never only its tick.
function titleOf(payload: readonly { payload?: unknown }[] | undefined, label: unknown): string {
  const bucket = payload?.[0]?.payload as Bucket | undefined;
  return bucket?.title ?? String(label ?? "");
}

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
  // request narrows the listing to the one record a request left. The
  // audit log links here with it.
  request?: string;
  status?: string;
  // guardrail keeps only the turns a guardrail refused or redacted.
  guardrail?: string;
  range?: string;
  selected?: string;
};

export const Route = createFileRoute("/usage")({
  component: UsagePage,
  validateSearch: (search: Record<string, unknown>): UsageSearch => {
    const str = (value: unknown) =>
      typeof value === "string" && value !== "" ? value : undefined;
    const status = str(search.status);
    const guardrail = str(search.guardrail);
    return {
      model: str(search.model),
      provider: str(search.provider),
      key: str(search.key),
      request: str(search.request),
      status: status && (STATUSES as readonly string[]).includes(status) ? status : undefined,
      guardrail:
        guardrail && (GUARDRAIL_VERDICTS as readonly string[]).includes(guardrail)
          ? guardrail
          : undefined,
      range: rangeOf(search.range),
      selected: str(search.selected),
    };
  },
});

// --- Scope: admin keys read the cross-key listing, other keys fall back
// to their own activity, and the page reports the distinction. One probe
// answers for the session; a locked own listing surfaces as the activity
// query's own error.

function useActivityScope(enabled: boolean) {
  return useQuery({ ...queries.activityScope(), enabled });
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

// CacheCell names the layer that answered: an exact replay reads "hit", and
// a semantic hit reads "semantic" with its similarity one hover away, so a
// reader separates a near-duplicate answer from a verbatim one.
function CacheCell({ record }: { record: ActivityRecord }) {
  const status = record.cache_status;
  if (status === "HIT") {
    const semantic = record.cache_semantic || (record.cache_similarity ?? 0) > 0;
    const similarity = record.cache_similarity ? formatSimilarity(record.cache_similarity) : undefined;
    return (
      <span
        className="inline-flex h-5 items-center rounded-full bg-info-tint px-2 text-xs font-medium text-info"
        title={similarity ? `semantic hit · similarity ${similarity}` : undefined}
      >
        {semantic ? "semantic" : "hit"}
      </span>
    );
  }
  return <span className="text-xs text-text-4">{status ? status.toLowerCase() : "—"}</span>;
}

function formatSimilarity(similarity: number): string {
  return similarity.toFixed(2);
}

// GuardrailCell reports how a guardrail closed the turn. A refusal is a
// failed request from the caller's side, so it wears the error tone; a
// redaction answered, so it wears the warning tone. An allowed turn reads
// as the quiet default.
function GuardrailCell({ record }: { record: ActivityRecord }) {
  const verdict = record.guardrail_verdict;
  const title = record.guardrail_check ? `check: ${record.guardrail_check}` : undefined;
  if (verdict === "refuse") {
    return (
      <span className="inline-flex h-5 items-center rounded-full bg-error-tint px-2 text-xs font-medium text-error" title={title}>
        refused
      </span>
    );
  }
  if (verdict === "redact") {
    return (
      <span className="inline-flex h-5 items-center rounded-full bg-warning-tint px-2 text-xs font-medium text-warning" title={title}>
        redacted
      </span>
    );
  }
  return <span className="text-xs text-text-4" title={title}>{verdict ? verdict : "—"}</span>;
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
function rowTime(record: ActivityRecord, rangeSeconds: number | undefined): React.ReactNode {
  if (!rangeSeconds) return <RelativeTime iso={record.timestamp} />;
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
    ...queries.providerCatalog(),
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
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{record.model_requested ?? "Request"}</SheetTitle>
        </SheetHeader>
        <SheetBody>
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
                <CacheCell record={record} />
                {record.cache_similarity ? (
                  <span className="ml-2 font-mono text-xs tabular-nums text-text-3">
                    {formatSimilarity(record.cache_similarity)} similar
                  </span>
                ) : null}
              </DetailRow>
            )}
            {record.guardrail_verdict && (
              <DetailRow label="Guardrail">
                <GuardrailCell record={record} />
                {record.guardrail_check ? (
                  <span className="ml-2 font-mono text-xs text-text-3">{record.guardrail_check}</span>
                ) : null}
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
        </SheetBody>
      </SheetContent>
    </Sheet>
  );
}

// --- Page ---

const INPUT_CLASS =
  "h-8 rounded-sm border border-border-1 bg-bg-panel px-2 text-sm text-text-1 outline-none transition-colors duration-150 ease-standard placeholder:text-text-4 hover:border-border-2 focus-visible:border-accent";

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
  const [requestDraft, setRequestDraft] = useState(search.request ?? "");
  useEffect(() => {
    const timer = setTimeout(() => {
      void navigate({
        search: (previous: UsageSearch) => ({
          ...previous,
          model: modelDraft.trim() || undefined,
          provider: providerDraft.trim() || undefined,
          key: keyDraft.trim() || undefined,
          request: requestDraft.trim() || undefined,
        }),
        replace: true,
      });
    }, 300);
    return () => clearTimeout(timer);
  }, [modelDraft, providerDraft, keyDraft, requestDraft, navigate]);

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
    [
      rangeSeconds,
      search.model,
      search.provider,
      search.status,
      search.guardrail,
      search.key,
      search.request,
    ],
  );

  const filters: ActivityFilters = {
    model: search.model,
    provider: search.provider,
    status: search.status,
    guardrail: search.guardrail,
    key_id: admin ? search.key : undefined,
    request_id: search.request,
    limit: PAGE_LIMIT,
  };

  const activity = useInfiniteQuery({
    ...queries.activity({
      scope: scope.data ?? "own",
      filters,
      sinceISO,
    }),
    enabled: scope.data === "admin" || scope.data === "own",
    // A filter change keeps the rows on screen until the new page lands.
    placeholderData: keepPreviousData,
  });
  const locked =
    activity.error instanceof ApiError && activity.error.needsKey;
  const unconfigured =
    scope.data === "unconfigured" ||
    (activity.error instanceof ApiError && activity.error.status === 503);

  // Eagerly page in up to AUTO_PAGES so charts cover the window. Placeholder
  // pages belong to the previous filter, so they never page further.
  const pageCount = activity.data?.pages.length ?? 0;
  useEffect(() => {
    if (!activity.hasNextPage || activity.isFetchingNextPage || activity.isPlaceholderData) {
      return;
    }
    if (pageCount >= AUTO_PAGES) return;
    void activity.fetchNextPage();
  }, [
    activity.hasNextPage,
    activity.isFetchingNextPage,
    activity.isPlaceholderData,
    activity.fetchNextPage,
    pageCount,
  ]);

  const records = useMemo(
    () => activity.data?.pages.flatMap((page) => page.data ?? []) ?? [],
    [activity.data],
  );
  const partial = Boolean(activity.hasNextPage);
  const suffix = partial ? "+" : "";

  // The open request lives in the address. A record without a request id
  // falls back to its timestamp, which every record carries.
  const recordKey = (record: ActivityRecord) => record.request_id ?? record.timestamp;
  const selected = search.selected
    ? (records.find((record) => recordKey(record) === search.selected) ?? null)
    : null;
  const setSelected = (record: ActivityRecord | null) =>
    setSearch({ selected: record ? recordKey(record) : undefined });

  // A sample that still has pages behind it is the newest slice of the
  // window, so the charts cover only what loaded and the caption says so.
  const series = useMemo(
    () => bucketize(records, rangeSeconds, { truncated: partial }),
    [records, rangeSeconds, partial],
  );
  const buckets = series.buckets;
  const caption = describeBuckets(series, records.length);
  const animate = useChartMotion();
  const requestsEnd = endpointOf(buckets, requestsOf, CHART.neutral);
  const tokensEnd = endpointOf(buckets, (bucket) => bucket.tokens, CHART.neutral);
  const spendEnd = endpointOf(buckets, (bucket) => bucket.spendNano, CHART.neutral);
  const latencyEnd = endpointOf(buckets, (bucket) => bucket.latency, CHART.neutral);

  const totals = useMemo(() => {
    let tokens = 0;
    let spendNano = 0;
    let priced = 0;
    let withoutCost = 0;
    let errors = 0;
    let semantic = 0;
    let latencyTotal = 0;
    let latencyCount = 0;
    for (const record of records) {
      if (record.cache_semantic || (record.cache_similarity ?? 0) > 0) semantic++;
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
      semantic,
      avgLatency: latencyCount ? latencyTotal / latencyCount : undefined,
    };
  }, [records]);

  // The export streams the rows the listing shows, under the same filters
  // and window, as a file the browser saves. The page fetches the bytes
  // itself because a bearer key never rides a plain link.
  const [exporting, setExporting] = useState<ActivityExportFormat | null>(null);
  const [exportError, setExportError] = useState<string | null>(null);
  const exportRows = async (format: ActivityExportFormat) => {
    setExporting(format);
    setExportError(null);
    try {
      const blob = await exportActivity(scope.data === "admin" ? "admin" : "own", { ...filters, since: sinceISO }, format);
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `starport-activity-${range}.${format}`;
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(url);
    } catch (error) {
      setExportError(error instanceof Error ? error.message : String(error));
    } finally {
      setExporting(null);
    }
  };

  const listRef = useRef<HTMLDivElement>(null);
  const virtualizer = useWindowVirtualizer({
    count: records.length,
    estimateSize: () => ROW_HEIGHT,
    overscan: 12,
    scrollMargin: listRef.current?.offsetTop ?? 0,
  });

  if (locked) {
    return (
      <div className="flex flex-col gap-4">
        <Header />
        <p className="text-base text-text-3">
          Usage needs a key with the activity scope. Update it in Settings.
        </p>
      </div>
    );
  }

  if (unconfigured) {
    return (
      <div className="flex flex-col gap-4">
        <Header />
        <p className="text-base text-text-3">
          Usage accounting is not configured on this gateway.
        </p>
      </div>
    );
  }

  // Twelve fixed columns beside one that grows. Their minimum sum stays
  // under the 1136px content width of a 1440px viewport, so the guardrail
  // column at the end never clips: the table has no scroll axis of its own
  // because the header sticks to the window.
  const grid = admin
    ? "grid grid-cols-[100px_minmax(150px,1fr)_130px_100px_120px_70px_75px_65px_75px_85px_80px_80px] items-center"
    : "grid grid-cols-[100px_minmax(150px,1fr)_100px_120px_70px_75px_65px_75px_85px_80px_80px] items-center";
  const hasFilters = Boolean(
    search.model ||
      search.provider ||
      search.status ||
      search.guardrail ||
      search.key ||
      search.request,
  );
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
        <input
          type="search"
          placeholder="request ID"
          aria-label="Filter by request ID"
          value={requestDraft}
          onChange={(event) => setRequestDraft(event.target.value)}
          className={`${INPUT_CLASS} w-44 font-mono`}
        />
        <Select
          uiSize="sm"
          aria-label="Filter by status"
          value={search.guardrail ? `${GUARDRAIL_OPTION}${search.guardrail}` : (search.status ?? "")}
          onChange={(event) => {
            const value = event.target.value;
            if (value.startsWith(GUARDRAIL_OPTION)) {
              setSearch({ status: undefined, guardrail: value.slice(GUARDRAIL_OPTION.length) });
            } else {
              setSearch({ status: value || undefined, guardrail: undefined });
            }
          }}
        >
          <option value="">Any status</option>
          {STATUSES.map((status) => (
            <option key={status} value={status}>
              {status}
            </option>
          ))}
          <optgroup label="Guardrail">
            {GUARDRAIL_VERDICTS.map((verdict) => (
              <option key={verdict} value={`${GUARDRAIL_OPTION}${verdict}`}>
                {GUARDRAIL_LABELS[verdict]}
              </option>
            ))}
          </optgroup>
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
        <div className="ml-auto flex items-center gap-2">
        <span className="text-xs tabular-nums text-text-3">
          {loading
            ? "loading…"
            : `${formatCount(records.length)}${suffix} requests · ${scope.data === "admin" ? "all keys" : "your key"}`}
        </span>
        <GhostButton
          disabled={exporting !== null || loading}
          onClick={() => void exportRows("ndjson")}
          title="Download the filtered rows as NDJSON, one JSON record per line"
        >
          <Download className="mr-1.5 size-3.5" aria-hidden="true" />
          {exporting === "ndjson" ? "Exporting…" : "Export NDJSON"}
        </GhostButton>
        <GhostButton
          disabled={exporting !== null || loading}
          onClick={() => void exportRows("csv")}
          title="Download the filtered rows as CSV"
        >
          <Download className="mr-1.5 size-3.5" aria-hidden="true" />
          {exporting === "csv" ? "Exporting…" : "Export CSV"}
        </GhostButton>
        </div>
      </div>
      {exportError && (
        <p role="alert" className="text-xs text-error">
          Export failed: {exportError}
        </p>
      )}

      {activity.error ? (
        <p className="text-base text-text-3">
          Failed to load usage: {(activity.error as Error).message}
        </p>
      ) : loading ? (
        <TableSkeleton columns={6} rows={8} />
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
                  {totals.semantic > 0 && (
                    <>
                      <span className="mx-1.5 font-normal text-text-4">·</span>
                      <span className="text-info" title="Answered by the semantic cache">
                        {formatCount(totals.semantic)} semantic
                      </span>
                    </>
                  )}
                </>
              }
              series={REQUEST_SERIES}
              caption={caption}
            >
              <BarChart
                data={buckets}
                syncId={USAGE_SYNC_ID}
                margin={{ top: 4, right: 12, bottom: 0, left: 0 }}
              >
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
                      label={titleOf(payload, label)}
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
                {REQUEST_SERIES.map((item) => (
                  <Bar
                    key={item.name}
                    dataKey={item.name}
                    stackId="req"
                    name={item.name}
                    fill={item.color}
                    fillOpacity={item.opacity}
                    isAnimationActive={animate}
                  />
                ))}
                {requestsEnd && <ReferenceDot {...requestsEnd} />}
              </BarChart>
            </ChartCard>

            <ChartCard
              title="Tokens"
              value={`${formatCount(totals.tokens)}${suffix}`}
              caption={caption}
            >
              <AreaChart
                data={buckets}
                syncId={USAGE_SYNC_ID}
                margin={{ top: 4, right: 12, bottom: 0, left: 0 }}
              >
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
                  allowDecimals={false}
                  tickFormatter={(value: number) => formatCount(value)}
                />
                <Tooltip
                  cursor={CURSOR}
                  content={({ active, payload, label }) => (
                    <ChartTip
                      active={active}
                      label={titleOf(payload, label)}
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
                  isAnimationActive={animate}
                />
                {tokensEnd && <ReferenceDot {...tokensEnd} />}
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
              caption={caption}
            >
              <AreaChart
                data={buckets}
                syncId={USAGE_SYNC_ID}
                margin={{ top: 4, right: 12, bottom: 0, left: 0 }}
              >
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
                  width={60}
                  tick={AXIS_TICK}
                  tickLine={false}
                  axisLine={false}
                  tickFormatter={(value: number) => (value === 0 ? "$0" : formatNanoUSD(value))}
                />
                <Tooltip
                  cursor={CURSOR}
                  content={({ active, payload, label }) => (
                    <ChartTip
                      active={active}
                      label={titleOf(payload, label)}
                      rows={(payload ?? []).map((item) => ({
                        name: "spend",
                        value: formatNanoUSD(Number(item.value)),
                      }))}
                    />
                  )}
                />
                <Area
                  dataKey="spendNano"
                  stroke={CHART.neutral}
                  strokeWidth={1.5}
                  fill={CHART.neutral}
                  fillOpacity={0.12}
                  dot={false}
                  activeDot={{ r: 2.5, fill: CHART.neutral, stroke: "none" }}
                  isAnimationActive={animate}
                />
                {spendEnd && <ReferenceDot {...spendEnd} />}
              </AreaChart>
            </ChartCard>

            <ChartCard
              title="Latency"
              value={totals.avgLatency !== undefined ? `${formatMs(totals.avgLatency)} avg` : "—"}
              caption={caption}
            >
              <LineChart
                data={buckets}
                syncId={USAGE_SYNC_ID}
                margin={{ top: 4, right: 12, bottom: 0, left: 0 }}
              >
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
                  cursor={CURSOR}
                  content={({ active, payload, label }) => (
                    <ChartTip
                      active={active}
                      label={titleOf(payload, label)}
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
                  dot={false}
                  activeDot={{ r: 2.5, fill: CHART.neutral, stroke: "none" }}
                  isAnimationActive={animate}
                />
                {latencyEnd && <ReferenceDot {...latencyEnd} />}
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
                <div role="columnheader" className="px-2.5 text-xs font-medium text-text-3" title="How a guardrail closed the turn">Guardrail</div>
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
                    <div
                      role="cell"
                      className="px-2.5 font-mono text-xs tabular-nums text-text-3"
                      title={rangeSeconds ? record.timestamp : undefined}
                    >
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
                      <CacheCell record={record} />
                    </div>
                    <div role="cell" className="px-2.5">
                      <GuardrailCell record={record} />
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
