import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";

import { EntityLogo } from "@/components/catalog/EntityLogo";
import { CredentialPill } from "@/components/providers/ProviderCard";
import { listProviderCatalog, providerStatus } from "@/lib/api";
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

  const entry = catalog.data?.find((candidate) => candidate.id === providerId);
  const runtime = status.data?.providers?.find(
    (candidate) => candidate.provider_id === providerId,
  );
  const offerings = runtime?.offerings ?? [];
  const available = offerings.filter(
    (offering) => offering.state === "available",
  ).length;
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

      {!entry && !catalog.isPending && (
        <p className="text-base text-text-3">
          This provider is not in the current catalog snapshot.
        </p>
      )}
    </div>
  );
}
