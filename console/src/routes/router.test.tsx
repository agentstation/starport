// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { openSession } from "@/lib/api";
import { createConsoleRouter } from "@/router";

// The one model the stubbed gateway knows. Its id holds a slash, which is
// the shape every catalog id has and the shape a path segment cannot hold
// unencoded.
const MODEL = {
  id: "anthropic/claude-sonnet-5",
  name: "Claude Sonnet 5",
  context_length: 200_000,
  authors: [],
};

// answers is what the gateway says to each path. An unlisted path answers
// an empty object, which every list read treats as empty.
const answers: Record<string, unknown> = {
  "/api/v1/models": { data: [MODEL] },
  "/api/v1/admin/providers": { providers: [] },
};

function memoryStorage(): Storage {
  const entries = new Map<string, string>();
  return {
    get length() {
      return entries.size;
    },
    clear: () => entries.clear(),
    getItem: (key: string) => entries.get(key) ?? null,
    key: (index: number) => [...entries.keys()][index] ?? null,
    removeItem: (key: string) => void entries.delete(key),
    setItem: (key: string, value: string) => void entries.set(key, value),
  };
}

// open renders the real router at one address, with the session marker a
// launched console holds, so the loaders and the default components under
// test are the ones a browser meets.
async function open(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createConsoleRouter(
    queryClient,
    createMemoryHistory({ initialEntries: [path] }),
  );
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

beforeEach(() => {
  vi.stubGlobal("localStorage", memoryStorage());
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const path = new URL(String(input), "http://localhost").pathname;
      return new Response(JSON.stringify(answers[path] ?? {}), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({
      matches: false,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  );
  document.cookie = "starport_session_present=1; path=/";
});

afterEach(async () => {
  cleanup();
  document.cookie = "starport_session_present=; max-age=0; path=/";
  vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })));
  await openSession("reset");
  vi.unstubAllGlobals();
});

// An unencoded id typed into the address bar spans two segments and matches
// no route. The page names the list the reader wanted and links to it,
// rather than the router's bare default.
test("an address the router cannot match names the missing model and links to the list", async () => {
  await open("/models/unknown/unknown");

  expect(await screen.findByRole("heading", { name: "No model at this address" })).toBeTruthy();
  expect(screen.getByText("unknown/unknown")).toBeTruthy();
  const back = screen
    .getAllByRole("link", { name: "Models" })
    .find((link) => !link.closest("nav"));
  expect(back?.getAttribute("href")).toBe("/models");
});

// The detail route matches, and its loader learns from the catalog that the
// id names nothing. That is the same page, reached through notFound().
test("a model the catalog does not hold renders the not-found page", async () => {
  await open("/models/nope");

  expect(await screen.findByRole("heading", { name: "No model at this address" })).toBeTruthy();
  expect(screen.getByText("nope")).toBeTruthy();
});

// The sidebar matches by prefix, so a reader on a detail page still sees
// which list they are in. The overview matches exactly, or it would light
// up everywhere.
test("the models link stays active on a model detail route", async () => {
  await open("/models/anthropic%2Fclaude-sonnet-5");

  await screen.findByRole("heading", { name: "Claude Sonnet 5" });
  const nav = screen.getByRole("navigation", { name: "Console" });
  expect(within(nav).getByRole("link", { name: "Models" }).getAttribute("aria-current")).toBe(
    "page",
  );
  expect(within(nav).getByRole("link", { name: "Overview" }).getAttribute("aria-current")).toBeNull();
});

// The router encodes a param into one segment and decodes it on the way
// back, so an id with a slash survives a navigation, the address bar, and
// the route that reads it.
test("a model id with a slash round-trips through the address as one segment", async () => {
  const router = await open("/models");
  await screen.findByRole("navigation", { name: "Console" });

  await router.navigate({ to: "/models/$modelId", params: { modelId: MODEL.id } });

  await waitFor(() =>
    expect(router.state.location.pathname).toBe("/models/anthropic%2Fclaude-sonnet-5"),
  );
  expect(await screen.findByRole("heading", { name: "Claude Sonnet 5" })).toBeTruthy();
  expect(screen.getByTitle("Copy model ID").textContent).toContain("anthropic/claude-sonnet-5");
});
