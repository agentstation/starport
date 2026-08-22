import type { Model } from "@/lib/api";

// The models-list filter predicate. Filter state lives in the URL, so
// this seam defines what each search param means.
export type ModelsSearch = {
  q?: string;
  provider?: string;
  modality?: string;
  capability?: string;
};

// providerOf mirrors the gateway's model ID shape: "<provider>/<model>".
export function providerOf(model: Model): string {
  const slash = model.id.indexOf("/");
  return slash > 0 ? model.id.slice(0, slash) : "";
}

export function hasCapability(model: Model, capability: string): boolean {
  const params = model.supported_parameters ?? [];
  if (capability === "reasoning") {
    return params.includes("reasoning") || params.includes("include_reasoning");
  }
  return params.includes(capability);
}

export function matches(model: Model, search: ModelsSearch): boolean {
  // The provider filter matches the providers that actually serve the
  // model (its offerings), not just the id prefix — a canonical model
  // like meta/llama-… routes through providers the prefix never names.
  if (
    search.provider &&
    providerOf(model) !== search.provider &&
    !(model.offerings ?? []).some(
      (offering) => offering.provider === search.provider,
    )
  ) {
    return false;
  }
  if (
    search.modality &&
    !(model.architecture?.input_modalities ?? []).includes(search.modality)
  ) {
    return false;
  }
  if (search.capability && !hasCapability(model, search.capability)) return false;
  if (search.q) {
    const offeringIds = (model.offerings ?? [])
      .map((offering) => offering.provider_model_id)
      .join(" ");
    const haystack =
      `${model.id} ${model.name ?? ""} ${offeringIds}`.toLowerCase();
    if (!haystack.includes(search.q.toLowerCase())) return false;
  }
  return true;
}
