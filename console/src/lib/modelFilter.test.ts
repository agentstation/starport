import { expect, test } from "vitest";

import type { Model } from "@/lib/api";

import {
  authorIdsOf,
  facetValues,
  fuzzyIncludes,
  hasCapability,
  joinFacet,
  matches,
  operationsOf,
  outputModalitiesOf,
  providerOf,
} from "./modelFilter";

const model: Model = {
  id: "meta/llama-3.1-8b-instruct",
  name: "Llama 3.1 8B Instruct",
  supported_parameters: ["tools", "include_reasoning"],
  architecture: { input_modalities: ["text"] },
  authors: [{ id: "meta", name: "Meta" }],
  tags: ["open-weights", "chat"],
  offerings: [
    { provider: "groq", provider_model_id: "llama-3.1-8b-instant" },
    { provider: "deepinfra", provider_model_id: "meta-llama/Meta-Llama-3.1-8B" },
  ],
};

test("provider filter matches serving providers, not just the id prefix", () => {
  expect(providerOf(model)).toBe("meta");
  expect(matches(model, { provider: "groq" })).toBe(true);
  expect(matches(model, { provider: "meta" })).toBe(true);
  expect(matches(model, { provider: "openai" })).toBe(false);
});

test("author facet matches declared authors", () => {
  expect(matches(model, { author: "meta" })).toBe(true);
  expect(matches(model, { author: "openai" })).toBe(false);
  // Entries without author metadata fall back to the id prefix.
  expect(authorIdsOf({ id: "mistral/mistral-large" })).toEqual(["mistral"]);
  expect(matches({ id: "mistral/mistral-large" }, { author: "mistral" })).toBe(
    true,
  );
});

test("tag facet matches declared tags", () => {
  expect(matches(model, { tag: "open-weights" })).toBe(true);
  expect(matches(model, { tag: "vision" })).toBe(false);
  expect(matches({ id: "x" }, { tag: "chat" })).toBe(false);
});

// Facet params hold comma-joined lists: values OR within one facet and
// AND across facets, so "groq or openai" narrows without emptying.
test("a facet with several values matches any of them", () => {
  expect(matches(model, { provider: "openai,groq" })).toBe(true);
  expect(matches(model, { provider: "openai,anthropic" })).toBe(false);
  expect(matches(model, { tag: "vision,chat" })).toBe(true);
  // Facets still AND together.
  expect(matches(model, { provider: "openai,groq", tag: "vision" })).toBe(false);
});

test("facet params round-trip through their URL form", () => {
  expect(facetValues(undefined)).toEqual([]);
  expect(facetValues("")).toEqual([]);
  expect(facetValues("groq,openai")).toEqual(["groq", "openai"]);
  expect(joinFacet([])).toBeUndefined();
  expect(joinFacet(["groq", "openai"])).toBe("groq,openai");
});

test("query matches provider model ids alongside the canonical id", () => {
  expect(matches(model, { q: "llama-3.1-8b-instant" })).toBe(true);
  expect(matches(model, { q: "meta-llama/meta-llama" })).toBe(true);
  expect(matches(model, { q: "gpt-4o" })).toBe(false);
});

test("fuzzy id match accepts an in-order subsequence", () => {
  expect(fuzzyIncludes("openai/gpt-4o", "gpt4o")).toBe(true);
  expect(fuzzyIncludes("meta/llama-3.1-8b-instruct", "llama318b")).toBe(true);
  expect(fuzzyIncludes("openai/gpt-4o", "4ogpt")).toBe(false);
  expect(matches(model, { q: "llama318b" })).toBe(true);
  // The author name is a fuzzy candidate too.
  expect(matches(model, { q: "Meta" })).toBe(true);
});

// An image model accepts text and returns a picture. Reading the input
// modalities alone cannot tell it from a chat model, which is why the output
// facet exists: the two reach different paths.
const imageModel: Model = {
  id: "openai/gpt-image-1",
  name: "GPT Image 1",
  architecture: { input_modalities: ["text"], output_modalities: ["image"] },
  offerings: [
    {
      provider: "openai",
      provider_model_id: "gpt-image-1",
      operations: ["images-edits", "images-generations"],
    },
  ],
};

test("output facet selects a model by what it produces", () => {
  expect(outputModalitiesOf(imageModel)).toEqual(["image"]);
  expect(matches(imageModel, { output: "image" })).toBe(true);
  // The text-only model accepts text and returns text. Its input modalities
  // name text too, so a facet that read the input half would keep it here.
  expect(matches(model, { output: "image" })).toBe(false);
  expect(matches(model, { output: "text" })).toBe(false);
});

test("a model with no stated output modality says nothing rather than text", () => {
  // A catalog entry that predates the field is unknown, not text-only. It is
  // absent from both answers instead of being claimed by the text one.
  expect(outputModalitiesOf({ id: "legacy/model" })).toEqual([]);
  expect(matches({ id: "legacy/model" }, { output: "text" })).toBe(false);
});

test("operations gather across the offerings that serve a model", () => {
  expect(operationsOf(imageModel)).toEqual(["images-edits", "images-generations"]);
  expect(operationsOf(model)).toEqual([]);
});

test("reasoning capability accepts include_reasoning", () => {
  expect(hasCapability(model, "reasoning")).toBe(true);
  expect(hasCapability(model, "tools")).toBe(true);
  expect(hasCapability(model, "structured_outputs")).toBe(false);
});

// RNK-V18. An operator confirming what the catalog serves needs to narrow the
// table to one operation. A model serving several answers to each of them.
test("operation facet selects a model by what it serves", () => {
  const reranker: Model = {
    id: "cohere/rerank-v3.5",
    offerings: [
      {
        provider: "cohere",
        provider_model_id: "rerank-v3.5",
        operations: ["rerank"],
      },
    ],
  };

  expect(matches(reranker, { operation: "rerank" })).toBe(true);
  expect(matches(reranker, { operation: "chat-completions" })).toBe(false);
  expect(matches(imageModel, { operation: "images-edits" })).toBe(true);
  // A model whose offerings name no operation is one the catalog did not
  // describe, and it answers to no operation rather than to all of them.
  expect(matches(model, { operation: "chat-completions" })).toBe(false);
});
