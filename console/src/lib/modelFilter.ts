import { RECOGNITION_OPERATION, RERANK_OPERATION, type Model } from "@/lib/api";

// The models-list filter predicate. Filter state lives in the URL, so
// this seam defines what each search param means. Every facet param is
// a comma-joined list: values within one facet OR together, facets AND
// together. A single value is the one-element list, so old links keep
// working.
export type ModelsSearch = {
  q?: string;
  provider?: string;
  author?: string;
  tag?: string;
  modality?: string;
  output?: string;
  capability?: string;
  operation?: string;
};

// facetValues reads one facet param into its value list.
export function facetValues(raw: string | undefined): string[] {
  return raw ? raw.split(",").filter(Boolean) : [];
}

// joinFacet writes a value list back into URL form; an empty list drops
// the param entirely.
export function joinFacet(values: string[]): string | undefined {
  return values.length > 0 ? values.join(",") : undefined;
}

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

// outputModalitiesOf reads what a model produces. A catalog entry that
// predates the field says nothing rather than claiming text, because a
// missing fact and a text-only model are different answers.
export function outputModalitiesOf(model: Model): string[] {
  return model.architecture?.output_modalities ?? [];
}

// operationsOf lists every operation the model's offerings serve. A media
// model reaches its own path, so this is what tells a reader which one.
export function operationsOf(model: Model): string[] {
  const seen = new Set<string>();
  for (const offering of model.offerings ?? []) {
    for (const operation of offering.operations ?? []) seen.add(operation);
  }
  return [...seen].sort();
}

// SILENT_OPERATIONS are the operations that answer no chat turn. A document
// reader returns the text it read and a reranker returns scores, and each
// reaches this gateway through its own route rather than through the model
// field of a chat request.
const SILENT_OPERATIONS = new Set<string>([
  RECOGNITION_OPERATION,
  RERANK_OPERATION,
]);

// chattableModels drops the models that answer no chat turn.
//
// A document reader or a reranker reaches this gateway through its own route,
// not through the model field, so offering one in a chat picker hands the
// reader a routing refusal that says nothing about the mistake. A model that
// serves one of them beside chat stays: it can answer, and these are chat
// pickers.
//
// A model with no offerings stays too. That is a catalog this console could not
// read rather than a model that serves nothing, and hiding it would shrink the
// picker over a fact nobody established.
export function chattableModels(models: Model[]): Model[] {
  return models.filter((model) => {
    const offerings = model.offerings ?? [];
    if (offerings.length === 0) return true;
    return !offerings.every((offering) => answersNoChatTurn(offering));
  });
}

// answersNoChatTurn reads one offering. A silent operation beside any other one
// is a model that answers, so the test is what the list holds apart from the
// silent ones, not whether a silent one is in it. An offering that names no
// operation is one the catalog did not describe, and it is not excluded.
function answersNoChatTurn(offering: { operations?: string[] }): boolean {
  const operations = offering.operations ?? [];
  if (operations.length === 0) return false;
  return operations.every((operation) => SILENT_OPERATIONS.has(operation));
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

// passes reads one facet: an empty selection filters nothing, and a
// model passes when ANY selected value matches (OR within a facet).
function passes(raw: string | undefined, test: (value: string) => boolean): boolean {
  const values = facetValues(raw);
  return values.length === 0 || values.some(test);
}

export function matches(model: Model, search: ModelsSearch): boolean {
  // The provider filter matches the providers that actually serve the
  // model (its offerings), not just the id prefix — a canonical model
  // like meta/llama-… routes through providers the prefix never names.
  if (
    !passes(
      search.provider,
      (provider) =>
        providerOf(model) === provider ||
        (model.offerings ?? []).some(
          (offering) => offering.provider === provider,
        ),
    )
  ) {
    return false;
  }
  if (!passes(search.author, (author) => authorIdsOf(model).includes(author))) {
    return false;
  }
  if (!passes(search.tag, (tag) => (model.tags ?? []).includes(tag))) {
    return false;
  }
  if (
    !passes(search.modality, (modality) =>
      (model.architecture?.input_modalities ?? []).includes(modality),
    )
  ) {
    return false;
  }
  if (
    !passes(search.output, (output) =>
      outputModalitiesOf(model).includes(output),
    )
  ) {
    return false;
  }
  if (
    !passes(search.capability, (capability) => hasCapability(model, capability))
  ) {
    return false;
  }
  if (
    !passes(search.operation, (operation) =>
      operationsOf(model).includes(operation),
    )
  ) {
    return false;
  }
  if (search.q) {
    const query = search.q;
    if (!queryCandidates(model).some((candidate) => fuzzyIncludes(candidate, query))) {
      return false;
    }
  }
  return true;
}
