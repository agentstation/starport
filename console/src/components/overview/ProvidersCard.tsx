import { useQuery } from "@tanstack/react-query";

import { Card, CardTitle } from "@/components/ui/Card";
import { LoadFailed } from "@/components/ui/LoadFailed";
import { CardSkeleton } from "@/components/ui/skeleton";
import { ApiError } from "@/lib/api";
import { queries } from "@/lib/queries";

function Count({ label, value }: { label: string; value: number }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="text-xs text-text-3">{label}</div>
      <div className="font-mono text-xl font-medium tabular-nums text-text-1">{value}</div>
    </div>
  );
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
  const configured = providers.filter(
    (provider) => provider.operator_credential?.state !== "not_configured",
  ).length;
  return (
    <Card>
      <CardTitle>Providers</CardTitle>
      <div className="grid grid-cols-3 gap-4">
        <Count label="Known" value={providers.length} />
        <Count label="Credentialed" value={configured} />
        <Count label="Usable" value={usable} />
      </div>
    </Card>
  );
}
