import { Link } from "@tanstack/react-router";
import { MessageSquare, SplitSquareHorizontal } from "lucide-react";

import type {
  Model,
  ModelOffering,
  ProviderRuntimeStatus,
} from "@/lib/api";
import {
  formatContext,
  formatCount,
  formatPricePerM,
  providerLabel,
} from "@/lib/format";
import { operationsOf } from "@/lib/modelFilter";

// --- Actions: Open in chat seeds the composer's model; Compare lands
// in chat with compare mode seeded from the same model.

export function ModelActions({ modelId }: { modelId: string }) {
  return (
    <div className="flex shrink-0 items-center gap-2">
      <Link
        to="/chat"
        search={{ model: modelId }}
        data-testid="open-in-chat"
        className="flex items-center gap-1.5 rounded-sm bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors duration-150 ease-standard hover:bg-accent-hover"
      >
        <MessageSquare className="size-3.5" />
        Open in chat
      </Link>
      <Link
        to="/chat"
        search={{ model: modelId, compare: true }}
        data-testid="add-to-comparison"
        className="flex items-center gap-1.5 rounded-sm border border-border-2 px-3 py-1.5 text-sm text-text-2 transition-colors duration-150 ease-standard hover:text-text-1"
      >
        <SplitSquareHorizontal className="size-3.5" />
        Compare
      </Link>
    </div>
  );
}

// --- Capability tiers: chips grouped by how much they say about the
// model. Modalities describe the interface, capabilities the tiered
// features routing cares about, and parameters the long tail.

export type CapabilityTier = {
  tier: "modalities" | "operations" | "capabilities" | "parameters";
  chips: string[];
};

const CORE_CAPABILITIES = ["tools", "reasoning", "structured_outputs"];

export function capabilityTiers(model: Model): CapabilityTier[] {
  const tiers: CapabilityTier[] = [];
  const inputs = model.architecture?.input_modalities ?? [];
  const outputs = model.architecture?.output_modalities ?? [];
  const modalities = [
    ...inputs.map((modality) => `${modality} in`),
    ...outputs.map((modality) => `${modality} out`),
  ];
  if (modalities.length > 0) tiers.push({ tier: "modalities", chips: modalities });

  // The operations say which path serves this model. Modalities alone leave
  // that open: a model that emits a picture may serve it on the chat path or
  // on the image path, and a caller has to pick one.
  const operations = operationsOf(model);
  if (operations.length > 0) tiers.push({ tier: "operations", chips: operations });

  const params = model.supported_parameters ?? [];
  const capabilities = CORE_CAPABILITIES.filter((capability) =>
    capability === "reasoning"
      ? params.includes("reasoning") || params.includes("include_reasoning")
      : params.includes(capability),
  );
  if (capabilities.length > 0) {
    tiers.push({
      tier: "capabilities",
      chips: capabilities.map((capability) => capability.replaceAll("_", " ")),
    });
  }

  const rest = params.filter(
    (parameter) =>
      !CORE_CAPABILITIES.includes(parameter) &&
      parameter !== "include_reasoning",
  );
  if (rest.length > 0) {
    tiers.push({
      tier: "parameters",
      chips: rest.map((parameter) => parameter.replaceAll("_", " ")),
    });
  }
  return tiers;
}

const TIER_TONES: Record<CapabilityTier["tier"], string> = {
  modalities: "bg-info-tint text-text-2",
  operations: "bg-accent-tint text-accent",
  capabilities: "bg-success-tint text-success",
  parameters: "bg-bg-raised text-text-3",
};

export function CapabilityChips({ model }: { model: Model }) {
  const tiers = capabilityTiers(model);
  if (tiers.length === 0) return null;
  return (
    <div className="flex flex-col gap-1.5">
      {tiers.map(({ tier, chips }) => (
        <div key={tier} className="flex flex-wrap items-center gap-1.5">
          {chips.map((chip) => (
            <span
              key={chip}
              className={`inline-flex h-5 items-center whitespace-nowrap rounded-xs px-1.5 text-xs font-medium ${TIER_TONES[tier]}`}
            >
              {chip}
            </span>
          ))}
        </div>
      ))}
    </div>
  );
}

// --- Offering table: one row per provider offering, doubling as a
// routing preview — the circuit column reads the same live states the
// router admits attempts against.

export function offeringCircuit(
  providers: ProviderRuntimeStatus[] | undefined,
  offering: ModelOffering,
): string | undefined {
  const runtime = providers?.find(
    (candidate) => candidate.provider_id === offering.provider,
  );
  return runtime?.offerings?.find(
    (candidate) => candidate.provider_model_id === offering.provider_model_id,
  )?.state;
}

