import { Link } from "@tanstack/react-router";

import { EntityLogo } from "@/components/catalog/EntityLogo";
import type { ProviderCatalogEntry, ProviderRuntimeStatus } from "@/lib/api";
import { formatCount, formatRelativeTime, providerLabel } from "@/lib/format";

// One status vocabulary (DESIGN.md): the pill is the operator credential
// lifecycle and is the card's single always-present status. The adapter
// dot is an exception signal — it appears only when the adapter cannot
// route, so a healthy card states its status exactly once.
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

function AdapterFault({ state }: { state: string }) {
  return (
    <span className="flex items-center gap-1.5 text-xs text-text-3">
      <span
        aria-hidden="true"
        className={`size-2 shrink-0 rounded-full ${
          state === "no_offerings" ? "bg-text-4" : "bg-error"
        }`}
      />
      {state.replaceAll("_", " ")}
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
  const description = entry?.description;
  const credentialReason =
    credential &&
    !credential.usable &&
    credential.state !== "not_configured" &&
    credential.reason;
  const adapterState = status.adapter?.state ?? "unknown";
  const adapterFault = adapterState !== "ready";
  const adapterReason =
    adapterFault && adapterState !== "no_offerings" && status.adapter?.reason;
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
            <span className="shrink-0 font-mono text-xs text-text-4">
              {status.provider_id}
            </span>
          </div>
        </div>
        <CredentialPill credential={credential} />
      </div>
      {description && (
        <p className="line-clamp-2 text-xs leading-relaxed text-text-3">
          {description}
        </p>
      )}
      {(credentialReason || adapterReason) && (
        <p className="text-xs text-text-3">
          {(credentialReason || adapterReason || "").replaceAll("_", " ")}
        </p>
      )}
      <div className="flex items-center gap-4 text-xs text-text-3">
        {adapterFault && <AdapterFault state={adapterState} />}
        <span className="tabular-nums">
          {formatCount(offerings.length)} models · {formatCount(available)}{" "}
          available
        </span>
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
          <span className="shrink-0 font-mono text-xs text-text-4">
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
