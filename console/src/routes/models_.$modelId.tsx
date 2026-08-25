import { useQuery } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { ArrowLeft, Check, Copy } from "lucide-react";
import { useState } from "react";

import { EntityLogo } from "@/components/catalog/EntityLogo";
import {
  CapabilityChips,
  LineageLinks,
  ModelActions,
  OfferingTable,
} from "@/components/models/ModelDetail";
import { listModels, providerStatus } from "@/lib/api";
import { formatContext } from "@/lib/format";
import { useGatewayAccess } from "@/lib/useGatewayAccess";

export const Route = createFileRoute("/models_/$modelId")({
  component: ModelDetailPage,
});

function CopyableId({ id }: { id: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={() => {
        void navigator.clipboard.writeText(id).then(() => {
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        });
      }}
      title="Copy model ID"
      className="flex items-center gap-1.5 font-mono text-xs text-text-4 transition-colors duration-150 ease-standard hover:text-text-2"
    >
      {id}
      {copied ? (
        <Check className="size-3 text-success" />
      ) : (
        <Copy className="size-3" />
      )}
    </button>
  );
}

function ModelDetailPage() {
  const { modelId } = Route.useParams();
  const keyUsable = useGatewayAccess();

  const models = useQuery({
    queryKey: ["models"],
    queryFn: listModels,
    enabled: keyUsable,
    retry: false,
  });
  const status = useQuery({
    queryKey: ["provider-status"],
    queryFn: providerStatus,
    enabled: keyUsable,
    retry: false,
  });

  const model = models.data?.find((candidate) => candidate.id === modelId);

  if (models.isPending) {
    return <p className="text-base text-text-3">Loading model…</p>;
  }
  if (!model) {
    return (
      <div className="flex flex-col gap-4">
        <Link
          to="/models"
          className="flex items-center gap-1.5 text-xs text-text-3 transition-colors duration-150 ease-standard hover:text-text-1"
        >
          <ArrowLeft className="size-3.5" />
          Models
        </Link>
        <p className="text-base text-text-3">
          This model is not in the current catalog snapshot.
        </p>
      </div>
    );
  }

  const author = model.authors?.[0];
  const name = model.name ?? model.id;

  return (
    <div className="flex flex-col gap-5">
      <Link
        to="/models"
        className="flex items-center gap-1.5 text-xs text-text-3 transition-colors duration-150 ease-standard hover:text-text-1"
      >
        <ArrowLeft className="size-3.5" />
        Models
      </Link>

      <div className="flex items-start justify-between gap-4">
        <div className="flex min-w-0 items-start gap-3">
          {author && (
            <EntityLogo
              kind="authors"
              id={author.id}
              name={author.name ?? author.id}
              size={40}
            />
          )}
          <div className="flex min-w-0 flex-col gap-1">
            <div className="flex flex-wrap items-baseline gap-2.5">
              <h1 className="text-xl font-semibold tracking-[-0.01em]">{name}</h1>
              {model.open_weights && (
                <span className="inline-flex h-5 items-center rounded-xs bg-info-tint px-1.5 text-xs font-medium text-text-2">
                  open weights
                </span>
              )}
            </div>
            <CopyableId id={model.id} />
            {model.description && (
              <p className="mt-1 max-w-prose text-sm text-text-3">
                {model.description}
              </p>
            )}
          </div>
        </div>
        <ModelActions modelId={model.id} />
      </div>

      <div className="flex flex-wrap items-center gap-4 text-sm text-text-2">
        <span className="tabular-nums">
          {formatContext(model.context_length)} context
        </span>
        {model.knowledge_cutoff && (
          <span>knowledge cutoff {model.knowledge_cutoff}</span>
        )}
        {(model.authors ?? []).map((entry) => (
          <Link
            key={entry.id}
            to="/authors/$authorId"
            params={{ authorId: entry.id }}
            className="text-accent-link transition-colors duration-150 ease-standard hover:underline"
          >
            {entry.name ?? entry.id}
          </Link>
        ))}
      </div>

      <CapabilityChips model={model} />

      {(model.tags ?? []).length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          {(model.tags ?? []).map((tag) => (
            <span
              key={tag}
              className="inline-flex h-5 items-center rounded-xs bg-bg-raised px-1.5 text-xs text-text-3"
            >
              {tag}
            </span>
          ))}
        </div>
      )}

      <section className="flex flex-col gap-2">
        <h2 className="text-xs font-medium uppercase tracking-wide text-text-3">
          Providers
        </h2>
        <OfferingTable model={model} providers={status.data?.providers} />
      </section>

      <LineageLinks model={model} />
    </div>
  );
}
