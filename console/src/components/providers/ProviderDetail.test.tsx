// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, test, vi } from "vitest";

import type { ActivityRecord } from "@/lib/api";

import {
  activityStats,
  checkedEnvNames,
  circuitBreakdown,
  EnvironmentCredentialPanel,
  HealthPanel,
  OfferingsTable,
  operatorEnvNames,
  policyChips,
  PolicyChips,
} from "./ProviderDetail";

// Unit tests run outside a live router: Link renders as a plain anchor
// with the resolved path and search string — the contract the page
// depends on.
vi.mock("@tanstack/react-router", () => ({
  Link: ({
    to,
    params,
    search,
    children,
    ...rest
  }: {
    to: string;
    params?: Record<string, string>;
    search?: Record<string, string>;
    children?: ReactNode;
  } & Record<string, unknown>) => {
    const path = Object.entries(params ?? {}).reduce(
      (resolved, [key, value]) => resolved.replace(`$${key}`, value),
      to,
    );
    const query = new URLSearchParams(search ?? {}).toString();
    return (
      <a href={query ? `${path}?${query}` : path} {...rest}>
        {children}
      </a>
    );
  },
}));

afterEach(() => {
  cleanup();
});

// --- Policy chips

test("maps declared policies and headquarters to chips", () => {
  expect(
    policyChips({
      id: "groq",
      headquarters: "United States",
      policies: {
        retains_data: false,
        trains_on_data: false,
        retention: "30 days",
        moderated: true,
      },
    }),
  ).toEqual([
    "no data retention",
    "no training on data",
    "retention: 30 days",
    "moderated",
    "HQ: United States",
  ]);
});

test("renders nothing when no policy facts are declared", () => {
  const { container } = render(<PolicyChips entry={{ id: "groq" }} />);
  expect(container.innerHTML).toBe("");
});

// --- Environment credential panel
//
// The panel reports one plane: the credential this gateway read from its own
// process environment. It never links to the keys page, because a gateway API
// key is not a provider credential and sending an operator there to fix a
// provider is what taught them the two were the same thing. The credential
// they can apply sits on this screen, in the panel beside it.

// noKeysLink asserts the separation the whole screen exists to make. Any
// anchor into /keys from this panel is the regression.
function noKeysLink(container: HTMLElement): void {
  expect(container.querySelector('a[href^="/keys"]')).toBeNull();
}

test("derives operator env names from the provider id", () => {
  expect(operatorEnvNames("groq")).toEqual([
    "GROQ_API_KEY",
    "STARPORT_GROQ_API_KEY",
  ]);
  expect(operatorEnvNames("azure-openai")).toEqual([
    "AZURE_OPENAI_API_KEY",
    "STARPORT_AZURE_OPENAI_API_KEY",
  ]);
});

test("states a usable credential without a fix-it path", () => {
  const { container } = render(
    <EnvironmentCredentialPanel
      providerId="groq"
      credential={{ state: "ready", usable: true }}
    />,
  );

  expect(screen.getByText(/Read from the gateway environment/)).toBeDefined();
  noKeysLink(container);
});

test("names the variables to set and sends nobody to the keys page", () => {
  const { container } = render(
    <EnvironmentCredentialPanel providerId="groq" credential={undefined} />,
  );

  expect(
    screen.getByText(
      "This gateway read no environment credential for this provider.",
    ),
  ).toBeDefined();
  expect(screen.getByText("GROQ_API_KEY")).toBeDefined();
  expect(screen.getByText("STARPORT_GROQ_API_KEY")).toBeDefined();
  expect(
    screen.getByText(/apply a gateway credential below/),
  ).toBeDefined();
  noKeysLink(container);
});

test("parses the server's checked-environment detail", () => {
  expect(
    checkedEnvNames("checked STARPORT_GOOGLE_AI_STUDIO_API_KEY, GEMINI_API_KEY"),
  ).toEqual(["STARPORT_GOOGLE_AI_STUDIO_API_KEY", "GEMINI_API_KEY"]);
  expect(checkedEnvNames("credential source environment is denied")).toEqual([]);
  expect(checkedEnvNames(undefined)).toEqual([]);
});

