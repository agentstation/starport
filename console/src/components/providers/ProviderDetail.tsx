import { Link } from "@tanstack/react-router";

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

// One routing vocabulary (internal/providers/state): routable offerings carry
// a route, unroutable ones name the filter that dropped them, and unknown
// means no planning generation has covered the offering yet.
const ROUTING_TONES: Record<string, string> = {
  routable: "bg-success-tint text-success",
  unroutable: "bg-warning-tint text-warning",
  unknown: "bg-bg-raised text-text-3",
};

function RoutingChip({ routing }: { routing: ProviderOfferingStatus["routing"] }) {
  const state = routing?.state ?? "unknown";
  const label =
    state === "unroutable" && routing?.reason
      ? `unroutable · ${routing.reason.replaceAll("_", " ")}`
      : state;
  return (
    <span
      className={`inline-flex h-5 items-center whitespace-nowrap rounded-xs px-1.5 text-xs font-medium ${
        ROUTING_TONES[state] ?? "bg-bg-raised text-text-3"
      }`}
    >
      {label}
    </span>
  );
}

// reachableSummary counts the offerings a request can actually reach. An
// advertised offering the planner drops is the failure this answers: the
// provider looks healthy while most of its catalog is unusable.
export function reachableSummary(
  offerings: ProviderOfferingStatus[],
): { routable: number; unroutable: number; known: number } {
  let routable = 0;
  let unroutable = 0;
  for (const offering of offerings) {
    if (offering.routing?.state === "routable") routable += 1;
    if (offering.routing?.state === "unroutable") unroutable += 1;
  }
  return { routable, unroutable, known: routable + unroutable };
}

// --- Data policy: catalog-declared data-handling facts, rendered as a
// summary list (label · value rows) so the retention prose reads as a
// sentence instead of truncating inside a chip. Only declared facts render —
// an absent field stays silent instead of guessing.

export type PolicyFact = { label: string; value: string; href?: string };

export function policyFacts(
  entry: ProviderCatalogEntry | undefined,
): PolicyFact[] {
  const facts: PolicyFact[] = [];
  const policies = entry?.policies;
  // The retention prose is the full answer when the catalog carries one;
  // the boolean is the fallback verdict, never a second row beside it.
  if (policies?.retention) {
    facts.push({ label: "Retention", value: policies.retention });
  } else if (policies?.retains_data === true) {
    facts.push({ label: "Retention", value: "Retains API data" });
  } else if (policies?.retains_data === false) {
    facts.push({ label: "Retention", value: "Does not retain API data" });
  }
  if (policies?.trains_on_data === true) {
    facts.push({ label: "Training", value: "May train on your data" });
  }
  if (policies?.trains_on_data === false) {
    facts.push({ label: "Training", value: "Does not train on your data" });
  }
  if (policies?.moderated === true) {
    facts.push({ label: "Moderation", value: "Moderated by the provider" });
  }
  if (entry?.headquarters) {
    facts.push({ label: "Headquarters", value: entry.headquarters });
  }
  if (policies?.privacy_policy_url) {
    facts.push({
      label: "Privacy policy",
      value: policies.privacy_policy_url,
      href: policies.privacy_policy_url,
    });
  }
  if (policies?.terms_of_service_url) {
    facts.push({
      label: "Terms of service",
      value: policies.terms_of_service_url,
      href: policies.terms_of_service_url,
    });
  }
  return facts;
}

