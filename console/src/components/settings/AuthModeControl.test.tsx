// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import type { AuthMode } from "@/lib/api";

import { AuthModeControl } from "./AuthModeControl";

const gateway = vi.hoisted(() => ({
  mode: { mode: "required", source: "default", can_change: true } as AuthMode,
  applied: [] as string[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    readAuthMode: async () => gateway.mode,
    setAuthMode: async (mode: AuthMode["mode"]) => {
      gateway.applied.push(mode);
      gateway.mode = { mode, source: "console", can_change: true };
      return gateway.mode;
    },
  };
});

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

beforeEach(() => {
  gateway.mode = { mode: "required", source: "default", can_change: true };
  gateway.applied = [];
  vi.stubGlobal("localStorage", memoryStorage());
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <AuthModeControl />
    </QueryClientProvider>,
  );
}

function choice(label: string): HTMLElement {
  return screen.getByRole("radio", { name: label });
}

// Opening a gateway is the change a reader cannot undo by reloading, so the
// switch has to state the consequence and wait, not commit on the click.
test("turning authentication off confirms before it commits", async () => {
  mount();
  await waitFor(() => expect(choice("Open")).toBeTruthy());

  fireEvent.click(choice("Open"));

  expect(gateway.applied).toEqual([]);
  const dialog = screen.getByRole("dialog");
  expect(dialog.textContent).toContain("without a key");

  fireEvent.click(screen.getByRole("button", { name: "Turn authentication off" }));

  await waitFor(() => expect(gateway.applied).toEqual(["disabled"]));
  await waitFor(() => expect(choice("Open").getAttribute("aria-checked")).toBe("true"));
});

// Cancelling has to leave the gateway exactly as it was. A dialog that closed
// and applied anyway would be worse than no dialog.
test("cancelling leaves the mode alone", async () => {
  mount();
  await waitFor(() => expect(choice("Open")).toBeTruthy());

  fireEvent.click(choice("Open"));
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

  expect(gateway.applied).toEqual([]);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(choice("Require a key").getAttribute("aria-checked")).toBe("true");
});

// Locking the gateway from a browser that holds no key locks this console out
// of it. The reader has to learn that before the switch, not from the next
// screen full of 401s.
test("requiring a key names the lockout when this browser has none", async () => {
  gateway.mode = { mode: "disabled", source: "console", can_change: true };
  mount();
  await waitFor(() => expect(choice("Require a key")).toBeTruthy());

  fireEvent.click(choice("Require a key"));

  const dialog = screen.getByRole("dialog");
  expect(dialog.textContent).toContain("this browser has none saved");
  expect(dialog.textContent).toContain("locks");
});

test("a stored key changes what the confirmation promises", async () => {
  localStorage.setItem("starport.apiKey", "STARPORT_test");
  gateway.mode = { mode: "disabled", source: "console", can_change: true };
  mount();
  await waitFor(() => expect(choice("Require a key")).toBeTruthy());

  fireEvent.click(choice("Require a key"));

  const dialog = screen.getByRole("dialog");
  expect(dialog.textContent).toContain("the console keeps working");
});

// The gateway answers can_change per request, and the console has to render
// the refusal rather than a control that would fail. Showing the gateway's own
// reason is what keeps the two from disagreeing.
test("a mode fixed by the operator renders as locked with the reason", async () => {
  gateway.mode = {
    mode: "required",
    source: "flag",
    can_change: false,
    reason: "the authentication mode is fixed by a command line flag for this process",
  };
  mount();
  await waitFor(() => expect(choice("Open")).toBeTruthy());

  expect((choice("Open") as HTMLButtonElement).disabled).toBe(true);
  expect(screen.getByText(/fixed by a command line flag/)).toBeTruthy();

  fireEvent.click(choice("Open"));

  expect(screen.queryByRole("dialog")).toBeNull();
  expect(gateway.applied).toEqual([]);
});
