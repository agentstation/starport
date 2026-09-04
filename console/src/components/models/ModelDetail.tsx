import { Link } from "@tanstack/react-router";
import { MessageSquare, SplitSquareHorizontal } from "lucide-react";

import type {
  Model,
  ModelOffering,
  ProviderOfferingStatus,
  ProviderRuntimeStatus,
} from "@/lib/api";
import {
  formatContext,
  formatCount,
  formatPricePair,
  formatPricePerM,
  formatUnitPrice,
  providerLabel,
  shortGenerationID,
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

// TIER_LABELS name each chip row. A row of bare chips leaves a reader to
// guess whether "image out" is a modality or an operation.
const TIER_LABELS: Record<CapabilityTier["tier"], string> = {
  modalities: "Modalities",
  operations: "Operations",
  capabilities: "Capabilities",
  parameters: "Parameters",
};

export function CapabilityChips({ model }: { model: Model }) {
  const tiers = capabilityTiers(model);
  if (tiers.length === 0) return null;
  return (
    <div className="flex flex-col gap-1.5">
      {tiers.map(({ tier, chips }) => (
        <div
          key={tier}
          role="group"
          aria-label={TIER_LABELS[tier]}
          className="flex flex-wrap items-center gap-1.5"
        >
          <span className="w-24 shrink-0 text-xs text-text-3">
            {TIER_LABELS[tier]}
          </span>
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

// offeringRuntime finds the live projection of one offering. Circuit state,
// routing state, and the reason each come from it, and each stays a value of
// its own: a router that refuses a route says nothing about the circuit, and
// an open circuit says nothing about the credential that pays.
export function offeringRuntime(
  providers: ProviderRuntimeStatus[] | undefined,
  offering: ModelOffering,
): ProviderOfferingStatus | undefined {
  return providers
    ?.find((candidate) => candidate.provider_id === offering.provider)
    ?.offerings?.find(
      (candidate) => candidate.provider_model_id === offering.provider_model_id,
    );
}

// offeringCredential reads the operator credential that pays this provider.
// Availability is credential-specific: the same offering is usable for a
// deployment that holds a working credential and unusable for one that does
// not, and no catalog fact states that.
export function offeringCredential(
  providers: ProviderRuntimeStatus[] | undefined,
  offering: ModelOffering,
): { state?: string; usable?: boolean } | undefined {
  return providers?.find((candidate) => candidate.provider_id === offering.provider)
    ?.operator_credential;
}

// credentialText says what the credential means for this offering in one
// word. A credential the gateway can use reads usable; one it cannot reads
// the state the gateway recorded; a provider with none reads none.
export function credentialText(
  credential: { state?: string; usable?: boolean } | undefined,
): string {
  if (!credential) return "none";
  if (credential.usable) return "usable";
  return credential.state ?? "unusable";
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

// The prompt and completion prices render as the console-wide pair (the
// same string the models table shows), so a reader compares one cell here
// with one cell there. The cache-read price keeps its own column because
// only some offerings publish one.
function price(value: string | undefined): string {
  return formatPricePerM(value) ?? "—";
}

// unitPrice names the price of one whole unit and the unit it buys, because
// the number alone reads as a token price beside four of them.
//
// A rerank offering that bills search units publishes no token price at all,
// and a document reader publishes a page price beside them. Either one shows
// four dashes across the token columns, so an offering with a real published
// price would read as one nobody priced.
function unitPrice(offering: ModelOffering): string {
  const searchUnit = formatUnitPrice(offering.pricing?.search_unit);
  if (searchUnit !== null) return `${searchUnit} / search`;
  const page = formatUnitPrice(offering.pricing?.page_input);
  if (page !== null) return `${page} / page`;
  return "—";
}

export function OfferingTable({
  model,
  providers,
  generation,
}: {
  model: Model;
  providers: ProviderRuntimeStatus[] | undefined;
  // generation is the catalog generation the offering facts came from. It is
  // the provenance of every row: a reader who compares two deployments needs
  // to know which catalog each one read.
  generation?: string;
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
          <tr className="border-b border-border-1 text-left text-xs font-medium text-text-3">
            <th scope="col" className="px-4 py-2.5">Provider</th>
            <th scope="col" className="px-4 py-2.5">Provider model ID</th>
            <th scope="col" className="px-4 py-2.5 text-right">Context</th>
            <th scope="col" className="px-4 py-2.5 text-right">Max out</th>
            <th scope="col" className="px-4 py-2.5 text-right">Price / M</th>
            <th scope="col" className="px-4 py-2.5 text-right">Cache read / M</th>
            <th scope="col" className="px-4 py-2.5 text-right">Unit price</th>
            <th scope="col" className="px-4 py-2.5 text-right">Max docs</th>
            <th scope="col" className="px-4 py-2.5">Lifecycle</th>
            <th scope="col" className="px-4 py-2.5">Availability</th>
            <th scope="col" className="px-4 py-2.5">Credential</th>
            <th scope="col" className="px-4 py-2.5">Circuit</th>
            <th scope="col" className="px-4 py-2.5">Routing</th>
          </tr>
        </thead>
        <tbody>
          {offerings.map((offering) => {
            const circuit = offeringCircuit(providers, offering);
            const routing = offeringRuntime(providers, offering)?.routing?.state;
            const credential = offeringCredential(providers, offering);
            return (
              <tr
                key={`${offering.provider}/${offering.provider_model_id}`}
                className="border-b border-border-1 last:border-b-0"
              >
                <td className="px-4 py-2.5">
                  <Link
                    to="/providers/$providerId"
                    params={{ providerId: offering.provider }}
                    className="text-text-1 transition-colors duration-150 ease-standard hover:text-accent-link"
                  >
                    {providerLabel(offering.provider, offering.provider_name)}
                  </Link>
                </td>
                <td className="px-4 py-2.5 font-mono text-xs text-text-3">
                  {offering.provider_model_id}
                </td>
                <td className="px-4 py-2.5 text-right tabular-nums text-text-2">
                  {formatContext(offering.context_length)}
                </td>
                <td className="px-4 py-2.5 text-right tabular-nums text-text-2">
                  {offering.max_completion_tokens
                    ? formatCount(offering.max_completion_tokens)
                    : "—"}
                </td>
                <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-text-2">
                  {formatPricePair(
                    offering.pricing?.prompt,
                    offering.pricing?.completion,
                  ) ?? "—"}
                </td>
                <td className="px-4 py-2.5 text-right tabular-nums text-text-2">
                  {price(offering.pricing?.cache_read)}
                </td>
                <td className="whitespace-nowrap px-4 py-2.5 text-right tabular-nums text-text-2">
                  {unitPrice(offering)}
                </td>
                <td className="px-4 py-2.5 text-right tabular-nums text-text-2">
                  {offering.max_documents ? formatCount(offering.max_documents) : "—"}
                </td>
                <td className="px-4 py-2.5 text-xs text-text-3">
                  <span data-testid="offering-lifecycle">{offering.lifecycle ?? "—"}</span>
                </td>
                <td className="px-4 py-2.5 text-xs text-text-3">
                  <span data-testid="offering-availability">
                    {offering.availability ?? "—"}
                  </span>
                </td>
                <td className="px-4 py-2.5 text-xs text-text-3">
                  <span data-testid="offering-credential">{credentialText(credential)}</span>
                </td>
                <td className="px-4 py-2.5">
                  <span
                    data-testid="offering-circuit"
                    className={
                      circuit
                        ? `inline-flex h-5 items-center whitespace-nowrap rounded-xs px-1.5 text-xs font-medium ${
                            CIRCUIT_TONES[circuit] ?? "bg-bg-raised text-text-3"
                          }`
                        : "text-xs text-text-3"
                    }
                  >
                    {circuit ? circuit.replaceAll("_", " ") : "—"}
                  </span>
                </td>
                <td className="px-4 py-2.5 text-xs text-text-3">
                  <span data-testid="offering-routing">
                    {routing ? routing.replaceAll("_", " ") : "—"}
                  </span>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <p data-testid="offering-provenance" className="px-4 py-2.5 text-xs text-text-3">
        {generation
          ? `These offerings come from catalog generation ${shortGenerationID(generation)}.`
          : "The catalog generation behind these offerings is not reported."}
      </p>
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
