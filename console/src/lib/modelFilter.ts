import type { Model } from "@/lib/api";

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

// CHAT_OPERATION is the catalog's own name for a chat completion. A model
// answers a chat turn only through an offering that serves it.
export const CHAT_OPERATION = "chat-completions";

// chattableModels keeps the models that answer a chat turn.
//
// An embedding model, a reranker, a document reader, a speech model, and an
// image generator each reach this gateway through their own route, not through
// the model field of a chat request. Offering one in a chat picker hands the
// reader a routing refusal that says nothing about the mistake. The test is
// positive: an offering that names chat completions answers, and one that
// names only other operations does not. A model that serves chat beside one of
// them stays.
//
// A model with no offerings stays, and so does an offering that names no
// operation. Each is a catalog this console could not read rather than a model
// that serves nothing, and hiding it would shrink the picker over a fact
// nobody established.
export function chattableModels(models: Model[]): Model[] {
  return models.filter((model) => {
    const offerings = model.offerings ?? [];
    if (offerings.length === 0) return true;
    return offerings.some((offering) => answersChatTurn(offering));
  });
}

function answersChatTurn(offering: { operations?: string[] }): boolean {
  const operations = offering.operations ?? [];
  return operations.length === 0 || operations.includes(CHAT_OPERATION);
}

// PRESET_PREFIX marks a preset id in a model field. A preset is the reader's
// own routing choice, so the default rule never replaces one.
const PRESET_PREFIX = "@preset/";

// defaultChatModel picks the model a new conversation opens on.
//
// The model the reader chose last wins while the catalog still routes it as a
// chat model, and a preset always wins. Otherwise the first chat model in
// catalog order that a provider with a usable operator credential serves, so
// a default the reader never chose is one that can answer. Without such a
// provider the first chat model stands in, and without any chat model the
// first catalog model does.
export function defaultChatModel(
  remembered: string,
  models: Model[],
  usableProviders: ReadonlySet<string>,
): string {
  if (remembered.startsWith(PRESET_PREFIX)) return remembered;
  const candidates = chattableModels(models);
  if (remembered && candidates.some((model) => model.id === remembered)) {
    return remembered;
  }
  const credentialed = usableProviders.size
    ? candidates.find((model) =>
        (model.offerings ?? []).some((offering) =>
          usableProviders.has(offering.provider),
        ),
      )
    : undefined;
  return (credentialed ?? candidates[0] ?? models[0])?.id ?? "";
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
