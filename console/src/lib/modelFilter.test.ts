import { expect, test } from "vitest";

import type { Model } from "@/lib/api";

import { hasCapability, matches, providerOf } from "./modelFilter";

const model: Model = {
  id: "meta/llama-3.1-8b-instruct",
  name: "Llama 3.1 8B Instruct",
  supported_parameters: ["tools", "include_reasoning"],
  architecture: { input_modalities: ["text"] },
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

test("query matches provider model ids alongside the canonical id", () => {
  expect(matches(model, { q: "llama-3.1-8b-instant" })).toBe(true);
  expect(matches(model, { q: "meta-llama/meta-llama" })).toBe(true);
  expect(matches(model, { q: "gpt-4o" })).toBe(false);
});

test("reasoning capability accepts include_reasoning", () => {
  expect(hasCapability(model, "reasoning")).toBe(true);
  expect(hasCapability(model, "tools")).toBe(true);
  expect(hasCapability(model, "structured_outputs")).toBe(false);
});
