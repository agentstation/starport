// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { FreshnessBar } from "./FreshnessBar";

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    catalogMetadata: async () => ({
      generation_id: "gen-0123456789abcdef",
      generated_at: "2026-09-01T00:00:00Z",
      catalog_sequence: 42,
      availability_revision: 7,
      completeness: "complete",
      manifest_available: true,
    }),
  };
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <FreshnessBar />
    </QueryClientProvider>,
  );
}

test("details opens a popover and Escape closes it", async () => {
  mount();
  const trigger = await screen.findByRole("button", { name: "Details" });
  expect(screen.queryByRole("dialog", { name: "Catalog details" })).toBeNull();

  fireEvent.click(trigger);
  const dialog = await screen.findByRole("dialog", { name: "Catalog details" });
  expect(dialog.textContent).toContain("gen-0123456789abcdef");
  expect(dialog.textContent).toContain("42");

  fireEvent.keyDown(dialog, { key: "Escape" });
  await waitFor(() => {
    expect(screen.queryByRole("dialog", { name: "Catalog details" })).toBeNull();
  });
});
