import type { Model } from "@/lib/api";

// The models-list filter predicate. Filter state lives in the URL, so
// this seam defines what each search param means.
export type ModelsSearch = {
  q?: string;
  provider?: string;
  author?: string;
  tag?: string;
  modality?: string;
  capability?: string;
};

// providerOf mirrors the gateway's model ID shape: "<author>/<model>".
export function providerOf(model: Model): string {
  const slash = model.id.indexOf("/");
  return slash > 0 ? model.id.slice(0, slash) : "";
}

// authorIdsOf returns the declared authors, falling back to the id
// prefix for catalog entries that predate author metadata.
export function authorIdsOf(model: Model): string[] {
  const declared = (model.authors ?? [])
    .map((author) => author.id)
    .filter(Boolean);
  if (declared.length > 0) return declared;
  const prefix = providerOf(model);
  return prefix ? [prefix] : [];
}

export function hasCapability(model: Model, capability: string): boolean {
  const params = model.supported_parameters ?? [];
  if (capability === "reasoning") {
    return params.includes("reasoning") || params.includes("include_reasoning");
  }
  return params.includes(capability);
}

// fuzzyIncludes accepts a plain substring match, or the query as an
// in-order subsequence once separators are stripped, so "gpt4o" finds
// "openai/gpt-4o". Candidates stay short (ids, names), which keeps
// subsequence false positives rare.
export function fuzzyIncludes(candidate: string, query: string): boolean {
  const haystack = candidate.toLowerCase();
  const needle = query.toLowerCase();
  if (haystack.includes(needle)) return true;
  const compact = needle.replace(/[^a-z0-9]/g, "");
  if (compact.length === 0) return false;
  let matched = 0;
  for (const char of haystack) {
    if (char === compact[matched]) {
      matched += 1;
      if (matched === compact.length) return true;
    }
  }
  return false;
}

// queryCandidates lists the strings a fuzzy query may land on: the
// canonical id, the display name, author ids and names, and every
// provider model id an offering serves.
function queryCandidates(model: Model): string[] {
  return [
    model.id,
    model.name ?? "",
    ...(model.authors ?? []).flatMap((author) => [author.id, author.name ?? ""]),
    ...(model.offerings ?? []).map((offering) => offering.provider_model_id),
  ].filter(Boolean);
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
  if (search.author && !authorIdsOf(model).includes(search.author)) return false;
  if (search.tag && !(model.tags ?? []).includes(search.tag)) return false;
  if (
    search.modality &&
    !(model.architecture?.input_modalities ?? []).includes(search.modality)
  ) {
    return false;
  }
  if (search.capability && !hasCapability(model, search.capability)) return false;
  if (search.q) {
    const query = search.q;
    if (!queryCandidates(model).some((candidate) => fuzzyIncludes(candidate, query))) {
      return false;
    }
  }
  return true;
}
