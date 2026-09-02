import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import { cleanup, render } from "@testing-library/react";
import { vi } from "vitest";

import { openSession } from "@/lib/api";
import { createConsoleRouter } from "@/router";

// A route test renders the real router at one address, with the session
// marker a launched console holds, against a stubbed gateway. This module
// owns that harness so each test file states only its answers.

// An answer is what the gateway says to one path: a JSON body, or a function
// that builds the response from the request URL when a test needs to hold or
// vary a reply.
export type Answer = unknown | ((url: URL) => Response | Promise<Response>);

export function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

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

// stubGateway installs the browser globals a console needs and answers each
// fetch from the table. An unlisted path answers an empty object, which every
// list read treats as empty. Call it in beforeEach.
export function stubGateway(answers: Record<string, Answer>) {
  vi.stubGlobal("localStorage", memoryStorage());
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      const answer = answers[url.pathname];
      if (typeof answer === "function") {
        return (answer as (url: URL) => Response | Promise<Response>)(url);
      }
      return json(answer ?? {});
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
}

// resetGateway unmounts the page, clears the session marker, and returns the
// credential store to its initial state. Call it in afterEach.
export async function resetGateway() {
  cleanup();
  document.cookie = "starport_session_present=; max-age=0; path=/";
  vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })));
  await openSession("reset");
  vi.unstubAllGlobals();
}

// openConsole renders the router at one address and returns it, so a test can
// read the location or navigate.
export function openConsole(path: string) {
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
