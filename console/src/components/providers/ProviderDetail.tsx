import { Link } from "@tanstack/react-router";
import { KeyRound } from "lucide-react";

import type {
  ActivityRecord,
  ProviderCatalogEntry,
  ProviderOfferingStatus,
  ProviderRuntimeStatus,
} from "@/lib/api";
import { formatCount, formatMs, formatRelativeTime } from "@/lib/format";

// One circuit vocabulary (internal/availability): healthy and half_open
// admit attempts; open and unavailable reject them.
const CIRCUIT_TONES: Record<string, string> = {
  healthy: "bg-success-tint text-success",
  half_open: "bg-warning-tint text-warning",
  open: "bg-error-tint text-error",
  unavailable: "bg-bg-raised text-text-3",
};

function CircuitChip({ state }: { state: string | undefined }) {
  const value = state ?? "unknown";
  return (
    <span
      className={`inline-flex h-5 items-center whitespace-nowrap rounded-xs px-1.5 text-xs font-medium ${
        CIRCUIT_TONES[value] ?? "bg-bg-raised text-text-3"
      }`}
    >
      {value.replaceAll("_", " ")}
    </span>
  );
}

// --- Policy chips: catalog-declared data-handling facts. Only declared
// facts render — an absent field stays silent instead of guessing.

export function policyChips(entry: ProviderCatalogEntry | undefined): string[] {
  const chips: string[] = [];
  const policies = entry?.policies;
  if (policies?.retains_data === true) chips.push("retains data");
  if (policies?.retains_data === false) chips.push("no data retention");
  if (policies?.trains_on_data === true) chips.push("trains on data");
  if (policies?.trains_on_data === false) chips.push("no training on data");
  if (policies?.retention) chips.push(`retention: ${policies.retention}`);
  if (policies?.moderated === true) chips.push("moderated");
  if (entry?.headquarters) chips.push(`HQ: ${entry.headquarters}`);
  return chips;
}

export function PolicyChips({ entry }: { entry: ProviderCatalogEntry | undefined }) {
  const chips = policyChips(entry);
  if (chips.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {chips.map((chip) => (
        <span
          key={chip}
          title={chip}
          className="inline-flex h-5 max-w-96 items-center rounded-xs bg-bg-raised px-1.5 text-xs text-text-3"
        >
          <span className="truncate">{chip}</span>
        </span>
      ))}
    </div>
  );
}

// --- Credential panel: the operator credential state with a fix-it
// path. Operator credentials come from the environment (README:
// <PROVIDER>_API_KEY, then STARPORT_<PROVIDER>_API_KEY); callers can
// also attach their own key to a gateway key on the API Keys page.

export function operatorEnvNames(providerId: string): [string, string] {
  const stem = providerId.toUpperCase().replaceAll("-", "_");
  return [`${stem}_API_KEY`, `STARPORT_${stem}_API_KEY`];
}

export function CredentialPanel({
  providerId,
  credential,
}: {
  providerId: string;
  credential: ProviderRuntimeStatus["operator_credential"];
}) {
  const state = credential?.state ?? "not_configured";
  const usable = credential?.usable === true;
  const [envName, prefixedEnvName] = operatorEnvNames(providerId);
  return (
    <section
      data-testid="credential-panel"
      className="flex flex-col gap-2 rounded-md border border-border-1 bg-bg-panel p-4"
    >
      <h2 className="text-xs font-medium uppercase tracking-wide text-text-3">
        Credential
      </h2>
      {usable ? (
        <p className="text-sm text-text-2">
          Operator credential is ready
          {credential?.updated_at
            ? ` · updated ${formatRelativeTime(credential.updated_at)}`
            : ""}
          .
        </p>
      ) : (
        <>
          <p className="text-sm text-text-2">
            {state === "not_configured"
              ? "No operator credential is configured for this provider."
              : `The operator credential is ${state.replaceAll("_", " ")}${
                  credential?.reason
                    ? ` (${credential.reason.replaceAll("_", " ")})`
                    : ""
                }.`}
          </p>
          <p className="text-sm text-text-3">
            Set{" "}
            <code className="font-mono text-xs text-text-2">{envName}</code> or{" "}
            <code className="font-mono text-xs text-text-2">{prefixedEnvName}</code>{" "}
            in the gateway environment, or attach your own provider key to a
            gateway key.
          </p>
          <Link
            to="/keys"
            className="flex w-fit items-center gap-1.5 text-sm text-accent-link transition-colors duration-150 ease-standard hover:underline"
          >
            <KeyRound className="size-3.5" />
            Manage API Keys
          </Link>
        </>
      )}
    </section>
  );
}

// --- Health panel: circuit-state breakdown plus a rolling window of
// live routing data when the activity log is readable.