export function PolicySummary({
  entry,
}: {
  entry: ProviderCatalogEntry | undefined;
}) {
  const facts = policyFacts(entry);
  if (facts.length === 0) return null;
  return (
    <section data-testid="policy-summary" className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-text-3">
        Data policy
      </h2>
      <dl className="flex flex-col divide-y divide-border-1 rounded-md border border-border-1 bg-bg-panel">
        {facts.map((fact) => (
          <div key={fact.label} className="flex gap-4 px-4 py-2.5">
            <dt className="w-32 shrink-0 text-sm text-text-4">{fact.label}</dt>
            <dd className="min-w-0 text-sm text-text-2">
              {fact.href ? (
                <a
                  href={fact.href}
                  target="_blank"
                  rel="noreferrer"
                  className="break-all text-accent-link transition-colors duration-150 ease-standard hover:underline"
                >
                  {fact.value}
                </a>
              ) : (
                fact.value
              )}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

// --- Environment credential section: the provider credential this
// deployment read from its own process environment (README:
// <PROVIDER>_API_KEY, then STARPORT_<PROVIDER>_API_KEY).
//
// It is read-only from the console by construction: the value lives in the
// process the operator started, and no HTTP route can change it. It renders
// as the first row of the provider credential card — the source the gateway
// tries first — and carries no card chrome of its own. Neither it nor the
// section below it is a gateway API key: a gateway API key authenticates a
// caller and never pays a provider.

export function operatorEnvNames(providerId: string): [string, string] {
  const stem = providerId.toUpperCase().replaceAll("-", "_");
  return [`${stem}_API_KEY`, `STARPORT_${stem}_API_KEY`];
}

// checkedEnvNames parses the server's "checked A, B, C" credential detail
// into the authoritative environment names the resolver consulted.
export function checkedEnvNames(detail: string | undefined): string[] {
  if (!detail?.startsWith("checked ")) return [];
  return detail
    .slice("checked ".length)
    .split(",")
    .map((name) => name.trim())
    .filter(Boolean);
}

export function EnvironmentCredentialPanel({
  providerId,
  credential,
}: {
  providerId: string;
  credential: ProviderRuntimeStatus["operator_credential"];
}) {
  const state = credential?.state ?? "not_configured";
  const usable = credential?.usable === true;
  const [envName, prefixedEnvName] = operatorEnvNames(providerId);
  const checked = checkedEnvNames(credential?.detail);
  const failureDetail =
    credential?.detail && checked.length === 0 ? credential.detail : undefined;
  return (
    <section
      data-testid="credential-panel"
      className="flex min-w-0 flex-1 flex-col gap-2"
    >
      {usable ? (
        <p className="text-sm text-text-2">
          Set as{" "}
          <code className="font-mono text-xs text-text-2">{envName}</code> or{" "}
          <code className="font-mono text-xs text-text-2">{prefixedEnvName}</code>{" "}
          in the gateway environment
          {credential?.updated_at
            ? ` · read ${formatRelativeTime(credential.updated_at)}`
            : ""}
          .
        </p>
      ) : (
        <>
          <p className="text-sm text-text-2">
            {state === "not_configured"
              ? "This gateway read no environment credential for this provider."
              : `The environment credential is ${state.replaceAll("_", " ")}${
                  credential?.reason
                    ? ` (${credential.reason.replaceAll("_", " ")})`
                    : ""
                }.`}
          </p>
          {failureDetail && (
            <p data-testid="credential-detail" className="text-sm text-text-3">
              {failureDetail}
            </p>
          )}
          {checked.length > 0 ? (
            <p data-testid="credential-detail" className="text-sm text-text-3">
              The gateway checked{" "}
              {checked.map((name, index) => (
                <span key={name}>
                  {index > 0 && (index === checked.length - 1 ? " and " : ", ")}
                  <code className="font-mono text-xs text-text-2">{name}</code>
                </span>
              ))}
              . Set one in the gateway environment, or apply a gateway
              credential below.
            </p>
          ) : (
            <p className="text-sm text-text-3">
              Set{" "}
              <code className="font-mono text-xs text-text-2">{envName}</code> or{" "}
              <code className="font-mono text-xs text-text-2">{prefixedEnvName}</code>{" "}
              in the gateway environment, or apply a gateway credential below.
            </p>
          )}
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
      {(() => {
        const reach = reachableSummary(offerings);
        if (reach.known === 0) return null;
        return (
          <p
            data-testid="reachable-summary"
            className={`text-sm ${reach.unroutable > 0 ? "text-warning" : "text-text-3"}`}
          >
            <span className="tabular-nums">
              {formatCount(reach.routable)} of {formatCount(reach.known)}
            </span>{" "}
            offerings are reachable
            {reach.unroutable > 0
              ? `; ${formatCount(reach.unroutable)} advertised but unroutable.`
              : "."}
          </p>
        );
      })()}
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
              <th className="px-3 py-2 font-medium">Routing</th>
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
                <td className="px-3 py-2">
                  <RoutingChip routing={offering.routing} />
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
