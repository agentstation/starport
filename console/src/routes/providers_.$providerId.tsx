import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { useMemo } from "react";

import { EntityLogo } from "@/components/catalog/EntityLogo";
import {
  availableOfferings,
  CredentialPill,
} from "@/components/providers/ProviderCard";
import {
  CredentialPanel,
  HealthPanel,
  OfferingsTable,
  PolicyChips,
} from "@/components/providers/ProviderDetail";
import {
  ApiError,
  listActivity,
  listAdminActivity,
  listProviderCatalog,
  providerStatus,
} from "@/lib/api";
import { formatCount, providerLabel } from "@/lib/format";
import { useHasApiKey } from "@/lib/useApiKey";

export const Route = createFileRoute("/providers_/$providerId")({
  component: ProviderDetailPage,
});

function ProviderDetailPage() {
  const { providerId } = Route.useParams();
  const hasKey = useHasApiKey();

  const catalog = useQuery({
    queryKey: ["provider-catalog"],
    queryFn: listProviderCatalog,
    enabled: hasKey,
    retry: false,
  });
  const status = useQuery({
    queryKey: ["provider-status"],
    queryFn: providerStatus,
    enabled: hasKey,
    retry: false,
  });

  // The health window is pinned per mount so refetches keep comparable
  // bounds. Admin keys read the cross-key log; other keys fall back to
  // their own activity; a locked log leaves the panel on circuit state.
  const sinceISO = useMemo(
    () => new Date(Date.now() - 60 * 60 * 1000).toISOString(),
    [],
  );
  const activity = useQuery({
    queryKey: ["provider-activity", providerId, sinceISO],
    queryFn: async () => {
      const filters = { provider: providerId, since: sinceISO, limit: 200 };
      try {
        return await listAdminActivity(filters);
      } catch (error) {
        if (!(error instanceof ApiError) || !error.needsKey) throw error;
      }
      return listActivity(filters);
    },
    enabled: hasKey,
    retry: false,
  });

  const entry = catalog.data?.find((candidate) => candidate.id === providerId);
  const runtime = status.data?.providers?.find(
    (candidate) => candidate.provider_id === providerId,
  );
  const offerings = runtime?.offerings ?? [];
  const available = availableOfferings(offerings);
  const name = providerLabel(providerId, entry?.name);

  if (catalog.isPending && !entry) {
    return <p className="text-base text-text-3">Loading provider…</p>;
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
        {runtime && <CredentialPill credential={runtime.operator_credential} />}
      </div>

      <PolicyChips entry={entry} />

      <div className="flex flex-wrap items-center gap-4 text-sm text-text-2">
        {runtime && (
          <span className="tabular-nums">
            {formatCount(offerings.length)} models · {formatCount(available)}{" "}
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
          <a
            href={entry.url}
            target="_blank"
            rel="noreferrer"
            className="text-accent-link transition-colors duration-150 ease-standard hover:underline"
          >
            Website
          </a>
        )}
        {entry?.docs_url && (
          <a
            href={entry.docs_url}
            target="_blank"
            rel="noreferrer"
            className="text-accent-link transition-colors duration-150 ease-standard hover:underline"
          >
            Documentation
          </a>
        )}
      </div>

      {runtime && (
        <div className="grid gap-4 lg:grid-cols-2">
          <CredentialPanel
            providerId={providerId}
            credential={runtime.operator_credential}
          />
          <HealthPanel offerings={offerings} records={activity.data?.data} />
        </div>
      )}

      {runtime && <OfferingsTable providerId={providerId} offerings={offerings} />}

      {!entry && !catalog.isPending && (
        <p className="text-base text-text-3">
          This provider is not in the current catalog snapshot.
        </p>
      )}
    </div>
  );
}
