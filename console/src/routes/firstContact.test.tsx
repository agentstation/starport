// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { routeTree } from "@/routeTree.gen";

import { destination } from "./auth";

// Node exposes an unavailable `localStorage` global that shadows the jsdom
// one, so the browser store the client reads is stubbed here, the same way
// src/lib/api.test.ts does.
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

// open renders the real route tree at one address, the way a browser arriving
// there would. The tree is the generated one on purpose: the acceptance this
// holds is about every route, and a hand-built tree would only hold it for the
// routes the test remembered to add.
async function open(path: string) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  render(
    <QueryClientProvider client={new QueryClient()}>
      {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
      <RouterProvider router={router as any} />
    </QueryClientProvider>,
  );
  return router;
}

// interceptNavigation catches the full page load the sign-in page performs once
// a session opens. jsdom will not let `location.assign` be redefined, so the
// whole object is stubbed; the router reads a memory history and never touches
// it.
function interceptNavigation() {
  const assign = vi.fn();
  vi.stubGlobal("location", { ...window.location, assign });
  return assign;
}

let fetches: string[] = [];
let calls: RequestInit[] = [];

beforeEach(() => {
  fetches = [];
  calls = [];
  vi.stubGlobal("localStorage", memoryStorage());
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      fetches.push(String(input));
      return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } });
    }),
  );
  document.cookie = "starport_session_present=; max-age=0; path=/";
  // The shell reads the colour-scheme preference on mount, and jsdom has no
  // media queries. Without this the signed-in cases pass on an error boundary
  // instead of on a rendered console, which would prove nothing.
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({
      matches: false,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// This is the acceptance CSG5 exists for. Before it, a sessionless browser got
// the shell: navigation to pages it could not read, and every panel behind it
// firing a request the gateway would refuse. The reader learned they were
// signed out from a wall of failures rather than from a page that told them.
test("a browser with no credential meets the sign-in page and asks the gateway for nothing", async () => {
  await open("/models");

  expect(await screen.findByRole("heading", { name: /sign in to this gateway/i })).toBeTruthy();
  expect(fetches).toEqual([]);
});

// The redirect carries where the reader was going, so a deep link survives
// signing in instead of dumping them on the overview.
test("the sign-in page remembers the route that was asked for", async () => {
  const router = await open("/providers/groq");

  await screen.findByRole("heading", { name: /sign in to this gateway/i });
  await waitFor(() => expect(router.state.location.pathname).toBe("/auth"));
  expect(router.state.location.search).toMatchObject({ next: "/providers/groq" });
});

// The other half: a credential must not send a reader to a page telling them
// how to get one. `hasCredential` prefers the session marker cookie, which is
// what /launch and /console/session both set.
test("a browser holding a session gets the console, not the sign-in page", async () => {
  document.cookie = "starport_session_present=1; path=/";

  const router = await open("/models");

  await waitFor(() => expect(router.state.location.pathname).toBe("/models"));
  // The shell itself, not merely the absence of the sign-in page: this is the
  // route that must keep working exactly as it did.
  expect(await screen.findByRole("navigation", { name: "Console" })).toBeTruthy();
  expect(screen.queryByRole("heading", { name: /sign in to this gateway/i })).toBeNull();
});

// A reader who is already signed in and lands on /auth — from a bookmark, or
// from a second tab — is sent on rather than shown a form for a session they
// already hold.
test("the sign-in page bounces a reader who already has a session", async () => {
  document.cookie = "starport_session_present=1; path=/";

  const router = await open("/auth?next=/usage");

  await waitFor(() => expect(router.state.location.pathname).toBe("/usage"));
});

// The paste itself. Two properties matter and neither is obvious from reading
// the component: the token goes in the body rather than the URL, and the
// request carries no Authorization header. A console that sent a stored gateway
// API key alongside would be presenting one credential to ask for another.
test("pasting a token posts it to the session route and sends no bearer key", async () => {
  const assign = interceptNavigation();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      fetches.push(String(input));
      calls.push(init ?? {});
      return new Response(null, { status: 204 });
    }),
  );

  await open("/auth?next=/usage");
  const field = await screen.findByLabelText(/local admin token/i);
  fireEvent.change(field, { target: { value: "  starport_local_secret  " } });
  fireEvent.click(screen.getByRole("button", { name: /open console/i }));

  await waitFor(() => expect(fetches).toEqual(["/console/session"]));
  const sent = calls[0] ?? {};
  expect(sent.method).toBe("POST");
  // Trimmed: an operator pasting from a terminal brings whitespace with it.
  expect(sent.body).toBe(JSON.stringify({ token: "starport_local_secret" }));
  expect(sent.headers).not.toHaveProperty("Authorization");
  await waitFor(() => expect(assign).toHaveBeenCalledWith("/usage"));
});

// A refusal has to say what the gateway said. The route answers a pasted
// gateway API key with a message naming which credential the field wants, and
// swallowing it here would put the reader back to guessing.
test("a refused paste shows the gateway's own message and stays put", async () => {
  const assign = interceptNavigation();
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ message: "That is a gateway API key, which…" }), {
          status: 401,
          headers: { "Content-Type": "application/json" },
        }),
    ),
  );

  await open("/auth");
  fireEvent.change(await screen.findByLabelText(/local admin token/i), {
    target: { value: "STARPORT_wrong" },
  });
  fireEvent.click(screen.getByRole("button", { name: /open console/i }));

  expect(await screen.findByRole("alert")).toHaveProperty(
    "textContent",
    "That is a gateway API key, which…",
  );
  expect(assign).not.toHaveBeenCalled();
});

// `next` arrives in a URL, so it is whatever the last link said. A console that
// honoured a full URL would be a redirector: follow a link, sign in to your own
// gateway, and land on somebody else's page having just proved you trust this
// one. Every rejected form falls back to the overview rather than failing.
test("only a path on this gateway is honoured as a destination", () => {
  expect(destination("/usage")).toBe("/usage");
  expect(destination("/models?q=llama#top")).toBe("/models?q=llama#top");

  expect(destination(undefined)).toBe("/");
  expect(destination("https://example.com/steal")).toBe("/");
  expect(destination("//example.com/steal")).toBe("/");
  expect(destination("javascript:alert(1)")).toBe("/");
  // Sending a signed-in reader back to the sign-in page is a loop, not a
  // destination.
  expect(destination("/auth?next=/auth")).toBe("/");
});
