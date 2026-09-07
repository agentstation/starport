import { CircleSlash, LoaderCircle, Lock } from "lucide-react";
import type { RefObject } from "react";

import {
  UNAUTHORIZED_SENTENCE,
  type CatalogAdminRead,
  type CatalogSummaryRead,
} from "@/components/shell/CatalogSummary";
import { Pill } from "@/components/ui/Pill";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { formatAge } from "@/lib/format";
import { cn } from "@/lib/utils";

// CatalogChip is the one catalog indicator of the console. It sits in the
// shell, so every route carries it, and it holds one element for each status
// concept the reader can act on. A concept never shares a glyph with another
// one: usability, authorization, and freshness each own their element, and the
// three operator concepts — degradation, fallback, and a source this gateway
// could not read — each own a pill of their own beside them. Work in flight
// adds an icon and never replaces the dot.
//
// A healthy catalog deserves almost no attention, so the healthy chip is a
// small dot, the word Catalog, and an age. The generation lives in the panel's
// identity section, where a reader who needs to quote it can copy it.

// CatalogVerdict is what the chip states about freshness. The server grades
// the age and the console reads the grade. This console holds no age rule of
// its own.
export type CatalogVerdict = "fresh" | "stale" | "unknown";

// verdictOf maps the server grade onto the two verdicts the chip draws. The
// gateway grades a catalog current, warn, critical, or unknown. Warn and
// critical are both stale to a reader, who acts the same way for each.
export function verdictOf(freshness: string | undefined): CatalogVerdict {
  if (freshness === "current") return "fresh";
  if (freshness === "warn" || freshness === "critical") return "stale";
  return "unknown";
}

// CatalogPosture is the one state the chip leads with. The three are
// exclusive: a session that cannot read the catalog learns nothing about its
// freshness, and a gateway with no catalog has no freshness to state.
export type CatalogPosture = "authorization" | "unusable" | "catalog";

export function postureOf(read: CatalogSummaryRead): CatalogPosture {
  if (read.refused) return "authorization";
  if (read.error !== null && read.error !== undefined) return "unusable";
  if (read.summary !== undefined && read.summary.usable === false) return "unusable";
  return "catalog";
}

// chipSentence is the accessible name of the chip and the text of its tooltip.
// The small-screen control shows the glyph alone, so this sentence carries the
// label, the age, and the verdict for a reader who cannot see them.
export function chipSentence(read: CatalogSummaryRead): string {
  const posture = postureOf(read);
  if (posture === "authorization") return UNAUTHORIZED_SENTENCE;
  if (posture === "unusable") return "No catalog is available. Open the catalog panel.";
  if (read.summary === undefined) return "The catalog state is loading.";
  const verdict = verdictOf(read.summary.freshness);
  const age = formatAge(read.summary.age_seconds);
  const grade =
    verdict === "fresh"
      ? "The catalog is fresh."
      : verdict === "stale"
        ? "The catalog is stale."
        : "The catalog freshness is unknown.";
  return `${grade} It is ${age} old. Open the catalog panel.`;
}

// FreshnessDot reports liveness. A stale dot carries a short exclamation mark
// inside it, so the state reads without color alone.
function FreshnessDot({ verdict }: { verdict: CatalogVerdict }) {
  const tone =
    verdict === "fresh"
      ? "bg-success text-bg"
      : verdict === "stale"
        ? "bg-warning text-bg"
        : "bg-border-2 text-text-3";
  return (
    <span
      data-testid="catalog-freshness-dot"
      data-verdict={verdict}
      aria-hidden="true"
      className={cn(
        "inline-flex size-2.5 shrink-0 items-center justify-center rounded-full text-[8px] font-bold leading-none",
        tone,
      )}
    >
      {verdict === "stale" ? "!" : ""}
    </span>
  );
}

export type CatalogChipProps = {
  read: CatalogSummaryRead;
  admin: CatalogAdminRead;
  open: boolean;
  onToggle: () => void;
  // small draws the 44 px icon control of the small-screen top bar. The wide
  // chip is 32 px tall and carries its text.
  small?: boolean;
  chipRef?: RefObject<HTMLButtonElement | null>;
};

export function CatalogChip({ read, admin, open, onToggle, small, chipRef }: CatalogChipProps) {
  const posture = postureOf(read);
  const sentence = chipSentence(read);
  const summary = read.summary;
  const status = admin.status;
  const degraded = admin.admin && status?.snapshot?.degraded === true;
  const fallback = admin.admin && (status?.runtime?.fallback ?? summary?.fallback) === true;
  // Only the source this gateway read itself raises the source pill. A problem
  // the upstream reported about its own sources belongs to the hop chain in
  // the panel, not to the chip.
  const sourceDown = admin.admin && status?.source_health === "unavailable";
  const working = admin.admin && admin.working !== undefined;

  const glyph =
    posture === "authorization" ? (
      <Lock data-testid="catalog-authorization-glyph" aria-hidden="true" className="size-3.5 text-text-3" />
    ) : posture === "unusable" ? (
      <CircleSlash data-testid="catalog-unusable-glyph" aria-hidden="true" className="size-3.5 text-error" />
    ) : (
      <FreshnessDot verdict={verdictOf(summary?.freshness)} />
    );

  const label = posture === "unusable" ? "No catalog" : "Catalog";

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <button
            type="button"
            ref={chipRef}
            data-testid="catalog-chip"
            data-posture={posture}
            aria-label={sentence}
            aria-expanded={open}
            onClick={onToggle}
            // A button opens on Enter and on Space. Both are handled here and
            // the default is prevented, so the browser does not also
            // synthesize a click and close what the key just opened, and
            // Space never scrolls the page under the panel.
            onKeyDown={(event) => {
              if (event.key !== "Enter" && event.key !== " ") return;
              event.preventDefault();
              onToggle();
            }}
            className={cn(
              "inline-flex items-center justify-center rounded-full border border-border-2 bg-bg-raised text-xs text-text-2 transition-colors duration-150 ease-standard hover:border-border-1",
              small ? "size-11" : "h-8 gap-2 px-3",
            )}
          />
        }
      >
        {glyph}
        {!small && (
          <>
            <span data-testid="catalog-label">{label}</span>
            {posture === "catalog" && summary !== undefined && (
              <span data-testid="catalog-age" className="text-text-3">
                {formatAge(summary.age_seconds)}
              </span>
            )}
            {degraded && (
              <span data-testid="catalog-degraded-pill">
                <Pill tone="warning">Degraded</Pill>
              </span>
            )}
            {fallback && (
              <span data-testid="catalog-fallback-pill">
                <Pill tone="warning">Fallback</Pill>
              </span>
            )}
            {sourceDown && (
              <span data-testid="catalog-source-down-pill">
                <Pill tone="error">Source down</Pill>
              </span>
            )}
            {working && (
              <LoaderCircle
                data-testid="catalog-activity-icon"
                aria-hidden="true"
                className="size-3.5 animate-spin text-text-3 motion-reduce:animate-none"
              />
            )}
          </>
        )}
      </TooltipTrigger>
      <TooltipContent>{sentence}</TooltipContent>
    </Tooltip>
  );
}
