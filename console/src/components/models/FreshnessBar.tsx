import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { useState, type ReactNode } from "react";

import { ChangesPanel } from "@/components/models/ChangesPanel";
import { IconButton } from "@/components/ui/IconButton";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { accessMessage, ApiError, refreshCatalog } from "@/lib/api";
import { queries } from "@/lib/queries";
import { shortGenerationID } from "@/lib/format";
import { announce, errorText, report } from "@/lib/mutations";

// manifestSentence says what a missing manifest means for the reader. The
// server's reason names the cause; the sentence leads with the effect,
// which is that the completeness and degradation facts are unknown.
export function manifestSentence(reason: string | undefined): string {
  const lead = "No manifest is stored for this generation, so completeness and degradation are unknown.";
  if (!reason) return lead;
  const cause = reason.endsWith(".") ? reason : `${reason}.`;
  return `${lead} ${cause.charAt(0).toUpperCase()}${cause.slice(1)}`;
}

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
  const refresh = useMutation({
    mutationFn: () => refreshCatalog(),
    onSuccess: async (result) => {
      if (result?.changed) {
        announce(`Catalog updated to generation ${shortGenerationID(result.generation_id)}`);
      } else {
        announce("Catalog is already current");
      }
      await queryClient.invalidateQueries({ queryKey: queries.models().queryKey });
      await queryClient.invalidateQueries({ queryKey: queries.catalogMetadata().queryKey });
    },
    onError: (error) => {
      if (error instanceof ApiError && error.needsKey) {
        report(accessMessage(error, "admin"));
      } else {
        report(`Refresh failed: ${errorText(error)}`);
      }
    },
  });

  const data = metadata.data;
  return (
    <div className="flex items-center justify-between gap-x-4 rounded-md border border-border-1 bg-bg-panel px-4 py-2">
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
          <span className="text-xs text-text-3">
            generated <RelativeTime iso={data.generated_at} />
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
          <Badge tone="neutral" title={manifestSentence(data.manifest_unavailable_reason)}>
            no manifest
          </Badge>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        {data && (
          <Popover open={detailsOpen} onOpenChange={setDetailsOpen}>
            <PopoverTrigger className="h-7 rounded-xs px-2 text-xs text-text-3 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1 data-popup-open:bg-bg-hover data-popup-open:text-text-1">
              Details
            </PopoverTrigger>
            <PopoverContent
              align="end"
              aria-label="Catalog details"
              data-testid="freshness-details"
              className="w-80 text-xs"
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
                    <dd data-testid="manifest-sentence" className="text-text-2">
                      {manifestSentence(data.manifest_unavailable_reason)}
                    </dd>
                  </>
                )}
              </dl>
            </PopoverContent>
          </Popover>
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
        <IconButton
          label="Refresh catalog"
          onClick={() => refresh.mutate()}
          disabled={refresh.isPending}
          className="size-7 rounded-xs text-text-3 hover:bg-bg-hover hover:text-text-1 disabled:opacity-50"
        >
          <RefreshCw className={`size-3.5 ${refresh.isPending ? "animate-spin" : ""}`} />
        </IconButton>
      </div>
      {changesOpen && <ChangesPanel onClose={() => setChangesOpen(false)} />}
    </div>
  );
}
