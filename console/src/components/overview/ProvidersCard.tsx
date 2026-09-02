import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { Card, CardTitle } from "@/components/ui/Card";
import { LoadFailed } from "@/components/ui/LoadFailed";
import { CardSkeleton } from "@/components/ui/skeleton";
import { ApiError, type ProviderRuntimeStatus } from "@/lib/api";
import { queries } from "@/lib/queries";

function Count({ label, value }: { label: string; value: number }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="text-xs text-text-3">{label}</div>
      <div className="font-mono text-xl font-medium tabular-nums text-text-1">{value}</div>
    </div>
  );
}

// credentialBlocked keeps the providers that hold a credential the gateway
// cannot use. "Credentialed 2, Usable 1" alone leaves the reader to open
// every provider page to find the one that is blocked, so the card names
// it and says why in the same breath.
export function credentialBlocked(
  providers: ProviderRuntimeStatus[],
): Array<{ providerId: string; reason: string }> {
  return providers
    .filter(
      (provider) =>
        provider.operator_credential &&
        provider.operator_credential.state !== "not_configured" &&
        !provider.operator_credential.usable,
    )
    .map((provider) => {
      const credential = provider.operator_credential;
      const code = credential?.reason || credential?.state || "unusable";
      return { providerId: provider.provider_id, reason: code.replaceAll("_", " ") };
    })
    .sort((a, b) => a.providerId.localeCompare(b.providerId));
}

// ProvidersCard summarizes provider credential posture. The providers
// page (CM5) carries the detail.
export function ProvidersCard() {
  const status = useQuery({
    ...queries.providerStatus(),
  });

  if (status.isPending) return <CardSkeleton lines={2} />;
  if (status.error) {
    if (status.error instanceof ApiError && status.error.needsKey) {
      return (
        <Card>
          <CardTitle>Providers</CardTitle>
          <p className="text-base text-text-3">
            Provider status needs an admin-scoped key.
          </p>
        </Card>
      );
    }
    return (
      <LoadFailed
        what="provider status"
        error={status.error}
        onRetry={() => void status.refetch()}
      />
    );
  }

  const providers = status.data?.providers ?? [];
  const usable = providers.filter(
    (provider) => provider.operator_credential?.usable,
  ).length;
  // A provider that reports no credential at all is not credentialed, so
  // "Credentialed" minus "Usable" always equals the rows the list below
  // names.
  const configured = providers.filter(
    (provider) =>
      provider.operator_credential &&
      provider.operator_credential.state !== "not_configured",
  ).length;
  const blocked = credentialBlocked(providers);
  return (
    <Card>
      <CardTitle>Providers</CardTitle>
      <div className="grid grid-cols-3 gap-4">
        <Count label="Known" value={providers.length} />
        <Count label="Credentialed" value={configured} />
        <Count label="Usable" value={usable} />
      </div>
      {blocked.length > 0 && (
        <ul
          data-testid="credential-reasons"
          aria-label="Credentialed but not usable"
          className="mt-4 flex flex-col gap-1 border-t border-border-1 pt-3"
        >
          {blocked.map(({ providerId, reason }) => (
            <li key={providerId} className="flex min-w-0 items-baseline gap-2 text-xs">
              <Link
                to="/providers/$providerId"
                params={{ providerId }}
                className="shrink-0 font-mono text-text-2 transition-colors duration-150 ease-standard hover:text-accent-link"
              >
                {providerId}
              </Link>
              <span className="min-w-0 truncate text-text-3">{reason}</span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
