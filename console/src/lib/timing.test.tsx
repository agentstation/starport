// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { MetadataLine } from "@/components/chat/Messages";
import { StatsRow } from "@/components/overview/StatsRow";
import type { SystemMetrics } from "@/lib/api";
import { resetGateway, stubGateway } from "@/test/console";

import { BROWSER_ROUND_TRIP, GATEWAY_ADDED, GATEWAY_SERVICE, timingLabel } from "./timing";

afterEach(resetGateway);

// The answer the gateway gives today: both timings are partial and name
// their boundary (TestMetricsNameTimingBoundaries proves the Go side).
function metrics(sample: SystemMetrics["sample"]): SystemMetrics {
  return {
    requests: { total: 1200, errors: 3, rate_1min: 4 },
    latency: { p50: 420, p95: 900, p99: 1500, boundary: GATEWAY_SERVICE, complete: false },
    overhead: { p50: 3, p95: 8, p99: 12, boundary: GATEWAY_ADDED, complete: false },
    sample,
  };
}

function overview(body: SystemMetrics) {
  stubGateway({ "/api/v1/admin/metrics": body });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <StatsRow />
    </QueryClientProvider>,
  );
}

describe("timing labels", () => {
  it("a partial gateway measurement is labeled partial with its boundary", async () => {
    overview(metrics({ records: 1000, window: "24h0m0s", truncated: false }));

    expect(await screen.findByText("Gateway latency p50")).toBeTruthy();
    expect(screen.queryByText("Latency p50")).toBeNull();
    const service = screen.getByText("Partial: gateway service");
    expect(service.getAttribute("title")).toMatch(/Authentication, limits, budgets, and request decoding are outside/);
    expect(screen.getByText("Partial: gateway added")).toBeTruthy();
    expect(screen.queryByText(/^Complete/)).toBeNull();

    // A gateway that claims complete with no known boundary stays partial.
    expect(timingLabel({ complete: true }).text).toBe("Partial: boundary not reported");
    expect(timingLabel(undefined).complete).toBe(false);
  });

  it("a complete client measurement is labeled complete", () => {
    render(
      <MetadataLine
        message={{ role: "assistant", content: "Hello", stats: { ttftMs: 180, latencyMs: 1240 } }}
        model={undefined}
      />,
    );

    const scope = screen.getByText("Complete: browser round trip");
    expect(scope.getAttribute("title")).toBe(
      "From the browser request to the last byte. It includes the network and every gateway stage.",
    );
    expect(timingLabel({ boundary: BROWSER_ROUND_TRIP, complete: true }).complete).toBe(true);
  });

  it("a truncated metrics sample is labeled partial", async () => {
    overview(metrics({ records: 1000, window: "24h0m0s", truncated: true }));

    const service = await screen.findByText("Partial: gateway service, partial sample");
    expect(service.getAttribute("title")).toMatch(/The sample holds only the newest requests\.$/);
    expect(screen.getByText("Partial: gateway added, partial sample")).toBeTruthy();

    // A truncated sample makes even a complete timing partial.
    const label = timingLabel({ boundary: BROWSER_ROUND_TRIP, complete: true }, { truncated: true });
    expect(label).toMatchObject({ complete: false, text: "Partial: browser round trip, partial sample" });
  });
});
