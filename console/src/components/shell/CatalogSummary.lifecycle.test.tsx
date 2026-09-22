// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { CatalogIndicator } from "@/components/shell/CatalogPanel";
import {
  RETRY_AFTER_DEFAULT_SECONDS,
  RETRY_AFTER_MAX_SECONDS,
  RETRY_AFTER_MIN_SECONDS,
  refusedRead,
  retryAfterMilliseconds,
  statusCadence,
  summaryCadence,
} from "@/components/shell/CatalogSummary";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ApiError } from "@/lib/api";
import { OPERATION_INTERVAL, STATUS_INTERVAL, SUMMARY_INTERVAL } from "@/lib/queries";

// The catalog read is a background read, so its cost falls on a reader who
// gains nothing from it. Four rules keep that cost honest: the shell holds one
// summary query however many surfaces read it, a hidden page draws no request,
// a gateway that asked for a wait gets it, and a refused credential stops the
// read until the session behind it changes. The operator status is stricter
// still: it polls only while a reader looks at the panel that shows it.

const gateway = vi.hoisted(() => ({
  summaryCalls: 0,
  statusCalls: 0,
  refuse: null as { status: number; retryAfter?: number } | null,
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    catalogSummary: async () => {
      gateway.summaryCalls += 1;
      if (gateway.refuse) {
        throw new actual.ApiError(
          gateway.refuse.status,
          "refused",
          null,
          gateway.refuse.retryAfter,
        );
      }
      return {
        generation_id: "01J9ABCDEFGHJKMNPQRSTVWXYZ",
        age_seconds: 60,
        usable: true,
        freshness: "current",
      };
    },
    catalogStatus: async () => {
      gateway.statusCalls += 1;
      return { runtime: { usable: true } };
    },
    catalogChanges: async () => ({ available: false }),
  };
});

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

function setVisibility(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { value: state, configurable: true });
  fireEvent(window, new Event("visibilitychange"));
}

beforeEach(() => {
  gateway.summaryCalls = 0;
  gateway.statusCalls = 0;
  gateway.refuse = null;
  vi.stubGlobal("localStorage", memoryStorage());
  setVisibility("visible");
  vi.useFakeTimers({ shouldAdvanceTime: true });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function mount(copies = 1) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <TooltipProvider>
        {Array.from({ length: copies }, (_, index) => (
          <CatalogIndicator key={index} />
        ))}
      </TooltipProvider>
    </QueryClientProvider>,
  );
}

test("the shell holds one summary query however many surfaces read it", async () => {
  mount(3);

  await waitFor(() => expect(gateway.summaryCalls).toBe(1));
  expect(screen.getAllByTestId("catalog-chip").length).toBe(3);
});

test("a hidden page draws no request and a visible one resumes the cadence", async () => {
  mount();
  await waitFor(() => expect(gateway.summaryCalls).toBe(1));

  setVisibility("hidden");
  await vi.advanceTimersByTimeAsync(5 * SUMMARY_INTERVAL);
  expect(gateway.summaryCalls).toBe(1);

  // A page the reader can see again is worth a fresh read.
  setVisibility("visible");
  await vi.advanceTimersByTimeAsync(SUMMARY_INTERVAL);
  await waitFor(() => expect(gateway.summaryCalls).toBeGreaterThan(1));
});

test("after a 503 the shell waits the interval the gateway asked for", async () => {
  gateway.refuse = { status: 503, retryAfter: 90 };
  mount();
  await waitFor(() => expect(gateway.summaryCalls).toBe(1));

  // The normal cadence would have asked again by now. The gateway asked for
  // 90 seconds, so nothing leaves this browser yet.
  await vi.advanceTimersByTimeAsync(60_000);
  expect(gateway.summaryCalls).toBe(1);

  await vi.advanceTimersByTimeAsync(31_000);
  await waitFor(() => expect(gateway.summaryCalls).toBe(2));
});

test("after a 401 the shell stops until the session changes", async () => {
  gateway.refuse = { status: 401 };
  mount();
  await waitFor(() => expect(gateway.summaryCalls).toBe(1));

  await vi.advanceTimersByTimeAsync(10 * SUMMARY_INTERVAL);
  fireEvent(window, new Event("focus"));
  fireEvent(window, new Event("online"));
  await vi.advanceTimersByTimeAsync(1_000);
  expect(gateway.summaryCalls).toBe(1);
  // A refused summary never reaches for the operator status either.
  expect(gateway.statusCalls).toBe(0);

  const { setApiKey } = await import("@/lib/api");
  gateway.refuse = null;
  setApiKey("a-new-key");

  await waitFor(() => expect(gateway.summaryCalls).toBe(2));
});

test("the operator status polls only while the panel is open", async () => {
  mount();
  await waitFor(() => expect(gateway.statusCalls).toBe(1));

  // A closed panel shows nothing, so it reads nothing.
  await vi.advanceTimersByTimeAsync(10 * STATUS_INTERVAL);
  expect(gateway.statusCalls).toBe(1);

  fireEvent.click(screen.getByTestId("catalog-chip"));
  await screen.findByTestId("catalog-panel");
  await vi.advanceTimersByTimeAsync(STATUS_INTERVAL);
  await waitFor(() => expect(gateway.statusCalls).toBe(2));
});

test("the cadence rules are stated once and read the same everywhere", () => {
  expect(summaryCadence(null)).toBe(SUMMARY_INTERVAL);
  expect(summaryCadence(new ApiError(401, "no", null))).toBe(false);
  expect(summaryCadence(new ApiError(403, "no", null))).toBe(false);
  expect(summaryCadence(new ApiError(503, "no", null))).toBe(RETRY_AFTER_DEFAULT_SECONDS * 1000);
  expect(summaryCadence(new ApiError(503, "no", null, 90))).toBe(90_000);
  // A gateway that asks for an absurd wait gets a bounded one.
  expect(retryAfterMilliseconds(new ApiError(503, "no", null, 1))).toBe(
    RETRY_AFTER_MIN_SECONDS * 1000,
  );
  expect(retryAfterMilliseconds(new ApiError(503, "no", null, 86_400))).toBe(
    RETRY_AFTER_MAX_SECONDS * 1000,
  );
  expect(retryAfterMilliseconds(new ApiError(500, "no", null))).toBeUndefined();

  expect(refusedRead(new ApiError(500, "no", null))).toBe(false);
  expect(statusCadence(false, false, null)).toBe(false);
  expect(statusCadence(true, false, null)).toBe(STATUS_INTERVAL);
  expect(statusCadence(true, true, null)).toBe(OPERATION_INTERVAL);
  expect(statusCadence(true, true, new ApiError(403, "no", null))).toBe(false);
});
