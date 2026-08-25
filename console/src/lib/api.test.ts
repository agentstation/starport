import { afterEach, beforeEach, expect, test, vi } from "vitest";

import {
  accessMessage,
  ApiError,
  isCredentialRejected,
  request,
  setApiKey,
} from "./api";

function respond(status: number): Response {
  return new Response(status === 204 ? null : JSON.stringify({}), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// Node exposes an unavailable `localStorage` global that shadows the jsdom
// one, so the browser store the client reads is stubbed here.
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
  vi.stubGlobal("localStorage", memoryStorage());
  setApiKey("starport-test-key");
});

afterEach(() => {
  setApiKey("");
  vi.unstubAllGlobals();
});

// The gateway answers an unknown key with 401 and an insufficient scope with
// 403. Reporting the first as the second sends the reader hunting for a
// permission they already hold, which is what a stale development key did.
test("accessMessage names the stored key for 401 and the scope for 403", () => {
  const unauthorized = accessMessage(new ApiError(401, "no", null), "admin");
  const forbidden = accessMessage(new ApiError(403, "no", null), "admin");

  expect(unauthorized).toMatch(/not accepted/i);
  expect(unauthorized).not.toMatch(/scope/i);
  expect(forbidden).toMatch(/admin scope/);
  expect(forbidden).not.toMatch(/not accepted/i);
});

test("a 401 marks the stored key rejected", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => respond(401)));

  await expect(request("/api/v1/models")).rejects.toBeInstanceOf(ApiError);
  expect(isCredentialRejected()).toBe(true);
});

// A 403 proves the key authenticated, so it must not send the console back to
// the connect prompt; the reader would replace a key that works.
test("a 403 leaves the stored key usable", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => respond(403)));

  await expect(request("/api/v1/admin/info")).rejects.toBeInstanceOf(ApiError);
  expect(isCredentialRejected()).toBe(false);
});

test("a success clears an earlier rejection", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => respond(401)));
  await expect(request("/api/v1/models")).rejects.toBeInstanceOf(ApiError);
  expect(isCredentialRejected()).toBe(true);

  vi.stubGlobal("fetch", vi.fn(async () => respond(200)));
  await request("/api/v1/models");
  expect(isCredentialRejected()).toBe(false);
});

test("storing a new key clears the rejection", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => respond(401)));
  await expect(request("/api/v1/models")).rejects.toBeInstanceOf(ApiError);

  setApiKey("starport-replacement-key");
  expect(isCredentialRejected()).toBe(false);
});

// Without a stored key a 401 says nothing about a key, so it must not set the
// rejected state that the reconnect prompt reads.
test("a 401 without a stored key does not mark a rejection", async () => {
  setApiKey("");
  vi.stubGlobal("fetch", vi.fn(async () => respond(401)));

  await expect(request("/api/v1/models")).rejects.toBeInstanceOf(ApiError);
  expect(isCredentialRejected()).toBe(false);
});
