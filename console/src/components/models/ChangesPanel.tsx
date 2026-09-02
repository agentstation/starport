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
      <p className="text-base text-text-3">
        {diff?.reason ??
          "No generation history to compare yet. Refresh the catalog twice to build one."}
      </p>
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
        <p className="mt-3 text-base text-text-2">
          The last two generations are semantically equal: no models, offerings,
          or prices changed. Only acquisition metadata differs.
        </p>
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
                  <th className="py-1.5 pr-3 font-medium">offering</th>
                  <th className="py-1.5 pr-3 font-medium">field</th>
                  <th className="py-1.5 text-right font-medium">/ M</th>
                </tr>
              </thead>
              <tbody>
                {diff.price_changes.map((change) => (
                  <tr
                    key={`${change.provider}/${change.provider_model_id}/${change.field}`}
                    className="border-b border-border-1"
                  >
                    <td className="py-1.5 pr-3 font-mono text-xs text-text-2">
                      {change.provider}/{change.provider_model_id}
                    </td>
                    <td className="py-1.5 pr-3 text-xs text-text-3">{change.field}</td>
                    <td className="py-1.5 text-right font-mono text-xs tabular-nums text-text-2">
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
        <p className="mt-3 text-base text-text-3">
          No model, offering, or price differences.
        </p>
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