test("prefers the server's checked env names over the derived guess", () => {
  render(
    <EnvironmentCredentialPanel
      providerId="google-ai-studio"
      credential={{
        state: "not_configured",
        usable: false,
        reason: "credential_not_configured",
        detail: "checked STARPORT_GOOGLE_AI_STUDIO_API_KEY, GEMINI_API_KEY",
      }}
    />,
  );

  expect(screen.getByText("STARPORT_GOOGLE_AI_STUDIO_API_KEY")).toBeDefined();
  expect(screen.getByText("GEMINI_API_KEY")).toBeDefined();
  // The naive client-side derivation is replaced by the authoritative list.
  expect(screen.queryByText("GOOGLE_AI_STUDIO_API_KEY")).toBeNull();
});

test("shows a source failure detail beneath the state line", () => {
  render(
    <EnvironmentCredentialPanel
      providerId="google-vertex"
      credential={{
        state: "unavailable",
        usable: false,
        reason: "credential_source_unavailable",
        detail: "credential source environment is unavailable",
      }}
    />,
  );

  expect(screen.getByTestId("credential-detail").textContent).toBe(
    "credential source environment is unavailable",
  );
});

test("surfaces the failure reason for a broken credential", () => {
  const { container } = render(
    <EnvironmentCredentialPanel
      providerId="groq"
      credential={{ state: "invalid", usable: false, reason: "credential_rejected" }}
    />,
  );

  expect(
    screen.getByText(
      /The environment credential is invalid \(credential rejected\)\./,
    ),
  ).toBeDefined();
  noKeysLink(container);
});

// --- Health math

function record(overrides: Partial<ActivityRecord>): ActivityRecord {
  return { timestamp: "2026-08-22T00:00:00Z", status: "ok", ...overrides };
}

test("computes rolling request, error, latency, and routing stats", () => {
  const stats = activityStats([
    record({ latency_ms: 100, routing_ms: 1 }),
    record({ latency_ms: 200, routing_ms: 2 }),
    record({ latency_ms: 300, routing_ms: 3 }),
    record({ status: "error", latency_ms: 400, routing_ms: 4 }),
  ]);

  expect(stats.requests).toBe(4);
  expect(stats.errorRate).toBe(0.25);
  expect(stats.p50LatencyMs).toBe(200);
  expect(stats.p95LatencyMs).toBe(400);
  expect(stats.medianRoutingMs).toBe(2);
});

test("reports an empty window as zero without fake latencies", () => {
  const stats = activityStats([]);

  expect(stats.requests).toBe(0);
  expect(stats.errorRate).toBe(0);
  expect(stats.p50LatencyMs).toBeUndefined();
});

test("orders the circuit breakdown by admission severity", () => {
  expect(
    circuitBreakdown([
      { provider_model_id: "a", state: "open" },
      { provider_model_id: "b", state: "healthy" },
      { provider_model_id: "c", state: "healthy" },
      { provider_model_id: "d", state: "unavailable" },
    ]),
  ).toEqual([
    ["healthy", 2],
    ["open", 1],
    ["unavailable", 1],
  ]);
});

test("states when the last hour carried no requests", () => {
  render(
    <HealthPanel
      offerings={[{ provider_model_id: "a", state: "healthy" }]}
      records={[]}
    />,
  );

  expect(
    screen.getByText("No requests through this provider in the last hour."),
  ).toBeDefined();
});

test("renders window stats when the provider served requests", () => {
  render(
    <HealthPanel
      offerings={[{ provider_model_id: "a", state: "healthy" }]}
      records={[record({ latency_ms: 120, routing_ms: 1 })]}
    />,
  );

  expect(screen.getByText("Requests (1h)")).toBeDefined();
  expect(screen.getByText("0.0%")).toBeDefined();
});

// --- Served models

test("links each offering into the provider-filtered models list", () => {
  render(
    <OfferingsTable
      providerId="groq"
      offerings={[
        { provider_model_id: "llama-3.3-70b", state: "healthy" },
        { provider_model_id: "whisper-large", state: "open", reason: "rate_limited" },
      ]}
    />,
  );

  expect(
    screen.getByText("llama-3.3-70b").closest("a")?.getAttribute("href"),
  ).toBe("/models?provider=groq&q=llama-3.3-70b");
  expect(screen.getByText("rate limited")).toBeDefined();
  expect(screen.getByText("open")).toBeDefined();
});