export type ActivityStats = {
  requests: number;
  errorRate: number;
  p50LatencyMs: number | undefined;
  p95LatencyMs: number | undefined;
  medianRoutingMs: number | undefined;
};

function percentile(sorted: number[], fraction: number): number | undefined {
  if (sorted.length === 0) return undefined;
  const index = Math.min(
    sorted.length - 1,
    Math.ceil(fraction * sorted.length) - 1,
  );
  return sorted[Math.max(0, index)];
}

export function activityStats(records: ActivityRecord[]): ActivityStats {
  const latencies = records
    .map((record) => record.latency_ms)
    .filter((value): value is number => typeof value === "number")
    .sort((a, b) => a - b);
  const routing = records
    .map((record) => record.routing_ms)
    .filter((value): value is number => typeof value === "number")
    .sort((a, b) => a - b);
  const errors = records.filter((record) => record.status === "error").length;
  return {
    requests: records.length,
    errorRate: records.length === 0 ? 0 : errors / records.length,
    p50LatencyMs: percentile(latencies, 0.5),
    p95LatencyMs: percentile(latencies, 0.95),
    medianRoutingMs: percentile(routing, 0.5),
  };
}

export function circuitBreakdown(
  offerings: ProviderOfferingStatus[],
): Array<[string, number]> {
  const counts = new Map<string, number>();
  for (const offering of offerings) {
    const state = offering.state ?? "unknown";
    counts.set(state, (counts.get(state) ?? 0) + 1);
  }
  const order = ["healthy", "half_open", "open", "unavailable"];
  return [...counts.entries()].sort(
    ([a], [b]) =>
      (order.indexOf(a) + 1 || order.length + 1) -
      (order.indexOf(b) + 1 || order.length + 1),
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-text-4">{label}</span>
      <span className="text-sm tabular-nums text-text-1">{value}</span>
    </div>
  );
}

export function HealthPanel({
  offerings,
  records,
}: {
  offerings: ProviderOfferingStatus[];
  records: ActivityRecord[] | undefined;
}) {
  const stats = records ? activityStats(records) : undefined;
  return (
    <section
      data-testid="health-panel"
      className="flex flex-col gap-3 rounded-md border border-border-1 bg-bg-panel p-4"
    >
      <h2 className="text-xs font-medium uppercase tracking-wide text-text-3">
        Health
      </h2>
      <div className="flex flex-wrap items-center gap-2">
        {circuitBreakdown(offerings).map(([state, count]) => (
          <span key={state} className="flex items-center gap-1.5 text-sm text-text-2">
            <CircuitChip state={state} />
            <span className="tabular-nums">{formatCount(count)}</span>
          </span>
        ))}
        {offerings.length === 0 && (
          <span className="text-sm text-text-3">No offerings reported.</span>
        )}
      </div>
      {stats && stats.requests > 0 ? (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Stat label="Requests (1h)" value={formatCount(stats.requests)} />
          <Stat
            label="Error rate"
            value={`${(stats.errorRate * 100).toFixed(1)}%`}
          />
          <Stat label="p50 latency" value={formatMs(stats.p50LatencyMs)} />
          <Stat label="p95 latency" value={formatMs(stats.p95LatencyMs)} />
        </div>
      ) : (
        <p className="text-sm text-text-3">
          No requests through this provider in the last hour.
        </p>
      )}
    </section>
  );
}

// --- Served models: every offering the runtime tracks, with its
// circuit state. Rows link into the models list filtered to this
// provider until the model detail page (CP9) exists.

export function OfferingsTable({
  providerId,
  offerings,
}: {
  providerId: string;
  offerings: ProviderOfferingStatus[];
}) {
  if (offerings.length === 0) return null;
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-text-3">
        Served models
      </h2>
      <div className="overflow-x-auto rounded-md border border-border-1">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border-1 text-left text-xs text-text-4">
              <th className="px-3 py-2 font-medium">Model</th>
              <th className="px-3 py-2 font-medium">Circuit</th>
              <th className="px-3 py-2 font-medium">Reason</th>
            </tr>
          </thead>
          <tbody>
            {offerings.map((offering) => (
              <tr
                key={offering.provider_model_id}
                className="border-b border-border-1 last:border-b-0"
              >
                <td className="px-3 py-2">
                  <Link
                    to="/models"
                    search={{
                      provider: providerId,
                      q: offering.provider_model_id,
                    }}
                    className="font-mono text-xs text-text-1 transition-colors duration-150 ease-standard hover:text-accent-link"
                  >
                    {offering.provider_model_id}
                  </Link>
                </td>
                <td className="px-3 py-2">
                  <CircuitChip state={offering.state} />
                </td>
                <td className="px-3 py-2 text-xs text-text-3">
                  {offering.reason ? offering.reason.replaceAll("_", " ") : "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
