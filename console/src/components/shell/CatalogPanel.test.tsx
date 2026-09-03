// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { CatalogPanel, PANEL_WIDTH, hopLabel, observationSummary } from "@/components/shell/CatalogPanel";
import type { CatalogAdminRead, CatalogSummaryRead } from "@/components/shell/CatalogSummary";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { CatalogAdminStatus, CatalogSummary } from "@/lib/api";

// The panel answers what this gateway serves and where it came from. Two
// facts must hold whatever the deployment looks like: the layers figure
// always starts at the embedded baseline that ships in the binary, and the
// hop chain never restates an upstream report as an observation of this
// gateway.

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return { ...actual, catalogChanges: async () => ({ available: false }) };
});

afterEach(cleanup);

const summary: CatalogSummary = {
  generation_id: "01J9ABCDEFGHJKMNPQRSTVWXYZ",
  generated_at: "2026-09-01T00:00:00Z",
  age_seconds: 7200,
  usable: true,
  freshness: "current",
  providers: 12,
  models: 511,
  next_update_at: "2026-09-02T00:00:00Z",
};

const read: CatalogSummaryRead = {
  summary,
  error: null,
  pending: false,
  refused: false,
  session: "session",
};

function draw(status: CatalogAdminStatus | undefined) {
  const admin: CatalogAdminRead = {
    status,
    admin: status !== undefined,
    working: undefined,
  };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <TooltipProvider>
        <CatalogPanel read={read} admin={admin} open onOpenChange={() => {}} />
      </TooltipProvider>
    </QueryClientProvider>,
  );
}

test("the layers figure always draws the embedded baseline, even with no upstream", () => {
  draw({ runtime: { source_kind: "embedded", fallback: false } });

  const baseline = screen.getByTestId("catalog-layer-embedded");
  expect(baseline.textContent).toContain("Embedded baseline");
  expect(screen.getByTestId("catalog-layer-upstream").textContent).toContain("none");
  expect(screen.getByTestId("catalog-layer-observations")).toBeTruthy();
  expect(screen.getByTestId("catalog-layer-effective")).toBeTruthy();
});

test("the layers figure still starts at the baseline when an upstream serves", () => {
  draw({
    runtime: { source_kind: "starmap", fallback: true },
    provenance: {
      effective: { generation_id: "01J9ZZZ" },
      upstream: { source_identity: "starmap.example", source_kind: "starmap" },
    },
    snapshot: {
      source_observations: [
        { source: "a", status: "succeeded" },
        { source: "b", status: "succeeded" },
        { source: "c", status: "failed" },
      ],
    },
  });

  expect(screen.getByTestId("catalog-layer-embedded").textContent).toContain("Embedded baseline");
  const upstream = screen.getByTestId("catalog-layer-upstream");
  expect(upstream.textContent).toContain("starmap.example");
  // A fallback keeps the last upstream generation, so the node says it is
  // retained rather than current.
  expect(upstream.textContent).toContain("retained");
  expect(screen.getByTestId("catalog-layer-observations").textContent).toContain("1 failed");
  expect(screen.getByTestId("catalog-layer-observations").textContent).toContain("2 succeeded");
});

test("the first hop reads direct and every later hop reads upstream-reported", () => {
  draw({
    source_health: "ok",
    upstream_health: "degraded",
    provenance: {
      upstream: {
        source_identity: "hop-1",
        chain: [
          { identity: "hop-1", health: "ok", observed_at: "2026-09-01T00:00:00Z" },
          { identity: "hop-2", health: "degraded", observed_at: "2026-08-31T00:00:00Z" },
        ],
      },
    },
  });

  expect(screen.getByTestId("catalog-hop-count").textContent).toBe("2 hops");
  const first = screen.getByTestId("catalog-hop-0");
  expect(first.getAttribute("data-hop-label")).toBe("direct");
  expect(screen.getByTestId("catalog-hop-0-label").textContent).toBe("Direct");
  expect(screen.getByTestId("catalog-hop-health").textContent).toBe("ok");

  const second = screen.getByTestId("catalog-hop-1");
  expect(second.getAttribute("data-hop-label")).toBe("upstream-reported");
  expect(screen.getByTestId("catalog-hop-1-label").textContent).toBe("Reported by upstream");
  // Only the direct hop carries health this gateway observed.
  expect(screen.getAllByTestId("catalog-hop-health").length).toBe(1);
});

test("the panel names the next update for every reader", () => {
  draw(undefined);

  expect(screen.getByTestId("catalog-next-update").textContent).not.toBe("—");
  expect(screen.getByTestId("catalog-admin-only").textContent).toBe(
    "Source, schedule, and provider detail need an admin session.",
  );
  expect(screen.queryByTestId("catalog-layers")).toBeNull();
});

test("the panel is bounded to the viewport", () => {
  draw(undefined);

  expect(PANEL_WIDTH).toBe("w-[min(480px,100vw)]");
  expect(screen.getByTestId("catalog-panel").getAttribute("class")).toContain(PANEL_WIDTH);
});

test("the hop label and the observation counts are stated once", () => {
  expect(hopLabel(0)).toBe("direct");
  expect(hopLabel(1)).toBe("upstream-reported");
  expect(observationSummary(undefined)).toBe("No provider observation is recorded.");
  expect(
    observationSummary([
      { source: "a", status: "skipped" },
      { source: "b", status: "skipped" },
    ]),
  ).toBe("2 skipped");
});
