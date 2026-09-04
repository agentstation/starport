import { useEffect, useRef, useState, type ReactNode } from "react";

import { CatalogChangesSection } from "@/components/shell/CatalogChanges";
import { CatalogChip, verdictOf } from "@/components/shell/CatalogChip";
import {
  ADMIN_ONLY_SENTENCE,
  UNAUTHORIZED_SENTENCE,
  useCatalogAdminStatus,
  useCatalogRefresh,
  useCatalogSummary,
  type CatalogAdminRead,
  type CatalogSummaryRead,
} from "@/components/shell/CatalogSummary";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/CopyButton";
import { Pill } from "@/components/ui/Pill";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import type { CatalogAdminStatus, CatalogSourceObservation } from "@/lib/api";
import { formatAge, formatCount, shortGenerationID } from "@/lib/format";

// CatalogPanel is the detail the chip opens. It answers, in order, what this
// gateway serves, what moved, what the layers below the served catalog are,
// how far the publication chain reaches, when the next update lands, which
// sources answered, and what an operator can do now.
//
// Two of the seven sections belong to every reader who reads models. The other
// five need an admin session, and a reader without one gets one sentence that
// states the scope rather than an error.

// PANEL_WIDTH bounds the panel to the viewport. A wide screen gets the 480 px
// sheet; a screen narrower than that gets the whole width and no horizontal
// scroll.
export const PANEL_WIDTH = "w-[min(480px,100vw)]";

// hopLabel says where the health of one hop comes from. This gateway observed
// the hop directly above it. Every hop beyond that one is a fact the upstream
// reported, and the panel never restates a reported fact as an observation of
// its own.
export function hopLabel(index: number): "direct" | "upstream-reported" {
  return index === 0 ? "direct" : "upstream-reported";
}

// observationSummary counts the acquisition outcomes of one generation, so the
// layers figure states "14 succeeded, 2 skipped, 1 failed" instead of a list
// no reader scans. An empty set reads as no observation, which is a fact and
// not a zero.
export function observationSummary(observations: CatalogSourceObservation[] | undefined): string {
  if (!observations || observations.length === 0) return "No provider observation is recorded.";
  const counts = new Map<string, number>();
  for (const observation of observations) {
    const status = observation.status ?? "unknown";
    counts.set(status, (counts.get(status) ?? 0) + 1);
  }
  return [...counts.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([status, count]) => `${count} ${status}`)
    .join(", ");
}

function Section({
  title,
  testid,
  children,
}: {
  title: string;
  testid: string;
  children: ReactNode;
}) {
  return (
    <section data-testid={testid} className="border-b border-border-1 py-4 last:border-b-0">
      <h3 className="mb-2 text-sm font-medium text-text-2">{title}</h3>
      {children}
    </section>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-0.5 text-xs">
      <span className="text-text-3">{label}</span>
      <span className="text-right text-text-1">{children}</span>
    </div>
  );
}

// LayerNode is one step of the layers figure. The figure is a vertical list,
// so a reader sees the whole stack under the served catalog at once.
function LayerNode({
  testid,
  name,
  detail,
  mark,
}: {
  testid: string;
  name: string;
  detail: string;
  mark?: string;
}) {
  return (
    <li data-testid={testid} className="flex items-baseline gap-2 border-l border-border-2 py-1 pl-3">
      <span className="text-xs text-text-1">{name}</span>
      <span className="font-mono text-xs text-text-3">{detail}</span>
      {mark && <Pill tone="warning">{mark}</Pill>}
    </li>
  );
}

