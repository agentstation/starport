// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";

import { CatalogChip, postureOf, verdictOf } from "@/components/shell/CatalogChip";
import type { CatalogAdminRead, CatalogSummaryRead } from "@/components/shell/CatalogSummary";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ApiError, type CatalogAdminStatus, type CatalogSummary } from "@/lib/api";

// The chip is the one catalog indicator of the console, so it must keep the
// status concepts apart. Usability, authorization, freshness, degradation,
// fallback, a source this gateway could not read, and work in flight are
// seven ideas, and a reader who acts on one must never read it out of a glyph
// that also carries another.

afterEach(cleanup);

const summary: CatalogSummary = {
  generation_id: "01J9ABCDEFGHJKMNPQRSTVWXYZ",
  age_seconds: 7200,
  usable: true,
  freshness: "current",
  providers: 12,
  models: 511,
};

function reader(over: Partial<CatalogSummaryRead> = {}): CatalogSummaryRead {
  return {
    summary,
    error: null,
    pending: false,
    refused: false,
    session: "session",
    ...over,
  };
}

function operator(status: CatalogAdminStatus | undefined): CatalogAdminRead {
  return {
    status,
    admin: status !== undefined,
    working: status?.operations?.find((operation) => operation.state === "running"),
  };
}

function draw(read: CatalogSummaryRead, admin: CatalogAdminRead, small = false) {
  render(
    <TooltipProvider>
      <CatalogChip read={read} admin={admin} open={false} onToggle={() => {}} small={small} />
    </TooltipProvider>,
  );
}

test("the healthy chip states freshness, label, and age in three elements", () => {
  draw(reader(), operator(undefined));

  const dot = screen.getByTestId("catalog-freshness-dot");
  expect(dot.getAttribute("data-verdict")).toBe("fresh");
  expect(dot.textContent).toBe("");
  expect(screen.getByTestId("catalog-label").textContent).toBe("Catalog");
  // The generation belongs to the panel's identity section, not to the chip.
  expect(screen.queryByTestId("catalog-generation")).toBeNull();
  expect(screen.getByTestId("catalog-chip").textContent).not.toContain("01J9ABCDEFGHJKMNPQRS");
  expect(screen.getByTestId("catalog-age").textContent).toBe("2h");
  expect(screen.getByTestId("catalog-chip").getAttribute("aria-expanded")).toBe("false");
});

test("a stale grade marks the dot itself rather than borrowing another element", () => {
  draw(reader({ summary: { ...summary, freshness: "critical" } }), operator(undefined));

  const dot = screen.getByTestId("catalog-freshness-dot");
  expect(dot.getAttribute("data-verdict")).toBe("stale");
  expect(dot.textContent).toBe("!");
  expect(screen.getByTestId("catalog-label").textContent).toBe("Catalog");
});

test("usability, authorization, and freshness never share one element", () => {
  draw(reader({ summary: { ...summary, usable: false } }), operator(undefined));
  expect(screen.getByTestId("catalog-unusable-glyph")).toBeTruthy();
  expect(screen.getByTestId("catalog-label").textContent).toBe("No catalog");
  expect(screen.queryByTestId("catalog-freshness-dot")).toBeNull();
  cleanup();

  draw(
    reader({ summary: undefined, error: new ApiError(403, "denied", null), refused: true }),
    operator(undefined),
  );
  expect(screen.getByTestId("catalog-authorization-glyph")).toBeTruthy();
  expect(screen.getByTestId("catalog-label").textContent).toBe("Catalog");
  expect(screen.queryByTestId("catalog-freshness-dot")).toBeNull();
  expect(screen.queryByTestId("catalog-unusable-glyph")).toBeNull();
});

test("an admin render gives degradation, fallback, source health, and work their own elements", () => {
  draw(
    reader(),
    operator({
      runtime: { fallback: true, source_kind: "starmap" },
      source_health: "unavailable",
      upstream_health: "ok",
      snapshot: { degraded: true },
      operations: [{ id: "run-1", state: "running" }],
    }),
  );

  expect(screen.getByTestId("catalog-degraded-pill")).toBeTruthy();
  expect(screen.getByTestId("catalog-fallback-pill")).toBeTruthy();
  expect(screen.getByTestId("catalog-source-down-pill")).toBeTruthy();
  expect(screen.getByTestId("catalog-activity-icon")).toBeTruthy();
  // Work in flight adds an icon; it never replaces the freshness dot.
  expect(screen.getByTestId("catalog-freshness-dot").getAttribute("data-verdict")).toBe("fresh");
});

test("an upstream that reports its own trouble never raises the source pill", () => {
  draw(reader(), operator({ source_health: "ok", upstream_health: "unavailable" }));

  expect(screen.queryByTestId("catalog-source-down-pill")).toBeNull();
});

test("a models:read render holds no admin pill and no activity icon", () => {
  // A reader without the admin scope reads no operator status at all, so the
  // chip has nothing to draw the operator elements from.
  draw(reader(), operator(undefined));

  expect(screen.queryByTestId("catalog-degraded-pill")).toBeNull();
  expect(screen.queryByTestId("catalog-fallback-pill")).toBeNull();
  expect(screen.queryByTestId("catalog-source-down-pill")).toBeNull();
  expect(screen.queryByTestId("catalog-activity-icon")).toBeNull();
});

test("the small-screen control carries label, age, and verdict in its name", () => {
  draw(reader(), operator(undefined), true);

  const chip = screen.getByTestId("catalog-chip");
  const name = chip.getAttribute("aria-label") ?? "";
  expect(name).toContain("The catalog is fresh.");
  expect(name).toContain("It is 2h old.");
  expect(name).not.toContain("01J9ABCDEFGHJKMNPQRS");
  expect(screen.queryByTestId("catalog-label")).toBeNull();
});

test("the server grade decides the verdict and the console grades no age itself", () => {
  expect(verdictOf("current")).toBe("fresh");
  expect(verdictOf("warn")).toBe("stale");
  expect(verdictOf("critical")).toBe("stale");
  expect(verdictOf(undefined)).toBe("unknown");
  expect(postureOf(reader({ error: new ApiError(503, "gone", null) }))).toBe("unusable");
});
