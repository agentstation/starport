import { afterEach, expect, test, vi } from "vitest";

import { ApiError } from "./api";
import * as api from "./api";
import { queries, retryPolicy } from "./queries";

afterEach(() => vi.restoreAllMocks());

// Every factory, called with placeholder arguments, so the test reads the key
// each one produces without knowing its arity.
function everyKey(): Array<readonly unknown[]> {
  const factories = Object.values(queries) as Array<
    (...args: never[]) => { queryKey: readonly unknown[] }
  >;
  return factories.map((factory) =>
    factory(...(["sample", "sample"] as never[])).queryKey,
  );
}

// An invalidation by prefix must reach one resource. Two factories that share
// a first segment would refetch each other on every write.
test("every factory key starts with a prefix no other factory uses", () => {
  const prefixes = everyKey().map((key) => key[0]);
  expect(prefixes.length).toBe(Object.keys(queries).length);
  expect(new Set(prefixes).size).toBe(prefixes.length);
  for (const prefix of prefixes) expect(typeof prefix).toBe("string");
});

// The preset list and a preset's history are two resources: a rollback
// refreshes one history, and a create refreshes the list alone.
test("preset history keys do not sit under the preset list key", () => {
  expect(queries.presetHistory("fast").queryKey[0]).not.toBe(
    queries.presets().queryKey[0],
  );
});

// The usage window is part of the activity key, so a refetch keeps the bounds
// it started with and a new window is a new record.
test("the activity key carries the window bound", () => {
  const base = { scope: "admin" as const, filters: { model: "m" } };
  const first = queries.activity({ ...base, sinceISO: "2026-09-01T00:00:00Z" });
  const second = queries.activity({ ...base, sinceISO: "2026-09-01T01:00:00Z" });
  expect(first.queryKey).not.toEqual(second.queryKey);
  expect(JSON.stringify(first.queryKey)).toContain("2026-09-01T00:00:00Z");
});

// A gateway answer is final. Only a request that never reached the gateway
// earns one more try.
test("retryPolicy retries a network failure once and an API error never", () => {
  expect(retryPolicy(0, new TypeError("Failed to fetch"))).toBe(true);
  expect(retryPolicy(1, new TypeError("Failed to fetch"))).toBe(false);
  expect(retryPolicy(0, new ApiError(500, "provider down", null))).toBe(false);
  expect(retryPolicy(0, new ApiError(403, "forbidden", null))).toBe(false);
});

// The scope probe asks the admin listing once. A refusal is the answer
// "own", not a reason to probe the own listing too.
test("the scope probe makes one request", async () => {
  const admin = vi
    .spyOn(api, "listAdminActivity")
    .mockRejectedValue(new ApiError(403, "forbidden", null));
  const own = vi.spyOn(api, "listActivity").mockResolvedValue({ data: [] });

  const scope = await queries.activityScope().queryFn!({} as never);

  expect(scope).toBe("own");
  expect(admin).toHaveBeenCalledTimes(1);
  expect(own).not.toHaveBeenCalled();
});

test("the scope probe reads an admin listing as admin and a 503 as unconfigured", async () => {
  const admin = vi.spyOn(api, "listAdminActivity").mockResolvedValue({ data: [] });
  expect(await queries.activityScope().queryFn!({} as never)).toBe("admin");

  admin.mockRejectedValue(new ApiError(503, "no store", null));
  expect(await queries.activityScope().queryFn!({} as never)).toBe("unconfigured");
});