function IdentitySection({
  read,
  status,
}: {
  read: CatalogSummaryRead;
  status: CatalogAdminStatus | undefined;
}) {
  const summary = read.summary;
  const snapshot = status?.snapshot;
  return (
    <Section title="Identity" testid="catalog-identity">
      <Row label="Generation">
        <span data-testid="catalog-identity-generation" className="font-mono">
          {shortGenerationID(summary?.generation_id)}
        </span>
      </Row>
      <Row label="Generated">
        <RelativeTime iso={summary?.generated_at} />
      </Row>
      <Row label="Age">{formatAge(summary?.age_seconds)}</Row>
      <Row label="Freshness">
        <span data-testid="catalog-identity-verdict">{verdictOf(summary?.freshness)}</span>
      </Row>
      <Row label="Models">{formatCount(summary?.models)}</Row>
      <Row label="Providers">{formatCount(summary?.providers)}</Row>
      <Row label="Next update">
        <span data-testid="catalog-next-update">
          <RelativeTime iso={summary?.next_update_at ?? status?.next_update_at} />
        </span>
      </Row>
      {snapshot?.payload_checksum && (
        <Row label="Payload checksum">
          <span className="font-mono">{shortGenerationID(snapshot.payload_checksum)}</span>
        </Row>
      )}
      {snapshot?.catalog_sequence !== undefined && (
        <Row label="Catalog sequence">{snapshot.catalog_sequence}</Row>
      )}
      {snapshot?.availability_revision !== undefined && (
        <Row label="Availability revision">{snapshot.availability_revision}</Row>
      )}
    </Section>
  );
}

// LayersSection draws the stack the effective catalog rests on. The embedded
// baseline is always the first node, because a Starport binary always carries
// one, and a reader who sees no upstream still learns what serves.
function LayersSection({ status }: { status: CatalogAdminStatus | undefined }) {
  const runtime = status?.runtime;
  const upstream = status?.provenance?.upstream;
  const identity = upstream?.source_identity ?? "none";
  return (
    <Section title="Layers" testid="catalog-layers">
      <ol className="flex flex-col">
        <LayerNode
          testid="catalog-layer-embedded"
          name="Embedded baseline"
          // The routes CAT8 shipped do not report the generation that ships in
          // the binary, so the node states the layer and says what it lacks.
          detail={runtime?.source_kind === "embedded" ? "serving now" : "generation not reported"}
        />
        <LayerNode
          testid="catalog-layer-upstream"
          name="Selected upstream"
          detail={identity}
          mark={runtime?.fallback ? "retained" : undefined}
        />
        <LayerNode
          testid="catalog-layer-observations"
          name="Local observations"
          detail={observationSummary(status?.snapshot?.source_observations)}
        />
        <LayerNode
          testid="catalog-layer-effective"
          name="Effective catalog"
          detail={shortGenerationID(status?.provenance?.effective?.generation_id)}
        />
      </ol>
    </Section>
  );
}

// HopsSection draws the publication chain above this gateway. Only the first
// hop carries health, because it is the only one this gateway read itself.
function HopsSection({ status }: { status: CatalogAdminStatus | undefined }) {
  const chain = status?.provenance?.upstream?.chain ?? [];
  return (
    <Section title="Upstream hops" testid="catalog-hops">
      <p data-testid="catalog-hop-count" className="mb-2 text-xs text-text-3">
        {chain.length === 1 ? "1 hop" : `${chain.length} hops`}
      </p>
      {chain.length === 0 ? (
        <p className="text-xs text-text-3">No upstream hop is reported.</p>
      ) : (
        <ol className="flex flex-col">
          {chain.map((hop, index) => {
            const label = hopLabel(index);
            return (
              <li
                key={`${hop.identity ?? "hop"}-${index}`}
                data-testid={`catalog-hop-${index}`}
                data-hop-label={label}
                className="flex items-baseline gap-2 border-l border-border-2 py-1 pl-3 text-xs"
              >
                <span className="font-mono text-text-1">{hop.identity ?? "unknown"}</span>
                <span data-testid={`catalog-hop-${index}-label`} className="text-text-3">
                  {label === "direct" ? "Direct" : "Reported by upstream"}
                </span>
                {label === "direct" ? (
                  <span data-testid="catalog-hop-health" className="text-text-2">
                    {status?.source_health ?? hop.health ?? "unknown"}
                  </span>
                ) : (
                  <RelativeTime iso={hop.observed_at} className="text-text-3" />
                )}
              </li>
            );
          })}
        </ol>
      )}
    </Section>
  );
}

