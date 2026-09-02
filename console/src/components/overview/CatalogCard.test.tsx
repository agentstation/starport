// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { CatalogCard } from "./CatalogCard";

// The card's one read is the catalog metadata. The stub fails once and then
// answers, which is the shape a retry has to survive.
const gateway = vi.hoisted(() => ({ failures: 0, calls: 0 }));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    catalogMetadata: async () => {
      gateway.calls += 1;
      if (gateway.failures > 0) {
        gateway.failures -= 1;
        throw new Error("catalog metadata unreachable");
      }
      return {
        generation_id: "gen-0123456789abcdef",
        generated_at: "2026-09-01T00:00:00Z",
        catalog_sequence: 42,
        availability_revision: 7,
        completeness: "complete",
        manifest_available: true,
      };
    },
  };
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <CatalogCard />
    </QueryClientProvider>,
  );
}

test("the card holds its shape while the read runs", async () => {
  gateway.failures = 0;
  mount();

  const status = screen.getByRole("status");
  expect(status.getAttribute("aria-busy")).toBe("true");
  expect(status.textContent).toBe("Loading");

  await screen.findByText("Catalog sequence");
  expect(screen.queryByRole("status")).toBeNull();
});

test("a failed read names the failure and a retry runs the read again", async () => {
  gateway.failures = 1;
  gateway.calls = 0;
  mount();

  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("Could not load the Starmap catalog");
  expect(alert.textContent).toContain("catalog metadata unreachable");

  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await screen.findByText("Catalog sequence");
  expect(gateway.calls).toBe(2);
  expect(screen.queryByRole("alert")).toBeNull();
});
