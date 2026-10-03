import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { useCatalogRefresh, type CatalogAdminRead } from "@/components/shell/CatalogSummary";
import { Button } from "@/components/ui/button";
import { Pill, type PillTone } from "@/components/ui/Pill";
import { ApiError, type CatalogOperation } from "@/lib/api";
import { shortGenerationID } from "@/lib/format";
import { queries } from "@/lib/queries";

// CatalogRefresh is the one catalog action of the panel. It names its effect
// before the click and follows the run it started, or the run in flight,
// through GET /api/v1/admin/catalog/refreshes/{run_id} until the run closes.

// REFRESH_EFFECT states what Refresh sources does. A pin is a separate effect,
// and it is a configuration setting, so the panel points to it instead of
// writing configuration itself.
export const REFRESH_EFFECT =
  "Reads the catalog source and the provider sources again. A valid result becomes the new catalog generation. Requests use the current catalog until then.";

export const PIN_POINTER = "To keep one generation, set the generation pin in Settings, Configuration.";

// RUN_STATE maps the closed run states to the words the panel shows.
export const RUN_STATE: Record<string, { word: string; tone: PillTone }> = {
  accepted: { word: "Queued", tone: "neutral" },
  running: { word: "Running", tone: "info" },
  succeeded: { word: "Done", tone: "success" },
  failed: { word: "Failed", tone: "error" },
  canceled: { word: "Canceled", tone: "warning" },
};

// RUN_REASON maps the closed reason set to a cause in words.
export const RUN_REASON: Record<string, string> = {
  source_unavailable: "the catalog source answered nothing usable",
  timed_out: "the run reached its time limit",
  canceled: "an operator or a shutdown ended the run",
  stale_lease_epoch: "this instance lost the runtime lease during validation",
  route_validation_failed: "the new catalog did not become routable",
  accepted_head_conflict: "another instance accepted a catalog first",
  catalog_unavailable: "no catalog generation is present",
  runtime_generation_capacity: "retained requests delay the activation of a new generation",
  internal_error: "an internal failure with no safe cause",
};

function open(operation: CatalogOperation | undefined): boolean {
  return operation?.state === "accepted" || operation?.state === "running";
}

// runFacts states one run in sentences. It reads only the operation.
export function runFacts(operation: CatalogOperation): string[] {
  const facts: string[] = [];
  if (operation.joined) facts.push("This request joined the run in flight. Overlapping requests share one run.");
  if (operation.state === "succeeded") {
    facts.push(
      operation.changed
        ? `The run accepted generation ${shortGenerationID(operation.generation_id)}.`
        : "The sources had no change. The current catalog stays.",
    );
  }
  if (operation.reason) facts.push(`Cause: ${RUN_REASON[operation.reason] ?? operation.reason}.`);
  const permission = operation.permission_at_completion;
  if (!open(operation) && permission) {
    facts.push(
      `At completion the catalog ${permission.new_attempts_allowed ? "admitted" : "did not admit"} new requests${
        permission.admitted_streams_may_finish ? ", and admitted streams could finish" : ""
      }.`,
    );
  }
  return facts;
}

export function CatalogRefresh({ admin }: { admin: CatalogAdminRead }) {
  const { start, cancel } = useCatalogRefresh();
  const [started, setStarted] = useState<CatalogOperation | undefined>();
  const runID = started?.id ?? admin.working?.id;
  const run = useQuery({ ...queries.catalogRefresh(runID ?? ""), enabled: runID !== undefined });
  // The answer of the start carries joined, and the run route does not.
  const operation: CatalogOperation | undefined = run.data
    ? { ...run.data, joined: started?.id === run.data.id ? started.joined : undefined }
    : (started ?? admin.working);
  const working = open(operation) || admin.working !== undefined;
  const state = operation?.state ? RUN_STATE[operation.state] : undefined;
  const pruned = run.error instanceof ApiError && run.error.status === 404;

  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-text-2">{REFRESH_EFFECT}</p>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant="secondary"
          disabled={working || start.isPending}
          onClick={() => start.mutate(undefined, { onSuccess: setStarted })}
        >
          Refresh sources
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={!working || !operation || cancel.isPending}
          onClick={() => operation && cancel.mutate(operation.id, { onSuccess: setStarted })}
        >
          Cancel refresh
        </Button>
      </div>
      <div data-testid="catalog-refresh-run" aria-live="polite" className="flex flex-col gap-1 text-xs text-text-2">
        {start.error && <p className="text-error">The gateway did not accept the refresh: {start.error.message}</p>}
        {operation && (
          <p className="flex items-center gap-2">
            <span className="font-mono text-text-3">Run {operation.id.slice(0, 8)}</span>
            {state ? <Pill tone={state.tone}>{state.word}</Pill> : <span>{operation.state ?? "unknown"}</span>}
          </p>
        )}
        {pruned && <p>The gateway no longer keeps this run.</p>}
        {operation && runFacts(operation).map((fact) => <p key={fact}>{fact}</p>)}
      </div>
      <p className="text-xs text-text-3">{PIN_POINTER}</p>
    </div>
  );
}
