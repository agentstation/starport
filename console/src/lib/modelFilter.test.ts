import { expect, test } from "vitest";

import type { Model } from "@/lib/api";

import {
  authorIdsOf,
  fuzzyIncludes,
  hasCapability,
  matches,
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

test("reasoning capability accepts include_reasoning", () => {
  expect(hasCapability(model, "reasoning")).toBe(true);
  expect(hasCapability(model, "tools")).toBe(true);
  expect(hasCapability(model, "structured_outputs")).toBe(false);
});
