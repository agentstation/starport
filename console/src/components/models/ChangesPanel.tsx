import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { TableSkeleton } from "@/components/ui/skeleton";
import { RelativeTime } from "@/components/ui/RelativeTime";
import {
  accessMessage,
  ApiError,
  type CatalogChanges,
  type OfferingChange,
} from "@/lib/api";
import { queries } from "@/lib/queries";
import {
  formatUSD,
  shortGenerationID,
} from "@/lib/format";

// Diff prices arrive as per-million numbers already. An absent side is
// unknown, and an unknown price renders the dash, never $0.
function formatPerM(perM: number | undefined): string {
  return formatUSD(perM) ?? "—";
}

// sentence turns a server reason into a sentence for the reader: an
// initial capital and a closing period. An absent reason stays absent so
// the caller's fallback copy renders instead of an empty line.
function sentence(reason: string | undefined): string | undefined {
  if (!reason) return undefined;
  const trimmed = reason.trim();
  if (trimmed === "") return undefined;
  const capitalized = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
  return capitalized.endsWith(".") ? capitalized : `${capitalized}.`;
}

// NoChanges is the one empty state for a diff with nothing in it. The
// reader opened "What changed" to learn whether the catalog moved, so the
// answer leads and the detail explains what the comparison covered.
function NoChanges({ detail }: { detail: string }) {
  return (
    <div data-testid="no-changes" className="mt-3 flex flex-col gap-1">
      <p className="text-base text-text-1">
        No changes since the previous generation.
      </p>
      <p className="text-sm text-text-3">{detail}</p>
    </div>
  );
}

function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <div className="mb-2 mt-5 text-sm font-medium text-text-2">{children}</div>
  );
}

function groupOfferingsByProvider(diff: CatalogChanges) {
  const byProvider = new Map<
    string,
    { added: OfferingChange[]; removed: OfferingChange[] }
  >();
  const entry = (provider: string) => {
    let existing = byProvider.get(provider);
    if (!existing) {
      existing = { added: [], removed: [] };
      byProvider.set(provider, existing);
    }
    return existing;
  };
  for (const change of diff.offerings_added ?? []) {
    entry(change.provider).added.push(change);
  }
  for (const change of diff.offerings_removed ?? []) {
    entry(change.provider).removed.push(change);
  }
  return new Map(
    [...byProvider.entries()].sort((a, b) => a[0].localeCompare(b[0])),
  );
}

function ChangesBody() {
  const changes = useQuery({
    ...queries.catalogChanges(),
  });

  if (changes.isPending) {
    return <TableSkeleton columns={3} rows={4} />;
  }
  if (changes.error) {
    const error = changes.error;
    const message =
      error instanceof ApiError && error.needsKey
        ? accessMessage(error, "models:read")
        : `Failed to load catalog changes: ${error.message}`;
    return <p className="text-base text-text-3">{message}</p>;
  }

  const diff = changes.data;
  if (!diff?.available) {
    return (
      <div data-testid="no-history" className="flex flex-col gap-1">
        <p className="text-base text-text-1">Nothing to compare yet.</p>
        <p className="text-sm text-text-3">
          {sentence(diff?.reason) ??
            "The diff needs two accepted generations. Refresh the catalog after the next Starmap release to record a second one."}
        </p>
      </div>
    );
  }

  const header = (
    <div className="font-mono text-xs text-text-3">
      {shortGenerationID(diff.from_generation_id)} →{" "}
      {shortGenerationID(diff.to_generation_id)}
      {diff.to_generated_at ? (
        <>
          {" · "}
          <RelativeTime iso={diff.to_generated_at} />
        </>
      ) : (
        ""
      )}
    </div>
  );

  if (diff.semantically_equal) {
    return (
      <>
        {header}
        <NoChanges detail="Models, offerings, and prices match. Only acquisition metadata differs between the two generations." />
      </>
    );
  }

  const byProvider = groupOfferingsByProvider(diff);
  const modelList = (title: string, ids: string[] | undefined, tone: string) =>
    ids?.length ? (
      <>
        <SectionTitle>
          {title} ({ids.length})
        </SectionTitle>
        <ul className="flex flex-col gap-1">
          {ids.map((id) => (
            <li key={id} className={`font-mono text-xs ${tone}`}>
              {id}
            </li>
          ))}
        </ul>
      </>
    ) : null;

  const hasContent =
    diff.models_added?.length ||
    diff.models_removed?.length ||
    byProvider.size ||
    diff.price_changes?.length;

  return (
    <>
      {header}
      {modelList("Models added", diff.models_added, "text-success")}
      {modelList("Models removed", diff.models_removed, "text-error")}
      {byProvider.size > 0 && (
        <>
          <SectionTitle>Offerings by provider</SectionTitle>
          <div className="flex flex-col gap-1.5">
            {[...byProvider.entries()].map(([provider, entry]) => (
              <div key={provider}>
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs text-text-2">{provider}</span>
                  <span className="text-xs text-text-4">
                    {[
                      entry.added.length ? `+${entry.added.length} added` : null,
                      entry.removed.length ? `−${entry.removed.length} removed` : null,
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                </div>
                {entry.added.map((change) => (
                  <div
                    key={`+${change.provider_model_id}-${change.definition_id}`}
                    className="pl-3 font-mono text-xs text-success"
                  >
                    + {change.provider_model_id}
                    {change.definition_id && (
                      <span className="text-text-4"> ({change.definition_id})</span>
                    )}
                  </div>
                ))}
                {entry.removed.map((change) => (
                  <div
                    key={`−${change.provider_model_id}-${change.definition_id}`}
                    className="pl-3 font-mono text-xs text-error"
                  >
                    − {change.provider_model_id}
                    {change.definition_id && (
                      <span className="text-text-4"> ({change.definition_id})</span>
                    )}
                  </div>
                ))}
              </div>
            ))}
          </div>
        </>
      )}
      {diff.price_changes && diff.price_changes.length > 0 && (
        <>
          <SectionTitle>Price changes ({diff.price_changes.length})</SectionTitle>
          <div className="overflow-x-auto">
            <table className="w-full text-left">
              <thead>
                <tr className="border-b border-border-1 text-xs font-medium text-text-3">
                  <th scope="col" className="py-2.5 pr-3">Offering</th>
                  <th scope="col" className="py-2.5 pr-3">Field</th>
                  <th scope="col" className="py-2.5 text-right">/ M</th>
                </tr>
              </thead>
              <tbody>
                {diff.price_changes.map((change) => (
                  <tr
                    key={`${change.provider}/${change.provider_model_id}/${change.field}`}
                    className="border-b border-border-1"
                  >
                    <td className="py-2.5 pr-3 font-mono text-xs text-text-2">
                      {change.provider}/{change.provider_model_id}
                    </td>
                    <td className="py-2.5 pr-3 text-xs text-text-3">{change.field}</td>
                    <td className="py-2.5 text-right font-mono text-xs tabular-nums text-text-2">
                      {formatPerM(change.previous_per_1m)} →{" "}
                      {formatPerM(change.current_per_1m)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
      {!hasContent && (
        <NoChanges detail="The two generations list the same models, offerings, and prices." />
      )}
    </>
  );
}

export function ChangesPanel({ onClose }: { onClose: () => void }) {
  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent>
        <SheetHeader>
          <SheetTitle>Catalog changes</SheetTitle>
        </SheetHeader>
        <SheetBody>
          <ChangesBody />
        </SheetBody>
      </SheetContent>
    </Sheet>
  );
}
