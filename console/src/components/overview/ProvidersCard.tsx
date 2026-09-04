import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { Card, CardTitle } from "@/components/ui/Card";
import { LoadFailed } from "@/components/ui/LoadFailed";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { CardSkeleton } from "@/components/ui/skeleton";
import { ApiError, type ProviderRuntimeStatus } from "@/lib/api";
import { queries } from "@/lib/queries";

// A count is a control: hover or click lists the providers it counts, and
// each name links to the provider page. The trigger is a button, so a
// keyboard reaches the list, and the popover reads the button as its name.
function Count({
  label,
  providers,
  empty,
}: {
  label: string;
  providers: string[];
  empty: string;
}) {
  const id = label.toLowerCase();
  return (
    <Popover>
      <PopoverTrigger
        openOnHover
        delay={150}
        data-testid={`count-${id}`}
        className="-mx-2 -my-1 flex flex-col items-start gap-1 rounded-sm px-2 py-1 text-left outline-none transition-colors duration-150 ease-standard hover:bg-bg-hover focus-visible:ring-2 focus-visible:ring-accent/50 data-popup-open:bg-bg-hover"
      >
        <span className="text-xs text-text-3">{label}</span>
        <span className="font-mono text-xl font-medium tabular-nums text-text-1">
          {providers.length}
        </span>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        aria-label={`${label} providers`}
        className="max-h-80 w-56 overflow-y-auto"
      >
        {providers.length === 0 ? (
          <p className="text-xs text-text-3">{empty}</p>
        ) : (
          <ul data-testid={`count-${id}-list`} className="flex flex-col gap-0.5">
            {providers.map((providerId) => (
              <li key={providerId}>
                <Link
                  to="/providers/$providerId"
                  params={{ providerId }}
                  className="flex h-7 items-center rounded-xs px-1.5 font-mono text-sm text-text-2 transition-colors duration-150 ease-standard hover:bg-bg-hover hover:text-text-1"
                >
                  {providerId}
                </Link>
              </li>
            ))}
          </ul>
        )}
      </PopoverContent>
    </Popover>
  );
}

// configured reports a provider that holds a credential, usable or not. A
// provider that reports no credential at all is not credentialed, so
// "Credentialed" minus "Usable" always equals the rows the blocked list
// names.
function configured(provider: ProviderRuntimeStatus): boolean {
  return (
    provider.operator_credential !== undefined &&
    provider.operator_credential.state !== "not_configured"
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
    .filter((provider) => configured(provider) && !provider.operator_credential?.usable)
    .map((provider) => {
      const credential = provider.operator_credential;
      const code = credential?.reason || credential?.state || "unusable";
      return { providerId: provider.provider_id, reason: code.replaceAll("_", " ") };
    })
    .sort((a, b) => a.providerId.localeCompare(b.providerId));
}

function ids(providers: ProviderRuntimeStatus[]): string[] {
  return providers.map((provider) => provider.provider_id).sort((a, b) => a.localeCompare(b));
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
  const known = ids(providers);
  const credentialed = ids(providers.filter(configured));
  const usable = ids(providers.filter((provider) => provider.operator_credential?.usable));
  const blocked = credentialBlocked(providers);
  return (
    <Card>
      <CardTitle>Providers</CardTitle>
      <div className="grid grid-cols-3 gap-4">
        <Count label="Known" providers={known} empty="No providers are known." />
        <Count
          label="Credentialed"
          providers={credentialed}
          empty="No provider holds a credential."
        />
        <Count label="Usable" providers={usable} empty="No provider credential is usable." />
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
