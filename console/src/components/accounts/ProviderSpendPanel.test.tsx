// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { ApiError, type ProviderUsagePage } from "@/lib/api";

import { ProviderSpendPanel } from "./ProviderSpendPanel";

// The panel reports where an account's spend went and, as loudly, what the
// rollup could not count: a walk that hit its bound, and requests the
// gateway could not price.
const gateway = vi.hoisted(() => ({
  page: null as ProviderUsagePage | null,
  error: null as Error | null,
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    accountProviderUsage: async () => {
      if (gateway.error) throw gateway.error;
      return gateway.page ?? {};
    },
  };
});

afterEach(() => {
  cleanup();
  gateway.page = null;
  gateway.error = null;
});

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ProviderSpendPanel accountId="acme" />
    </QueryClientProvider>,
  );
}

// Rows sort by spend, the biggest first, and every honesty note under the
// table names what the numbers leave out.
test("lists provider spend with the truncation and unpriced notes", async () => {
  gateway.page = {
    data: [
      { provider: "groq", requests: 4, errors: 1, tokens: { total: 400 }, spend_nano_usd: 2_000_000_000, requests_without_cost: 0 },
      { provider: "openai", requests: 10, errors: 0, tokens: { total: 9_000 }, spend_nano_usd: 5_500_000_000, requests_without_cost: 3 },
    ],
    window: { since: "2026-08-03T00:00:00Z", until: "2026-09-02T00:00:00Z" },
    truncated: true,
  };
  mount();

  const table = await screen.findByRole("table", { name: "Provider spend" });
  const rows = within(table).getAllByRole("row").slice(1);
  expect(rows.map((row) => within(row).getAllByRole("cell")[0]?.textContent)).toEqual([
    "openai",
    "groq",
  ]);
  expect(within(rows[0]!).getAllByRole("cell").map((cell) => cell.textContent)).toEqual([
    "openai",
    "10",
    "0",
    "9,000",
    "$5.50",
  ]);
  expect(
    screen.getByText("The rollup stopped at its record bound, so these rows understate the window."),
  ).toBeTruthy();
  expect(screen.getByText("3 requests without a price are not in the spend.")).toBeTruthy();
});

// Nothing recorded is a plain sentence, not an empty table.
test("says when no request reached a provider", async () => {
  gateway.page = { data: [], window: { since: "2026-08-03T00:00:00Z" } };
  mount();

  expect(
    await screen.findByText("No request reached a provider for this account in the window."),
  ).toBeTruthy();
  expect(screen.queryByRole("table")).toBeNull();
});

// A refused read names the scope it needs instead of failing generically.
test("names the scope a refused read needs", async () => {
  gateway.error = new ApiError(403, "forbidden", null);
  mount();

  expect(
    await screen.findByText("Reading provider spend needs an admin-scoped key."),
  ).toBeTruthy();
});
