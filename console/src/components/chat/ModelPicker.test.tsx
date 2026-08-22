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
