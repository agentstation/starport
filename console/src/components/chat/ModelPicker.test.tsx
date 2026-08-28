// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { ModelPicker } from "./ModelPicker";

// The picker states capability from the catalog and nothing else. Starmap owns
// the per-model web-search fact and Starport publishes it as the
// web_search_options supported parameter, so a badge appears exactly when the
// catalog says the model can search.
vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    listModels: async () => [
      {
        id: "openai/gpt-4o-search-preview",
        name: "GPT-4o Search",
        supported_parameters: ["tools", "tool_choice", "web_search_options"],
      },
      {
        id: "meta/llama-3.1-8b-instruct",
        name: "Llama 3.1 8B",
        supported_parameters: ["tools", "tool_choice"],
      },
      {
        id: "mistral/mistral-ocr",
        name: "Mistral OCR",
        offerings: [
          {
            provider: "mistral",
            provider_model_id: "mistral-ocr-2505",
            operations: ["documents-recognition"],
          },
        ],
      },
      {
        id: "cohere/rerank-v3.5",
        name: "Rerank 3.5",
        offerings: [
          {
            provider: "cohere",
            provider_model_id: "rerank-v3.5",
            operations: ["rerank"],
          },
        ],
      },
      {
        id: "google/gemini-2.5-flash",
        name: "Gemini 2.5 Flash",
        offerings: [
          {
            provider: "google",
            provider_model_id: "gemini-2.5-flash",
            operations: ["chat-completions", "documents-recognition"],
          },
        ],
      },
    ],
    listPresets: async () => [],
    listProviderCatalog: async () => [],
  };
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <ModelPicker
        value="meta/llama-3.1-8b-instruct"
        favorites={new Set()}
        onToggleFavorite={vi.fn()}
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />
    </QueryClientProvider>,
  );
}

function rowFor(name: string): HTMLElement {
  const label = screen.getByText(name);
  const row = label.closest("[role='option']") ?? label.parentElement?.parentElement;
  if (!row) throw new Error(`no row for ${name}`);
  return row as HTMLElement;
}

test("web search badge follows the catalog parameter", async () => {
  mount();
  await waitFor(() => expect(screen.getByText("GPT-4o Search")).toBeTruthy());

  expect(rowFor("GPT-4o Search").querySelector("[aria-label='Web search']")).toBeTruthy();
  expect(rowFor("Llama 3.1 8B").querySelector("[aria-label='Web search']")).toBeNull();

  // Tool support is a separate fact and must keep reading independently.
  expect(rowFor("Llama 3.1 8B").querySelector("[aria-label='Tools']")).toBeTruthy();
});

// PLG-V18. A model that only reads documents answers no chat turn. It reaches
// this gateway through the file-parser plugin instead, so picking one from the
// chat picker returns a routing refusal that names nothing the reader did
// wrong. The catalog is the only place that says which models those are.
test("the chat picker omits a model that only reads documents", async () => {
  mount();
  await waitFor(() => expect(screen.getByText("Llama 3.1 8B")).toBeTruthy());

  expect(screen.queryByText("Mistral OCR")).toBeNull();

  // A model that reads documents and also answers chat stays. The exclusion is
  // about what a model cannot do, not about the operation being present.
  expect(screen.getByText("Gemini 2.5 Flash")).toBeTruthy();
});

// RNK-V18. A reranker scores a document list against one query and returns no
// message. It reaches this gateway through /v1/rerank, so naming one in the
// model field of a chat request is a routing refusal the reader cannot act on.
test("the chat picker omits a model that only reranks", async () => {
  mount();
  await waitFor(() => expect(screen.getByText("Llama 3.1 8B")).toBeTruthy());

  expect(screen.queryByText("Rerank 3.5")).toBeNull();

  // The exclusion reads the whole operation list rather than looking for one
  // name in it, so a model that reranks and also answers chat stays.
  expect(screen.getByText("Gemini 2.5 Flash")).toBeTruthy();
});
