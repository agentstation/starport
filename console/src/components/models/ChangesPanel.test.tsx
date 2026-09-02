// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import type { CatalogChanges } from "@/lib/api";

import { ChangesPanel } from "./ChangesPanel";

// The panel's one read is the generation diff. Each test sets the diff
// the stub answers with.
const gateway = vi.hoisted(() => ({
  diff: {} as Record<string, unknown>,
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    catalogChanges: async () => gateway.diff,
  };
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <ChangesPanel onClose={() => {}} />
    </QueryClientProvider>,
  );
}

const base: CatalogChanges = {
  available: true,
  from_generation_id: "gen-aaaaaaaaaaaaaaaa",
  to_generation_id: "gen-bbbbbbbbbbbbbbbb",
  to_generated_at: "2026-09-01T00:00:00Z",
  semantically_equal: false,
};

test("a diff with nothing in it says so instead of rendering an empty panel", async () => {
  gateway.diff = { ...base };
  mount();

  const empty = await screen.findByTestId("no-changes");
  expect(empty.textContent).toContain("No changes since the previous generation.");
  expect(empty.textContent).toContain(
    "The two generations list the same models, offerings, and prices.",
  );
});

test("a semantically equal diff reads the same lead and names the metadata", async () => {
  gateway.diff = { ...base, semantically_equal: true };
  mount();

  const empty = await screen.findByTestId("no-changes");
  expect(empty.textContent).toContain("No changes since the previous generation.");
  expect(empty.textContent).toContain("Only acquisition metadata differs");
});

test("a diff with content lists it and shows no empty state", async () => {
  gateway.diff = { ...base, models_added: ["openai/gpt-6"] };
  mount();

  await screen.findByText("openai/gpt-6");
  expect(screen.queryByTestId("no-changes")).toBeNull();
});

test("a diff without history leads with the answer and reads the reason as a sentence", async () => {
  gateway.diff = {
    available: false,
    reason: "fewer than two accepted generations are recorded; nothing to compare yet",
  };
  mount();

  const empty = await screen.findByTestId("no-history");
  expect(empty.textContent).toBe(
    "Nothing to compare yet.Fewer than two accepted generations are recorded; nothing to compare yet.",
  );
});
