// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { CommandPalette } from "./CommandPalette";
import { searchPalette, type PaletteItem } from "./paletteIndex";

const navigate = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    hasCredential: () => true,
    listModels: async () => [
      { id: "openai/gpt-oss-120b", name: "GPT-OSS 120B" },
      { id: "meta/llama-3.1-8b-instruct", name: "Llama 3.1 8B" },
    ],
    listProviderCatalog: async () => [{ id: "groq", name: "Groq" }],
    listAuthors: async () => [{ id: "anthropic", name: "Anthropic" }],
  };
});

afterEach(() => {
  cleanup();
  navigate.mockClear();
});

// --- searchPalette (pure index contract)

const INDEX: PaletteItem[] = [
  { kind: "page", id: "/models", label: "Models", hint: "/models" },
  { kind: "action", id: "toggle-theme", label: "Toggle theme", keywords: ["dark"] },
  { kind: "model", id: "openai/gpt-oss-120b", label: "openai/gpt-oss-120b" },
  { kind: "provider", id: "groq", label: "Groq", hint: "groq" },
  { kind: "author", id: "anthropic", label: "Anthropic", hint: "anthropic" },
];

test("finds one of each entity kind by fuzzy query", () => {
  // In-order subsequence, same contract as the models-list search.
  expect(searchPalette("gptoss120", INDEX).map((item) => item.kind)).toEqual([
    "model",
  ]);
  expect(searchPalette("groq", INDEX).map((item) => item.kind)).toEqual([
    "provider",
  ]);
  expect(searchPalette("anthro", INDEX).map((item) => item.kind)).toEqual([
    "author",
  ]);
  expect(searchPalette("models", INDEX).map((item) => item.kind)).toEqual([
    "page",
  ]);
  expect(searchPalette("dark", INDEX).map((item) => item.kind)).toEqual([
    "action",
  ]);
});

test("an empty query shows pages and actions, not entities", () => {
  const kinds = new Set(searchPalette("", INDEX).map((item) => item.kind));
  expect(kinds).toEqual(new Set(["page", "action"]));
});

test("substring matches rank before subsequence-only matches", () => {
  const items: PaletteItem[] = [
    // `meta` is an in-order subsequence of this long name only.
    {
      kind: "model",
      id: "google/aqa",
      label: "google/aqa",
      hint: "Model that performs Attributed Question Answering",
    },
    {
      kind: "model",
      id: "meta/llama-3.1-8b-instruct",
      label: "meta/llama-3.1-8b-instruct",
    },
  ];
  expect(searchPalette("meta", items).map((item) => item.id)).toEqual([
    "meta/llama-3.1-8b-instruct",
    "google/aqa",
  ]);
});

test("each kind is capped so one kind cannot flood the list", () => {
  const models: PaletteItem[] = Array.from({ length: 40 }, (_, index) => ({
    kind: "model",
    id: `openai/model-${index}`,
    label: `openai/model-${index}`,
  }));
  expect(searchPalette("openai", models)).toHaveLength(6);
});

// --- Component keyboard traversal

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <CommandPalette />
    </QueryClientProvider>,
  );
}

test("⌘K opens the palette and keyboard-only traversal navigates", async () => {
  mount();
  expect(screen.queryByRole("dialog")).toBeNull();

  fireEvent.keyDown(document, { key: "k", metaKey: true });
  const input = await screen.findByLabelText("Search everything");

  fireEvent.change(input, { target: { value: "gptoss120" } });
  await waitFor(() => {
    expect(screen.getByText("openai/gpt-oss-120b")).toBeTruthy();
  });

  fireEvent.keyDown(input, { key: "Enter" });
  expect(navigate).toHaveBeenCalledWith({
    to: "/models/$modelId",
    params: { modelId: "openai/gpt-oss-120b" },
  });
  // Enter closes the palette.
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("escape closes without navigating", async () => {
  mount();
  fireEvent.keyDown(document, { key: "k", ctrlKey: true });
  const input = await screen.findByLabelText("Search everything");
  fireEvent.keyDown(input, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(navigate).not.toHaveBeenCalled();
});
