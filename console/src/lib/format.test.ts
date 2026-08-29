import { expect, test } from "vitest";

import { formatRelativeTime } from "./format";

// A zero-value Go time serializes as "0001-01-01T00:00:00Z". It parses to a
// finite pre-epoch instant, so without a guard it falls through the relative
// buckets and renders as a first-century calendar date ("12/31/1"). An absent
// stamp must render as absent.
test("formatRelativeTime renders an absent stamp as a dash", () => {
  expect(formatRelativeTime(undefined)).toBe("—");
  expect(formatRelativeTime("")).toBe("—");
  expect(formatRelativeTime("not-a-date")).toBe("—");
  expect(formatRelativeTime("0001-01-01T00:00:00Z")).toBe("—");
});

test("formatRelativeTime renders a real stamp relatively", () => {
  expect(formatRelativeTime(new Date().toISOString())).toBe("just now");
  const fiveMinutes = new Date(Date.now() - 5 * 60 * 1000).toISOString();
  expect(formatRelativeTime(fiveMinutes)).toBe("5m ago");
});