function ScheduleSection({ status }: { status: CatalogAdminStatus | undefined }) {
  const freshness = status?.freshness;
  return (
    <Section title="Schedule" testid="catalog-schedule">
      <Row label="Next update">
        <RelativeTime iso={status?.next_update_at} />
      </Row>
      <Row label="Last observed">
        <RelativeTime iso={status?.runtime?.observed_at} />
      </Row>
      <Row label="Catalog grade">{freshness?.catalog ?? "unknown"}</Row>
      <Row label="Channel grade">{freshness?.channel ?? "unknown"}</Row>
      <Row label="Source check grade">{freshness?.source_check ?? "unknown"}</Row>
    </Section>
  );
}

function ProvidersSection({ status }: { status: CatalogAdminStatus | undefined }) {
  const observations = status?.snapshot?.source_observations ?? [];
  return (
    <Section title="Providers" testid="catalog-providers">
      {observations.length === 0 ? (
        <p className="text-xs text-text-3">No provider observation is recorded.</p>
      ) : (
        <ul className="flex flex-col">
          {observations.map((observation) => (
            <li
              key={observation.source}
              className="flex items-baseline justify-between gap-3 py-0.5 text-xs"
            >
              <span className="font-mono text-text-1">{observation.source}</span>
              <span className="text-text-3">
                {observation.status ?? "unknown"}
                {observation.completeness ? ` · ${observation.completeness}` : ""}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

function ActionsSection({ admin, status }: { admin: CatalogAdminRead; status: string }) {
  const { start, cancel } = useCatalogRefresh();
  const working = admin.working;
  return (
    <Section title="Actions" testid="catalog-actions">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant="secondary"
          disabled={working !== undefined || start.isPending}
          onClick={() => start.mutate()}
        >
          Refresh catalog
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={working === undefined || cancel.isPending}
          onClick={() => working && cancel.mutate(working.id)}
        >
          Cancel refresh
        </Button>
        <CopyButton text={status} label="status" />
      </div>
    </Section>
  );
}

export type CatalogPanelProps = {
  read: CatalogSummaryRead;
  admin: CatalogAdminRead;
  open: boolean;
  onOpenChange: (open: boolean) => void;
};

export function CatalogPanel({ read, admin, open, onOpenChange }: CatalogPanelProps) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent data-testid="catalog-panel" className={PANEL_WIDTH}>
        <SheetHeader>
          <SheetTitle>Catalog</SheetTitle>
        </SheetHeader>
        <SheetBody>
          {read.refused ? (
            <p data-testid="catalog-unauthorized" className="text-sm text-text-2">
              {UNAUTHORIZED_SENTENCE}
            </p>
          ) : (
            <>
              <IdentitySection read={read} status={admin.status} />
              <Section title="Changes" testid="catalog-changes">
                <CatalogChangesSection />
              </Section>
              {admin.admin ? (
                <>
                  <LayersSection status={admin.status} />
                  <HopsSection status={admin.status} />
                  <ScheduleSection status={admin.status} />
                  <ProvidersSection status={admin.status} />
                  <ActionsSection admin={admin} status={JSON.stringify(admin.status, null, 2)} />
                </>
              ) : (
                <p data-testid="catalog-admin-only" className="py-4 text-xs text-text-3">
                  {ADMIN_ONLY_SENTENCE}
                </p>
              )}
            </>
          )}
        </SheetBody>
      </SheetContent>
    </Sheet>
  );
}

// CatalogIndicator is the whole catalog surface of the shell: the chip, the
// panel it opens, and the one summary read behind both. The shell mounts one
// of these, so the console holds one catalog query however many routes a
// reader walks through.
export function CatalogIndicator({ small }: { small?: boolean }) {
  const [open, setOpen] = useState(false);
  const chipRef = useRef<HTMLButtonElement | null>(null);
  const read = useCatalogSummary();
  const admin = useCatalogAdminStatus(read, open);

  // The reader opened the panel from the chip, so the chip is where the
  // reader returns. Escape, the close control, and a click outside all end at
  // the same place.
  const wasOpen = useRef(false);
  useEffect(() => {
    if (wasOpen.current && !open) chipRef.current?.focus();
    wasOpen.current = open;
  }, [open]);

  return (
    <>
      <CatalogChip
        read={read}
        admin={admin}
        open={open}
        small={small}
        chipRef={chipRef}
        onToggle={() => setOpen((current) => !current)}
      />
      <CatalogPanel read={read} admin={admin} open={open} onOpenChange={setOpen} />
    </>
  );
}
