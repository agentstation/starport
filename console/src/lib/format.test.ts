import { describe, expect, test } from "vitest";

import {
  formatContext,
  formatCount,
  formatNanoUSD,
  formatPricePair,
  formatPricePerK,
  formatPricePerM,
  formatRelativeTime,
  formatUSD,
  formatUnitPrice,
  utcTooltip,
} from "./format";

const DASH = "—";

describe("formatCount", () => {
  test.each<[number | null | undefined, string]>([
    [undefined, DASH],
    [null, DASH],
    [0, "0"],
    [999, "999"],
    [9_999, "9,999"],
    [10_000, "10.0k"],
    [12_400, "12.4k"],
    [999_950, "1.0M"],
    [1_200_000, "1.2M"],
    [999_950_000, "1.0B"],
    [2_500_000_000, "2.5B"],
  ])("renders %s as %s", (input, expected) => {
    expect(formatCount(input)).toBe(expected);
  });
});

describe("formatContext", () => {
  test.each<[number | null | undefined, string]>([
    [undefined, DASH],
    [0, DASH],
    [512, "512"],
    [4_096, "4k"],
    [32_768, "33k"],
    [128_000, "128k"],
    [999_600, "1M"],
    [1_048_576, "1M"],
    [1_500_000, "1.5M"],
    [2_000_000_000, "2B"],
  ])("renders %s as %s", (input, expected) => {
    expect(formatContext(input)).toBe(expected);
  });
});

describe("formatUSD", () => {
  test.each<[number | undefined, string | null]>([
    [undefined, null],
    [Number.NaN, null],
    [0, "$0"],
    [15, "$15"],
    [150, "$150"],
    [1.2, "$1.20"],
    [1.2345678, "$1.23"],
    [0.8, "$0.80"],
    [0.125, "$0.125"],
    [0.05, "$0.05"],
    [0.0012, "$0.0012"],
    [0.0001851, "$0.000185"],
    [1e-7, "$0.0000001"],
    [1e-9, "$0.000000001"],
    [-2.5, "-$2.50"],
  ])("renders %s as %s", (input, expected) => {
    expect(formatUSD(input)).toBe(expected);
  });
});

describe("formatNanoUSD", () => {
  test.each<[number | undefined, string]>([
    [undefined, DASH],
    [0, "$0"],
    [1, "$0.000000001"],
    [345_000, "$0.000345"],
    [50_000_000, "$0.05"],
    [1_200_000_000, "$1.20"],
    [1_234_567_890, "$1.23"],
  ])("renders %s nano-USD as %s", (input, expected) => {
    expect(formatNanoUSD(input)).toBe(expected);
  });
});

describe("formatPricePerM", () => {
  test.each<[string | undefined, string | null]>([
    [undefined, null],
    ["", null],
    ["free", null],
    ["0", "$0"],
    ["2.2e-7", "$0.22"],
    ["0.0000008", "$0.80"],
    ["1.5e-5", "$15"],
    ["0.000000125", "$0.125"],
    ["1e-13", "$0.0000001"],
  ])("renders per-token %s as %s per million", (input, expected) => {
    expect(formatPricePerM(input)).toBe(expected);
  });
});

describe("formatPricePerK", () => {
  test.each<[string | undefined, string | null]>([
    [undefined, null],
    ["0", "$0"],
    ["2.58e-05", "$0.0258"],
    ["0.001", "$1"],
    ["1e-9", "$0.000001"],
  ])("renders per-unit %s as %s per thousand", (input, expected) => {
    expect(formatPricePerK(input)).toBe(expected);
  });
});

describe("formatUnitPrice", () => {
  test.each<[string | undefined, string | null]>([
    [undefined, null],
    ["0.001", "$0.001"],
    ["2", "$2"],
    ["1e-8", "$0.00000001"],
  ])("renders %s as %s", (input, expected) => {
    expect(formatUnitPrice(input)).toBe(expected);
  });
});

describe("formatPricePair", () => {
  test.each<[string | undefined, string | undefined, string | null]>([
    [undefined, undefined, null],
    ["2.2e-7", "8.8e-7", "$0.22 / M in · $0.88 / M out"],
    ["2.2e-7", undefined, "$0.22 / M in · — / M out"],
    [undefined, "1e-6", "— / M in · $1 / M out"],
    ["0", "0", "$0 / M in · $0 / M out"],
  ])("renders %s and %s as %s", (prompt, completion, expected) => {
    expect(formatPricePair(prompt, completion)).toBe(expected);
  });
});

describe("utcTooltip", () => {
  test.each<[string | null | undefined, string | undefined]>([
    [undefined, undefined],
    [null, undefined],
    ["", undefined],
    ["not-a-date", undefined],
    ["0001-01-01T00:00:00Z", undefined],
    ["2026-09-01T12:00:00Z", "2026-09-01T12:00:00.000Z"],
    ["2026-09-01T12:00:00+02:00", "2026-09-01T10:00:00.000Z"],
  ])("renders %s as %s", (input, expected) => {
    expect(utcTooltip(input)).toBe(expected);
  });
});

// A double formatted by precision falls into exponent notation below 1e-6.
// Every formatter fixes decimals instead, so no magnitude a catalog or a
// cost meter can produce reads as "1.0e-9".
test("no formatter emits exponent notation across twenty-five magnitudes", () => {
  for (let power = -12; power <= 12; power += 1) {
    const value = 1.5 * 10 ** power;
    const outputs = [
      formatUSD(value),
      formatNanoUSD(value),
      formatPricePerM(String(value)),
      formatPricePerK(String(value)),
      formatUnitPrice(String(value)),
      formatPricePair(String(value), String(value)),
      formatCount(value),
      formatContext(value),
    ];
    for (const output of outputs) {
      expect(String(output)).not.toMatch(/e[-+]/i);
    }
  }
});

// A zero-value Go time serializes as "0001-01-01T00:00:00Z". It parses to a
// finite pre-epoch instant, so without a guard it falls through the relative
// buckets and renders as a first-century calendar date ("12/31/1"). An absent
// stamp must render as absent.
test("formatRelativeTime renders an absent stamp as a dash", () => {
  expect(formatRelativeTime(undefined)).toBe(DASH);
  expect(formatRelativeTime("")).toBe(DASH);
  expect(formatRelativeTime("not-a-date")).toBe(DASH);
  expect(formatRelativeTime("0001-01-01T00:00:00Z")).toBe(DASH);
});

test("formatRelativeTime renders a real stamp relatively", () => {
  expect(formatRelativeTime(new Date().toISOString())).toBe("just now");
  const fiveMinutes = new Date(Date.now() - 5 * 60 * 1000).toISOString();
  expect(formatRelativeTime(fiveMinutes)).toBe("5m ago");
});
