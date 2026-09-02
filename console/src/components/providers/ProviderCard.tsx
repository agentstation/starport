import { Link } from "@tanstack/react-router";

import { EntityLogo } from "@/components/catalog/EntityLogo";
import type { ProviderCatalogEntry, ProviderRuntimeStatus } from "@/lib/api";
import { formatCount, formatRelativeTime, providerLabel } from "@/lib/format";

// One status vocabulary (DESIGN.md): the dot reports liveness — the health
// rollup is the card's single always-present status, and the same badge
// leads the detail header, so the two screens agree. The credential pill is
// lifecycle and appears only when the credential needs attention: a healthy
// card states its status exactly once.
const CREDENTIAL_TONES: Record<string, string> = {
  ready: "bg-success-tint text-success",
  not_configured: "bg-bg-raised text-text-3",
  refreshing: "bg-info-tint text-text-2",
  denied: "bg-error-tint text-error",
  invalid: "bg-error-tint text-error",
  unavailable: "bg-warning-tint text-warning",
};

export function CredentialPill({
  credential,
}: {
  credential: ProviderRuntimeStatus["operator_credential"];
}) {
  const state = credential?.state ?? "unknown";
  const label =
    state === "not_configured" ? "no credential" : state.replaceAll("_", " ");
  return (
    <span
      title={
        credential?.updated_at
          ? `environment credential · updated ${formatRelativeTime(credential.updated_at)}`
          : "environment credential"
      }
      className={`inline-flex h-5 shrink-0 items-center whitespace-nowrap rounded-xs px-1.5 text-xs font-medium ${
        CREDENTIAL_TONES[state] ?? "bg-bg-raised text-text-3"
      }`}
    >
      {label}
    </span>
  );
}

// providerHealth folds adapter state, circuit state, and routing into the
// one verdict both the card and the detail header lead with. It reports
// liveness only — a missing credential is the credential pill's story, even
// though it is often why nothing is available. The console has one
// user-facing health vocabulary: healthy, degraded, unreachable,
// unavailable, down. Unreachable means the gateway's own requests got no
// response back; unavailable means the provider responded and refused or
// erred; down means nothing is available AND the provider's own status page
// confirms a major or critical incident.
export type ProviderHealth = {
  state:
    | "healthy"
    | "degraded"
    | "unreachable"
    | "unavailable"
    | "down"
    | "no_models";
  label: string;
};

export function providerHealth(status: ProviderRuntimeStatus): ProviderHealth {
  const offerings = status.offerings ?? [];
  const adapterState = status.adapter?.state ?? "unknown";
  if (adapterState === "no_offerings" || offerings.length === 0) {
    return { state: "no_models", label: "no models" };
  }
  if (adapterState !== "ready") {
    return { state: "unavailable", label: "unavailable" };
  }
  const available = availableOfferings(offerings);
  if (available === 0) {
    // The provider's own status page is the strongest evidence: when the
    // gateway can serve nothing and the provider itself confirms a major
    // incident, the verdict is down, not a guess from failed requests.
    const indicator = status.incident?.indicator;
    if (indicator === "major" || indicator === "critical") {
      return { state: "down", label: "down" };
    }
    // The offering reason records the failure that tripped each circuit.
    // When everything that failed did so without any provider response, the
    // provider is unreachable, not refusing.
    const reasons = offerings
      .map((offering) => offering.reason)
      .filter((reason): reason is string => Boolean(reason));
    if (
      reasons.length > 0 &&
      reasons.every((reason) => reason === "provider_unreachable")
    ) {
      return { state: "unreachable", label: "unreachable" };
    }
    return { state: "unavailable", label: "unavailable" };
  }
  if (available < offerings.length) {
    return { state: "degraded", label: "degraded" };
  }
  return { state: "healthy", label: "healthy" };
}

const HEALTH_DOTS: Record<ProviderHealth["state"], string> = {
  healthy: "bg-success",
  degraded: "bg-warning",
  unreachable: "bg-error",
  unavailable: "bg-error",
  down: "bg-error",
  no_models: "bg-text-4",
};

export function HealthBadge({ health }: { health: ProviderHealth }) {
  return (
    <span
      data-testid="health-badge"
      className="flex shrink-0 items-center gap-1.5 text-xs text-text-2"
    >
      <span
        aria-hidden="true"
        className={`size-2 shrink-0 rounded-full ${HEALTH_DOTS[health.state]}`}
      />
      {health.label}
    </span>
  );
}

