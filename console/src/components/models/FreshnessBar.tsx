import { useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";

import { ChangesPanel } from "@/components/models/ChangesPanel";
import { accessMessage, ApiError, refreshCatalog } from "@/lib/api";
import { queries } from "@/lib/queries";
import { formatRelativeTime, shortGenerationID } from "@/lib/format";

// A catalog older than a week is worth flagging: an embedded bootstrap
// snapshot ships with the binary and can predate the install by releases.
const STALE_AFTER_SECONDS = 7 * 24 * 3600;

function Badge({
  tone,
  title,
  children,
}: {
  tone: "warn" | "err" | "neutral";
  title?: string;
  children: ReactNode;
}) {
  const tones = {
    warn: "bg-warning-tint text-warning",
    err: "bg-error-tint text-error",
    neutral: "bg-bg-raised text-text-3",
  };
  return (
    <span
      title={title}
      className={`inline-flex h-5 items-center rounded-xs px-1.5 text-xs font-medium ${tones[tone]}`}
    >
      {children}
    </span>
  );
}

// FreshnessBar renders the Starmap snapshot identity loudly: generation,
// age, degradation, and the two catalog counters, plus refresh and the
// generation-to-generation diff. Staleness is never hidden.
export function FreshnessBar() {
  const queryClient = useQueryClient();
  const metadata = useQuery({
    ...queries.catalogMetadata(),
  });
  const [changesOpen, setChangesOpen] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(
    null,
  );
  const noticeTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(noticeTimer.current), []);

  const say = (text: string, error = false) => {
    setNotice({ text, error });
    clearTimeout(noticeTimer.current);
    noticeTimer.current = setTimeout(() => setNotice(null), 6000);
  };

  const refresh = async () => {
    setRefreshing(true);
    try {
      const report = await refreshCatalog();
      if (report?.changed) {
        say(`Catalog updated to generation ${shortGenerationID(report.generation_id)}`);
      } else {
        say("Catalog is already current");
      }
      await queryClient.invalidateQueries({ queryKey: queries.models().queryKey });
      await queryClient.invalidateQueries({ queryKey: queries.catalogMetadata().queryKey });
    } catch (error) {
      if (error instanceof ApiError && error.needsKey) {
        say(accessMessage(error, "admin"), true);
      } else {
        say(`Refresh failed: ${error instanceof Error ? error.message : error}`, true);
      }
    } finally {
      setRefreshing(false);
    }
  };

  const data = metadata.data;
  return (
    <div className="relative flex items-center justify-between gap-x-4 overflow-visible rounded-md border border-border-1 bg-bg-panel px-4 py-2">
      <div className="flex min-w-0 items-center gap-2">
        <span
          className="rounded-xs border border-border-1 bg-bg-raised px-1.5 py-0.5 font-mono text-xs text-text-2"
          title={data?.generation_id}
        >
          {metadata.isPending
            ? "generation …"
            : data?.generation_id
              ? `generation ${shortGenerationID(data.generation_id)}`
              : "generation — (metadata unavailable)"}
        </span>
        {data?.generated_at && (
          <span className="text-xs text-text-3" title={data.generated_at}>
            generated {formatRelativeTime(data.generated_at)}
          </span>
        )}
        {data?.completeness && data.completeness !== "complete" && (
          <Badge tone="warn" title="This generation does not cover every source.">
            {data.completeness}
          </Badge>
        )}
        {data?.degraded && (
          <Badge
            tone="err"
            title={data.degradation_reasons?.join("; ") || "no reason recorded"}
          >
            degraded
          </Badge>
        )}
        {(data?.age_seconds ?? 0) > STALE_AFTER_SECONDS && (
          <Badge tone="warn" title="Refresh the catalog to pick up a newer generation.">
            stale
          </Badge>
        )}
        {data && !data.manifest_available && (
          <Badge tone="neutral" title={data.manifest_unavailable_reason ?? ""}>
            no manifest
          </Badge>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        {notice && (
          <span className={`text-xs ${notice.error ? "text-error" : "text-success"}`}>
            {notice.text}
          </span>
        )}
        {data && (
          <button
            type="button"
            onClick={() => setDetailsOpen((open) => !open)}
            aria-expanded={detailsOpen}
            className="h-7 rounded-xs px-2 text-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1"
          >
            Details
          </button>
        )}
        {data?.generation_id && (
          <button
            type="button"
            onClick={() => setChangesOpen(true)}
            className="h-7 rounded-xs px-2 text-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1"
          >
            What changed
          </button>
        )}
        <button
          type="button"
          onClick={refresh}
          disabled={refreshing}
          aria-label="Refresh catalog"
          title="Refresh catalog"
          className="flex size-7 items-center justify-center rounded-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1 disabled:opacity-50"
        >
          <RefreshCw className={`size-3.5 ${refreshing ? "animate-spin" : ""}`} />
        </button>
      </div>
      {detailsOpen && data && (
        <div
          data-testid="freshness-details"
          className="absolute right-3 top-full z-20 mt-1 w-80 rounded-md border border-border-1 bg-bg-panel p-3 text-xs shadow-lg"
        >
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5">
            <dt className="text-text-4">generation</dt>
            <dd className="break-all font-mono text-text-2">
              {data.generation_id ?? "—"}
            </dd>
            <dt className="text-text-4">generated at</dt>
            <dd className="text-text-2">{data.generated_at ?? "—"}</dd>
            <dt className="text-text-4">catalog sequence</dt>
            <dd className="tabular-nums text-text-2">
              {data.catalog_sequence ?? "—"}
            </dd>
            <dt className="text-text-4">availability revision</dt>
            <dd className="tabular-nums text-text-2">
              {data.availability_revision ?? "—"}
            </dd>
            {data.completeness && (
              <>
                <dt className="text-text-4">completeness</dt>
                <dd className="text-text-2">{data.completeness}</dd>
              </>
            )}
            {data.degraded && (
              <>
                <dt className="text-text-4">degraded</dt>
                <dd className="text-error">
                  {data.degradation_reasons?.join("; ") || "no reason recorded"}
                </dd>
              </>
            )}
            {!data.manifest_available && (
              <>
                <dt className="text-text-4">manifest</dt>
                <dd className="text-text-2">
                  {data.manifest_unavailable_reason ?? "unavailable"}
                </dd>
              </>
            )}
          </dl>
        </div>
      )}
      {changesOpen && <ChangesPanel onClose={() => setChangesOpen(false)} />}
    </div>
  );
}
