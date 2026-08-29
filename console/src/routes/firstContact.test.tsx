// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { openSession } from "@/lib/api";
import { routeTree } from "@/routeTree.gen";

// Node exposes an unavailable `localStorage` global that shadows the jsdom
// one, so the browser store the client reads is stubbed here, the same way
// src/lib/api.test.ts does. Writes are recorded because one of the properties
// this file holds is that a write never happens.
let writes: string[] = [];

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
    setItem: (key: string, value: string) => {
      writes.push(key);
      entries.set(key, value);
    },
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

// interceptNavigation catches the full page load the page performs once a
// credential is in hand. jsdom will not let `location.assign` be redefined, so
// the whole object is stubbed; the router reads a memory history and never
// touches it. `overrides` is how the trust readout gets a different address to
// report than the one jsdom serves from.
function interceptNavigation(overrides: Partial<Location> = {}) {
  const assign = vi.fn();
  vi.stubGlobal("location", { ...window.location, ...overrides, assign });
  return assign;
}

let fetches: string[] = [];
let calls: RequestInit[] = [];

beforeEach(() => {
  fetches = [];
  calls = [];
  writes = [];
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
  // media queries. Without this the credentialled cases pass on an error
  // boundary instead of on a rendered console, which would prove nothing.
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({
      matches: false,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  );
});

afterEach(async () => {
  cleanup();
  // The rejection flag is module state, so a test that makes the gateway refuse
  // a credential would leave every later test running against a console that
  // believes it has already been thrown out. `openSession` is the public way to
  // clear it; nothing here depends on the response beyond its success.
  vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })));
  await openSession("reset");
  vi.unstubAllGlobals();
});

// This is the acceptance CSG5 exists for. Before it, a credentialless browser
// got the shell: navigation to pages it could not read, and every panel behind
// it firing a request the gateway would refuse. The reader learned they were
// locked out from a wall of failures rather than from a page that told them.
//
// One request is allowed, and it is the page's own: the identity-provider
// list, which decides whether the page offers the operator-configured
// identity providers. It carries no credential and the gateway answers it to
// anyone, so it does not reopen what this test closed — the property held
// here is that nothing behind the locked shell fires.
test("a browser with no credential meets the first-contact page and asks only for the provider list", async () => {
  await open("/models");

  expect(await screen.findByRole("heading", { name: /open this console/i })).toBeTruthy();
  expect(fetches).toEqual(["/console/identity/providers"]);
});

// The redirect carries where the reader was going, so a deep link survives
// presenting a credential instead of dumping them on the overview.
test("the first-contact page remembers the route that was asked for", async () => {
  const router = await open("/providers/groq");

  await screen.findByRole("heading", { name: /open this console/i });
  await waitFor(() => expect(router.state.location.pathname).toBe("/auth"));
  expect(router.state.location.search).toMatchObject({ next: "/providers/groq" });
});

// The other half: a credential must not send a reader to a page telling them
// how to get one. `hasCredential` prefers the session marker cookie, which is
// what /launch and /console/session both set.
test("a browser holding a session gets the console, not the first-contact page", async () => {
  document.cookie = "starport_session_present=1; path=/";

  const router = await open("/models");

  await waitFor(() => expect(router.state.location.pathname).toBe("/models"));
  // The shell itself, not merely the absence of the first-contact page: this is
  // the route that must keep working exactly as it did.
  expect(await screen.findByRole("navigation", { name: "Console" })).toBeTruthy();
  expect(screen.queryByRole("heading", { name: /open this console/i })).toBeNull();
});

// A reader who already has a session and lands on /auth — from a bookmark, or
// from a second tab — is sent on rather than shown a form for a credential they
// already hold.
test("the first-contact page bounces a reader who already has a session", async () => {
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

  await waitFor(() => expect(fetches).toContain("/console/session"));
  // The provider-list read may land before or after the paste; the properties
  // held here belong to the session request alone.
  const sent = calls[fetches.indexOf("/console/session")] ?? {};
  expect(sent.method).toBe("POST");
  // Trimmed: an operator pasting from a terminal brings whitespace with it.
  expect(sent.body).toBe(JSON.stringify({ token: "starport_local_secret" }));
  expect(sent.headers).not.toHaveProperty("Authorization");
  await waitFor(() => expect(assign).toHaveBeenCalledWith("/usage"));
});

// The two fields on this page hold unlike credentials, and the difference shows
// up in where each one ends up. The local admin token is the operator of the
// machine: it is exchanged for a cookie the browser attaches and this code
// cannot read, and it must not be left in a store that outlives the tab. A
// gateway API key is a caller's own credential and has nowhere else to live, so
// it is kept.
//
// Asserting both halves in one test is deliberate. The write is what makes the
// no-write assertion meaningful: a storage stub that recorded nothing at all
// would pass the first half for the wrong reason.
test("the token never reaches browser storage, and the gateway API key does", async () => {
  interceptNavigation();
  vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })));

  await open("/auth");
  fireEvent.change(await screen.findByLabelText(/local admin token/i), {
    target: { value: "starport_local_secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: /open console/i }));

  await waitFor(() => expect(writes).toEqual([]));
  expect(window.sessionStorage.length).toBe(0);

  fireEvent.change(screen.getByLabelText(/gateway api key/i), {
    target: { value: "STARPORT_caller" },
  });
  fireEvent.click(screen.getByRole("button", { name: /use key/i }));

  await waitFor(() => expect(writes).toEqual(["starport.apiKey"]));
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

// The trust readout is rendered from the address the page was actually served
// on. The value of saying "Local-only" at all is that it is not always said.
test("the trust readout names the address this page was served on", async () => {
  interceptNavigation({ hostname: "10.0.0.5", protocol: "http:" });

  await open("/auth");

  expect(await screen.findByText("Network · 10.0.0.5 · not encrypted")).toBeTruthy();
  expect(screen.queryByText(/Local-only/)).toBeNull();
});

// A credential goes stale mid-visit — the session expires, or `starport auth
// rotate` ends it — and the route guard cannot catch it: the guard ran before
// the request that learned the news and does not run again until the reader
// navigates. Without this the console sits on a page of permission errors with
// no way back.
test("a session refused mid-visit returns the reader to the first-contact page", async () => {
  document.cookie = "starport_session_present=1; path=/";
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ message: "session expired" }), {
          status: 401,
          headers: { "Content-Type": "application/json" },
        }),
    ),
  );

  const router = await open("/models");

  await waitFor(() => expect(router.state.location.pathname).toBe("/auth"));
  expect(await screen.findByRole("heading", { name: /open this console/i })).toBeTruthy();
  // The page says why, rather than presenting a bare form to somebody who
  // believed they were already in.
  expect(await screen.findByRole("status")).toHaveProperty(
    "textContent",
    expect.stringContaining("did not accept"),
  );
});
