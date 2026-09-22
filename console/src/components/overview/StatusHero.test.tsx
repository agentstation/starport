// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { StatusHero } from "./StatusHero";

// The hero reads readiness alone here; the identity facts need a gateway
// credential the test does not hold.
const gateway = vi.hoisted(() => ({
  ready: async (): Promise<{ status?: string }> => ({ status: "ok" }),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    healthReady: () => gateway.ready(),
    hasCredential: () => false,
  };
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <StatusHero />
    </QueryClientProvider>,
  );
}

test("a ready gateway titles the page with its status and shows a healthy dot", async () => {
  gateway.ready = async () => ({ status: "ok" });
  mount();

  expect(await screen.findByRole("heading", { level: 1, name: "Status: Ready" })).toBeTruthy();
  const badge = screen.getByTestId("gateway-readiness");
  expect(badge.getAttribute("data-readiness")).toBe("ready");
  expect(badge.textContent).toBe("healthy");
});

test("a gateway that is not ready says so without the word Gateway", async () => {
  gateway.ready = async () => ({ status: "degraded" });
  mount();

  expect(await screen.findByRole("heading", { level: 1, name: "Status: Not ready" })).toBeTruthy();
  const badge = screen.getByTestId("gateway-readiness");
  expect(badge.getAttribute("data-readiness")).toBe("not_ready");
  expect(badge.textContent).toBe("unreachable");
});

test("the strip says it is connecting until the first health answer", () => {
  gateway.ready = () => new Promise(() => {});
  mount();

  expect(screen.getByRole("heading", { level: 1, name: "Status: Connecting" })).toBeTruthy();
  expect(screen.getByTestId("gateway-readiness").getAttribute("data-readiness")).toBe("connecting");
});
