import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, notFound } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";

import { authorLabel, AuthorLinks } from "@/components/authors/AuthorCard";
import { EntityLogo } from "@/components/catalog/EntityLogo";
import { offeringAvailability } from "@/components/models/ModelDetail";
import { DetailSkeleton } from "@/components/ui/skeleton";
import {
  ApiError,
  type Model,
  type ProviderRuntimeStatus,
} from "@/lib/api";
import { queries, settle } from "@/lib/queries";
import { formatContext } from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

export const Route = createFileRoute("/authors_/$authorId")({
  // The gateway answers an unknown author with a 404, which is the one
  // failure this loader turns into a not-found page.
  loader: async ({ context, params }) => {
    try {
      await context.queryClient.ensureQueryData(queries.author(params.authorId));
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) throw notFound();
    }
    await settle(
      context.queryClient.ensureQueryData(queries.models()),
      context.queryClient.ensureQueryData(queries.providerStatus()),
    );
  },
  component: AuthorDetailPage,
});

function BackLink() {
  return (
    <Link
      to="/authors"
      className="flex items-center gap-1.5 text-xs text-text-3 transition-colors duration-150 ease-standard hover:text-text-1"
    >
      <ArrowLeft className="size-3.5" />
      Authors
    </Link>
  );
}

function ModelRow({
  model,
  providers,
}: {
  model: Model;
  providers: ProviderRuntimeStatus[] | undefined;
}) {
  const { total, available } = offeringAvailability(model, providers);
  return (
    <Link
      to="/models/$modelId"
      params={{ modelId: model.id }}
      className="grid grid-cols-[minmax(280px,1fr)_110px_170px] items-center border-b border-border-1 px-2.5 py-2 transition-colors duration-150 ease-standard hover:bg-bg-hover"
    >
      <div className="flex min-w-0 items-baseline gap-2">
        <span className="truncate font-mono text-xs text-text-1">
          {model.id}
        </span>
        {model.name && model.name !== model.id && (
          <span className="hidden min-w-0 truncate text-xs text-text-4 @4xl:inline">
            {model.name}
          </span>
        )}
      </div>
      <span className="text-right font-mono text-xs tabular-nums text-text-2">
        {formatContext(model.context_length)}
      </span>
      <span className="text-right text-xs tabular-nums text-text-3">
        {total === 0
          ? "no providers"
          : `${available} of ${total} provider${total === 1 ? "" : "s"} available`}
      </span>
    </Link>
  );
}

function AuthorDetailPage() {
  const { authorId } = Route.useParams();
  const keyUsable = useGatewayAccess();

  const author = useQuery({
    ...queries.author(authorId),
    enabled: keyUsable,
  });
  const models = useQuery({
    ...queries.models(),
    enabled: keyUsable,
  });
  const status = useQuery({
    ...queries.providerStatus(),
    enabled: keyUsable,
  });

  if (author.isPending) {
    return <DetailSkeleton />;
  }
  if (author.error) {
    const missing =
      author.error instanceof ApiError && author.error.status === 404;
    return (
      <div className="flex flex-col gap-4">
        <BackLink />
        <p className="text-base text-text-3">
          {missing
            ? "This author is not in the current catalog snapshot."
            : `Failed to load author: ${author.error.message}`}
        </p>
      </div>
    );
  }

  const record = author.data;
  const byId = new Map((models.data ?? []).map((model) => [model.id, model]));
  const catalog = [...(record.models ?? [])].sort();

  return (
    <div className="flex flex-col gap-5">
      <BackLink />

      <div className="flex items-start gap-3">
        <EntityLogo
          kind="authors"
          id={record.id}
          name={authorLabel(record)}
          size={40}
        />
        <div className="flex min-w-0 flex-col gap-1">
          <div className="flex flex-wrap items-baseline gap-2.5">
            <h1 className="text-xl font-semibold tracking-[-0.01em]">
              {authorLabel(record)}
            </h1>
            <span className="font-mono text-xs text-text-4">{record.id}</span>
            {record.headquarters && (
              <span className="text-xs text-text-4">{record.headquarters}</span>
            )}
          </div>
          {record.description && (
            <p className="mt-1 max-w-prose text-sm text-text-3">
              {record.description}
            </p>
          )}
          <div className="mt-1.5 flex flex-wrap items-center gap-4">
            <AuthorLinks author={record} />
          </div>
        </div>
      </div>

      <section className="flex flex-col gap-2">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-sm font-medium text-text-2">
            Models · {catalog.length}
          </h2>
          <Link
            to="/models"
            search={{ author: record.id }}
            className="text-xs text-accent-link transition-colors duration-150 ease-standard hover:underline"
          >
            Browse in the models list
          </Link>
        </div>
        {catalog.length === 0 ? (
          <p className="text-sm text-text-3">
            No models from this author in the current catalog snapshot.
          </p>
        ) : (
          <div className="text-sm max-sm:overflow-x-auto">
            <div className="grid grid-cols-[minmax(280px,1fr)_110px_170px] border-b border-border-1 px-2.5 py-1.5 text-xs font-medium text-text-3">
              <span>model</span>
              <span className="text-right">context</span>
              <span className="text-right">availability</span>
            </div>
            {catalog.map((modelId) => {
              const model = byId.get(modelId);
              if (!model) {
                return (
                  <div
                    key={modelId}
                    className="grid grid-cols-[minmax(280px,1fr)_110px_170px] items-center border-b border-border-1 px-2.5 py-2"
                  >
                    <span className="truncate font-mono text-xs text-text-4">
                      {modelId}
                    </span>
                    <span />
                    <span className="text-right text-xs text-text-4">
                      not in this snapshot
                    </span>
                  </div>
                );
              }
              return (
                <ModelRow
                  key={modelId}
                  model={model}
                  providers={status.data?.providers}
                />
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}
