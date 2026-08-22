// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, test, vi } from "vitest";

import type { Model, ProviderRuntimeStatus } from "@/lib/api";

import {
  capabilityTiers,
  lineageLinks,
  ModelActions,
  offeringCircuit,
  OfferingTable,
} from "./ModelDetail";

// Unit tests run outside a live router: Link renders as a plain anchor
// with the resolved path and search string — the contract the page
// depends on.
vi.mock("@tanstack/react-router", () => ({
  Link: ({
    to,
    params,
    search,
    children,
    ...rest
  }: {
    to: string;
    params?: Record<string, string>;
    search?: Record<string, unknown>;
    children?: ReactNode;
  } & Record<string, unknown>) => {
    const path = Object.entries(params ?? {}).reduce(
      (resolved, [key, value]) => resolved.replace(`$${key}`, value),
      to,
    );
    const query = new URLSearchParams(
      Object.entries(search ?? {}).map(([key, value]) => [key, String(value)]),
    ).toString();
    return (
      <a href={query ? `${path}?${query}` : path} {...rest}>
        {children}
      </a>
    );
  },
}));

afterEach(() => {
  cleanup();
});

const model: Model = {
  id: "meta/llama-3.1-8b-instruct",
  name: "Llama 3.1 8B Instruct",
  context_length: 131072,
  supported_parameters: ["tools", "include_reasoning", "temperature", "top_p"],
  architecture: {
    input_modalities: ["text", "image"],
    output_modalities: ["text"],
  },
  offerings: [
    {
      provider: "groq",
      provider_name: "Groq",
      provider_model_id: "llama-3.1-8b-instant",
      context_length: 131072,
      max_completion_tokens: 8192,
      pricing: { prompt: "0.00000005", completion: "0.00000008" },
      lifecycle: "active",
    },
    {
      provider: "deepinfra",
      provider_model_id: "meta-llama/Meta-Llama-3.1-8B",
      availability: "available",
    },
  ],
};

const providers: ProviderRuntimeStatus[] = [
  {
    provider_id: "groq",
    offerings: [{ provider_model_id: "llama-3.1-8b-instant", state: "healthy" }],
  },
];

// --- Capability tiers

test("tiers modalities, core capabilities, and remaining parameters", () => {
  expect(capabilityTiers(model)).toEqual([
    { tier: "modalities", chips: ["text in", "image in", "text out"] },
    { tier: "capabilities", chips: ["tools", "reasoning"] },
    { tier: "parameters", chips: ["temperature", "top p"] },
  ]);
});

test("returns no tiers for a model without declared capabilities", () => {
  expect(capabilityTiers({ id: "x" })).toEqual([]);
});

// --- Actions

test("Open in chat seeds the chat composer with the model", () => {
  render(<ModelActions modelId={model.id} />);

  expect(screen.getByTestId("open-in-chat").getAttribute("href")).toBe(
    "/chat?model=meta%2Fllama-3.1-8b-instruct",
  );
});

test("Compare lands in chat with compare mode seeded", () => {
  render(<ModelActions modelId={model.id} />);

  expect(screen.getByTestId("add-to-comparison").getAttribute("href")).toBe(
    "/chat?model=meta%2Fllama-3.1-8b-instruct&compare=true",
  );
});

// --- Offering table

test("joins the live circuit state onto the matching offering", () => {
  const [groq, deepinfra] = model.offerings!;
  expect(offeringCircuit(providers, groq!)).toBe("healthy");
  expect(offeringCircuit(providers, deepinfra!)).toBeUndefined();
  expect(offeringCircuit(undefined, groq!)).toBeUndefined();
});

test("renders one row per offering with prices and provider links", () => {
  render(<OfferingTable model={model} providers={providers} />);

  expect(screen.getByText("Groq").closest("a")?.getAttribute("href")).toBe(
    "/providers/groq",
  );
  expect(screen.getByText("llama-3.1-8b-instant")).toBeDefined();
  // Per-million display of the per-token catalog price.
  expect(screen.getByText("$0.05")).toBeDefined();
  expect(screen.getByText("$0.08")).toBeDefined();
  expect(screen.getByText("healthy")).toBeDefined();
  // The offering without runtime state falls back to catalog availability.
  expect(screen.getByText("available")).toBeDefined();
});

test("states when no provider offers the model", () => {
  render(<OfferingTable model={{ id: "x" }} providers={providers} />);

  expect(
    screen.getByText("No provider currently offers this model."),
  ).toBeDefined();
});

// --- Lineage

test("links family, parent, and root without duplicates", () => {
  expect(
    lineageLinks({
      id: "meta/llama-3.1-8b-instruct",
      lineage: {
        family: "llama-3",
        parent: "meta/llama-3.1-405b-instruct",
        root: "meta/llama-3.1-405b-instruct",
      },
    }),
  ).toEqual([
    { label: "family: llama-3", query: "llama-3" },
    {
      label: "parent: meta/llama-3.1-405b-instruct",
      query: "meta/llama-3.1-405b-instruct",
    },
  ]);
  expect(lineageLinks({ id: "x" })).toEqual([]);
});