// offeringAvailability summarizes a model's live routability: how many
// of its offerings sit on circuits the router still admits attempts
// against (healthy or half-open).
export function offeringAvailability(
  model: Model,
  providers: ProviderRuntimeStatus[] | undefined,
): { total: number; available: number } {
  const offerings = model.offerings ?? [];
  const available = offerings.filter((offering) => {
    const state = offeringCircuit(providers, offering);
    return state === "healthy" || state === "half_open";
  }).length;
  return { total: offerings.length, available };
}

const CIRCUIT_TONES: Record<string, string> = {
  healthy: "bg-success-tint text-success",
  half_open: "bg-warning-tint text-warning",
  open: "bg-error-tint text-error",
  unavailable: "bg-bg-raised text-text-3",
};

function price(value: string | undefined): string {
  return formatPricePerM(value) ?? "—";
}

export function OfferingTable({
  model,
  providers,
}: {
  model: Model;
  providers: ProviderRuntimeStatus[] | undefined;
}) {
  const offerings = model.offerings ?? [];
  if (offerings.length === 0) {
    return (
      <p className="text-sm text-text-3">
        No provider currently offers this model.
      </p>
    );
  }
  return (
    <div className="overflow-x-auto rounded-md border border-border-1">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border-1 text-left text-xs text-text-4">
            <th className="px-3 py-2 font-medium">Provider</th>
            <th className="px-3 py-2 font-medium">Provider model ID</th>
            <th className="px-3 py-2 font-medium">Context</th>
            <th className="px-3 py-2 font-medium">Max out</th>
            <th className="px-3 py-2 font-medium">Prompt /1M</th>
            <th className="px-3 py-2 font-medium">Completion /1M</th>
            <th className="px-3 py-2 font-medium">Cache read /1M</th>
            <th className="px-3 py-2 font-medium">Circuit</th>
            <th className="px-3 py-2 font-medium">Lifecycle</th>
          </tr>
        </thead>
        <tbody>
          {offerings.map((offering) => {
            const circuit = offeringCircuit(providers, offering);
            return (
              <tr
                key={`${offering.provider}/${offering.provider_model_id}`}
                className="border-b border-border-1 last:border-b-0"
              >
                <td className="px-3 py-2">
                  <Link
                    to="/providers/$providerId"
                    params={{ providerId: offering.provider }}
                    className="text-text-1 transition-colors duration-150 ease-standard hover:text-accent-link"
                  >
                    {providerLabel(offering.provider, offering.provider_name)}
                  </Link>
                </td>
                <td className="px-3 py-2 font-mono text-xs text-text-3">
                  {offering.provider_model_id}
                </td>
                <td className="px-3 py-2 tabular-nums text-text-2">
                  {formatContext(offering.context_length)}
                </td>
                <td className="px-3 py-2 tabular-nums text-text-2">
                  {offering.max_completion_tokens
                    ? formatCount(offering.max_completion_tokens)
                    : "—"}
                </td>
                <td className="px-3 py-2 tabular-nums text-text-2">
                  {price(offering.pricing?.prompt)}
                </td>
                <td className="px-3 py-2 tabular-nums text-text-2">
                  {price(offering.pricing?.completion)}
                </td>
                <td className="px-3 py-2 tabular-nums text-text-2">
                  {price(offering.pricing?.cache_read)}
                </td>
                <td className="px-3 py-2">
                  {circuit ? (
                    <span
                      className={`inline-flex h-5 items-center whitespace-nowrap rounded-xs px-1.5 text-xs font-medium ${
                        CIRCUIT_TONES[circuit] ?? "bg-bg-raised text-text-3"
                      }`}
                    >
                      {circuit.replaceAll("_", " ")}
                    </span>
                  ) : (
                    <span className="text-xs text-text-4">
                      {offering.availability ?? "—"}
                    </span>
                  )}
                </td>
                <td className="px-3 py-2 text-xs text-text-3">
                  {offering.lifecycle ?? "—"}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// --- Lineage: family, root, and parent links into the models list
// query until dedicated family pages exist.

export function lineageLinks(
  model: Model,
): Array<{ label: string; query: string }> {
  const lineage = model.lineage;
  if (!lineage) return [];
  const links: Array<{ label: string; query: string }> = [];
  if (lineage.family) links.push({ label: `family: ${lineage.family}`, query: lineage.family });
  if (lineage.parent && lineage.parent !== model.id) {
    links.push({ label: `parent: ${lineage.parent}`, query: lineage.parent });
  }
  if (
    lineage.root &&
    lineage.root !== model.id &&
    lineage.root !== lineage.parent
  ) {
    links.push({ label: `root: ${lineage.root}`, query: lineage.root });
  }
  return links;
}

export function LineageLinks({ model }: { model: Model }) {
  const links = lineageLinks(model);
  if (links.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-3">
      {links.map(({ label, query }) => (
        <Link
          key={label}
          to="/models"
          search={{ q: query }}
          className="text-xs text-accent-link transition-colors duration-150 ease-standard hover:underline"
        >
          {label}
        </Link>
      ))}
    </div>
  );
}