// An offering is available when a request can both reach it and be admitted.
// The circuit answers admission: healthy, or half_open while it probes
// recovery; open and unavailable reject. Routing answers reach: an offering the
// planner drops never receives an attempt, so its circuit stays healthy forever
// and counting it as available overstates what the provider serves. Routing
// that no planning generation has judged yet stays unknown, and unknown does
// not disqualify — only an explicit unroutable verdict does.
export function availableOfferings(
  offerings: ProviderRuntimeStatus["offerings"],
): number {
  return (offerings ?? []).filter(
    (offering) =>
      (offering.state === "healthy" || offering.state === "half_open") &&
      offering.routing?.state !== "unroutable",
  ).length;
}

export function ProviderCard({
  status,
  entry,
}: {
  status: ProviderRuntimeStatus;
  entry: ProviderCatalogEntry | undefined;
}) {
  const credential = status.operator_credential;
  const offerings = status.offerings ?? [];
  const available = availableOfferings(offerings);
  const health = providerHealth(status);
  const description = entry?.description;
  const credentialReason =
    credential &&
    !credential.usable &&
    credential.state !== "not_configured" &&
    credential.reason;
  const adapterState = status.adapter?.state ?? "unknown";
  const adapterReason =
    adapterState !== "ready" &&
    adapterState !== "no_offerings" &&
    status.adapter?.reason;
  return (
    <Link
      to="/providers/$providerId"
      params={{ providerId: status.provider_id }}
      data-testid="provider-card"
      className="flex flex-col gap-2 rounded-md border border-border-1 bg-bg-panel p-4 transition-colors duration-150 ease-standard hover:border-border-2 hover:bg-bg-raised"
    >
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2.5">
          <EntityLogo
            kind="providers"
            id={status.provider_id}
            name={providerLabel(status.provider_id, entry?.name)}
            size={28}
          />
          <div className="flex min-w-0 items-baseline gap-2">
            <span className="truncate text-sm font-medium text-text-1">
              {providerLabel(status.provider_id, entry?.name)}
            </span>
            <span className="shrink-0 font-mono text-xs text-text-3">
              {status.provider_id}
            </span>
          </div>
        </div>
        <HealthBadge health={health} />
      </div>
      {description && (
        <p className="line-clamp-2 text-xs leading-relaxed text-text-3">
          {description}
        </p>
      )}
      {status.incident && (
        <p data-testid="provider-incident" className="text-xs text-warning">
          {status.incident.description || "provider reports a service incident"}
        </p>
      )}
      {(credentialReason || adapterReason) && (
        <p className="text-xs text-text-3">
          {(credentialReason || adapterReason || "").replaceAll("_", " ")}
        </p>
      )}
      <div className="flex items-center gap-3 text-xs text-text-3">
        <span className="tabular-nums">
          {formatCount(available)} of {formatCount(offerings.length)} models
          available
        </span>
        {!credential?.usable && <CredentialPill credential={credential} />}
      </div>
    </Link>
  );
}

export function CatalogProviderCard({ entry }: { entry: ProviderCatalogEntry }) {
  return (
    <Link
      to="/providers/$providerId"
      params={{ providerId: entry.id }}
      data-testid="provider-card"
      className="flex flex-col gap-2 rounded-md border border-border-1 bg-bg-panel p-4 transition-colors duration-150 ease-standard hover:border-border-2 hover:bg-bg-raised"
    >
      <div className="flex min-w-0 items-center gap-2.5">
        <EntityLogo
          kind="providers"
          id={entry.id}
          name={providerLabel(entry.id, entry.name)}
          size={28}
        />
        <div className="flex min-w-0 items-baseline gap-2">
          <span className="truncate text-sm font-medium text-text-1">
            {providerLabel(entry.id, entry.name)}
          </span>
          <span className="shrink-0 font-mono text-xs text-text-3">
            {entry.id}
          </span>
        </div>
      </div>
      {entry.description && (
        <p className="line-clamp-2 text-xs leading-relaxed text-text-3">
          {entry.description}
        </p>
      )}
      <span className="text-xs tabular-nums text-text-3">
        {formatCount(entry.models?.length ?? 0)} models
      </span>
    </Link>
  );
}

// Credentialed providers sort first: a usable credential ranks above a
// configured-but-broken one, which ranks above no credential at all.
export function credentialRank(status: ProviderRuntimeStatus): number {
  if (status.operator_credential?.usable) return 0;
  if (status.operator_credential?.state === "not_configured") return 2;
  return 1;
}
