import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, notFound } from "@tanstack/react-router";
import { Activity, ArrowLeft, BookOpen, Globe } from "lucide-react";
import { useMemo } from "react";

import { EntityLogo } from "@/components/catalog/EntityLogo";
import { ExternalLink } from "@/components/ui/ExternalLink";
import { ServedCredentialPanel } from "@/components/credentials/ServedCredentialPanel";
import {
  availableOfferings,
  HealthBadge,
  providerHealth,
} from "@/components/providers/ProviderCard";
import { ProviderCredentialCard } from "@/components/credentials/ProviderCredentialCard";
import { IncidentLog } from "@/components/providers/IncidentLog";
import { DetailSkeleton } from "@/components/ui/skeleton";
import { LoadFailed } from "@/components/ui/LoadFailed";
import {
  HealthPanel,
  OfferingsTable,
  PolicySummary,
} from "@/components/providers/ProviderDetail";
import { RelativeTime } from "@/components/ui/RelativeTime";
import { queries, settle } from "@/lib/queries";
import { formatCount, providerLabel } from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

export const Route = createFileRoute("/providers_/$providerId")({
  // A provider is known to the catalog or to the runtime. Only when both
  // answered and neither names it is the address a not-found page.
  loader: async ({ context, params }) => {
    const [catalog, status] = await Promise.all([
      context.queryClient.ensureQueryData(queries.providerCatalog()).catch(() => undefined),
      context.queryClient.ensureQueryData(queries.providerStatus()).catch(() => undefined),
    ]);
    await settle(
      context.queryClient.ensureQueryData(queries.providerIncidents(params.providerId)),
    );
    const known =
      catalog?.some((entry) => entry.id === params.providerId) ||
      status?.providers?.some((entry) => entry.provider_id === params.providerId);
    if (catalog && status && !known) throw notFound();
  },
  component: ProviderDetailPage,
});

function ProviderDetailPage() {
  const { providerId } = Route.useParams();
  const keyUsable = useGatewayAccess();

  // Each read narrows to the one provider this page is about.
  const catalog = useQuery({
    ...queries.providerCatalog(),
    enabled: keyUsable,
    select: (entries) => entries.find((candidate) => candidate.id === providerId) ?? null,
  });
  const status = useQuery({
    ...queries.providerStatus(),
    enabled: keyUsable,
    select: (report) =>
      report.providers?.find((candidate) => candidate.provider_id === providerId) ?? null,
  });

  // The health window is pinned per mount so refetches keep comparable
  // bounds. Admin keys read the cross-key log; other keys fall back to
  // their own activity; a locked log leaves the panel on circuit state.
  const sinceISO = useMemo(
    () => new Date(Date.now() - 60 * 60 * 1000).toISOString(),
    [],
  );
  const activity = useQuery({
    ...queries.providerActivity(providerId, sinceISO),
    enabled: keyUsable,
  });

  const incidents = useQuery({
    ...queries.providerIncidents(providerId),
    enabled: keyUsable,
  });

  const entry = catalog.data ?? undefined;
  const runtime = status.data ?? undefined;
  const offerings = runtime?.offerings ?? [];
  const available = availableOfferings(offerings);
  const name = providerLabel(providerId, entry?.name);

  if (catalog.isPending && !entry) {
    return <DetailSkeleton />;
  }

  return (
    <div className="flex flex-col gap-5">
      <Link
        to="/providers"
        className="flex items-center gap-1.5 text-xs text-text-3 transition-colors duration-150 ease-standard hover:text-text-1"
      >
        <ArrowLeft className="size-3.5" />
        Providers
      </Link>

      <div className="flex items-start justify-between gap-4">
        <div className="flex min-w-0 items-center gap-3">
          <EntityLogo kind="providers" id={providerId} name={name} size={40} />
          <div className="min-w-0">
            <div className="flex items-baseline gap-2.5">
              <h1 className="truncate text-xl font-semibold tracking-[-0.01em]">
                {name}
              </h1>
              <span className="shrink-0 font-mono text-xs text-text-4">
                {providerId}
              </span>
            </div>
            {entry?.description && (
              <p className="mt-1 max-w-prose text-sm text-text-3">
                {entry.description}
              </p>
            )}
          </div>
        </div>
        {runtime && <HealthBadge health={providerHealth(runtime)} />}
      </div>

      {runtime?.incident && (
        <p data-testid="provider-incident" className="text-sm text-warning">
          {runtime.incident.description ||
            "The provider reports a service incident."}
          {runtime.incident.checked_at && (
            <>
              {" · checked "}
              <RelativeTime iso={runtime.incident.checked_at} />
            </>
          )}
        </p>
      )}

      <div className="flex flex-wrap items-center gap-4 text-sm text-text-2">
        {runtime && (
          <span className="tabular-nums">
            {formatCount(available)} of {formatCount(offerings.length)} models
            available
          </span>
        )}
        <Link
          to="/models"
          search={{ provider: providerId }}
          className="text-accent-link transition-colors duration-150 ease-standard hover:underline"
        >
          Browse models
        </Link>
        {entry?.url && (
          <ExternalLink
            href={entry.url}
            icon={Globe}
            className="text-accent-link transition-colors duration-150 ease-standard hover:underline"
          >
            Website
          </ExternalLink>
        )}
        {entry?.docs_url && (
          <ExternalLink
            href={entry.docs_url}
            icon={BookOpen}
            className="text-accent-link transition-colors duration-150 ease-standard hover:underline"
          >
            Documentation
          </ExternalLink>
        )}
        {entry?.status_page_url && (
          <ExternalLink
            href={entry.status_page_url}
            icon={Activity}
            className="text-accent-link transition-colors duration-150 ease-standard hover:underline"
          >
            Status
          </ExternalLink>
        )}
      </div>

      {runtime && (
        <>
          <div className="grid items-start gap-4 lg:grid-cols-3">
            <div className="lg:col-span-2">
              <ProviderCredentialCard
                providerId={providerId}
                name={name}
                credential={runtime.operator_credential}
                fields={entry?.credential_fields ?? []}
              />
            </div>
            {activity.isError ? (
              <LoadFailed
                what="the last hour of activity"
                error={activity.error}
                onRetry={() => void activity.refetch()}
              />
            ) : (
              <ServedCredentialPanel records={activity.data?.data} />
            )}
          </div>
          <HealthPanel
            offerings={offerings}
            records={activity.data?.data}
            activityFailed={activity.isError}
          />
          <IncidentLog
            name={name}
            statusPageUrl={entry?.status_page_url}
            report={incidents.data}
            failed={incidents.isError}
          />
        </>
      )}

      {runtime && <OfferingsTable providerId={providerId} offerings={offerings} />}

      <PolicySummary entry={entry} />

      {!entry && !catalog.isPending && (
        <p className="text-base text-text-3">
          This provider is not in the current catalog snapshot.
        </p>
      )}
    </div>
  );
}
