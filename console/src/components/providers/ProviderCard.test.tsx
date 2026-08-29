// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import type { ProviderCatalogEntry, ProviderRuntimeStatus } from "@/lib/api";

import {
  availableOfferings,
  CatalogProviderCard,
  credentialRank,
  ProviderCard,
  providerHealth,
} from "./ProviderCard";

// The cards are unit-tested outside a live router: Link renders as a
// plain anchor whose href is the resolved path, which is exactly the
// contract the card depends on.
vi.mock("@tanstack/react-router", () => ({
  Link: ({
    to,
    params,
    children,
    ...rest
  }: {
    to: string;
    params?: Record<string, string>;
    children?: ReactNode;
  } & Record<string, unknown>) => {
    const href = Object.entries(params ?? {}).reduce(
      (path, [key, value]) => path.replace(`$${key}`, value),
      to,
    );
    return (
      <a href={href} {...rest}>
        {children}
      </a>
    );
  },
}));

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(() => Promise.resolve({ ok: false, text: () => Promise.resolve("") })),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function runtime(
  overrides: Partial<ProviderRuntimeStatus> = {},
): ProviderRuntimeStatus {
  return {
    provider_id: "groq",
    adapter: { state: "ready" },
    operator_credential: { state: "ready", usable: true },
    offerings: [
      { provider_model_id: "a", state: "healthy" },
      { provider_model_id: "b", state: "half_open" },
      { provider_model_id: "c", state: "open" },
    ],
    ...overrides,
  };
}

const entry: ProviderCatalogEntry = {
  id: "groq",
  name: "Groq",
  description: "Fast inference on custom LPU hardware.",
};

test("renders the id inline beside the name, not as its own row", () => {
  render(<ProviderCard status={runtime()} entry={entry} />);

  const name = screen.getByText("Groq");
  const id = screen.getByText("groq");
  // Same flex row: the identity line holds logo, name, and id together.
  expect(id.parentElement).toBe(name.parentElement);
  expect(id.className).toContain("font-mono");
  expect(id.className).not.toContain("rounded");
});

test("states the model count once, as 'M of N models available'", () => {
  render(<ProviderCard status={runtime()} entry={entry} />);

  expect(screen.getByText("2 of 3 models available")).toBeDefined();
});

test("links the whole card to the provider detail route", () => {
  render(<ProviderCard status={runtime()} entry={entry} />);

  expect(screen.getByTestId("provider-card").getAttribute("href")).toBe(
    "/providers/groq",
  );
});

test("shows the health badge as the single status on a healthy card", () => {
  render(<ProviderCard status={runtime()} entry={entry} />);

  // Two of three offerings admit requests, so the rollup says degraded.
  expect(screen.getByTestId("health-badge").textContent).toBe("degraded");
  // A usable credential renders no pill: the card states its status once.
  expect(screen.queryByText("ready")).toBeNull();
});

test("rolls adapter, circuit, and routing into one health verdict", () => {
  expect(providerHealth(runtime()).state).toBe("degraded");
  expect(
    providerHealth(
      runtime({ offerings: [{ provider_model_id: "a", state: "healthy" }] }),
    ).state,
  ).toBe("healthy");
  expect(
    providerHealth(runtime({ adapter: { state: "error" } })).state,
  ).toBe("unavailable");
  expect(
    providerHealth(
      runtime({ offerings: [{ provider_model_id: "a", state: "open" }] }),
    ).state,
  ).toBe("unavailable");
  expect(
    providerHealth(runtime({ adapter: { state: "no_offerings" }, offerings: [] }))
      .state,
  ).toBe("no_models");
});

test("separates a provider that never answered from one that refused", () => {
  // Every tripped circuit records the no-response reason: the gateway's own
  // requests got nothing back, so the verdict is unreachable.
  expect(
    providerHealth(
      runtime({
        offerings: [
          { provider_model_id: "a", state: "open", reason: "provider_unreachable" },
          { provider_model_id: "b", state: "open", reason: "provider_unreachable" },
        ],
      }),
    ).state,
  ).toBe("unreachable");
  // A provider that responded with errors — or a mix — stays unavailable.
  expect(
    providerHealth(
      runtime({
        offerings: [
          { provider_model_id: "a", state: "open", reason: "provider_unreachable" },
          { provider_model_id: "b", state: "open", reason: "provider_unavailable" },
        ],
      }),
    ).state,
  ).toBe("unavailable");
  // No recorded reason gives no evidence to upgrade the verdict.
  expect(
    providerHealth(
      runtime({ offerings: [{ provider_model_id: "a", state: "open" }] }),
    ).state,
  ).toBe("unavailable");
});

test("calls a provider down only on the word of its own status page", () => {
  const failed = {
    offerings: [
      { provider_model_id: "a", state: "open", reason: "provider_unreachable" },
    ],
  };
  // Nothing available and the provider itself confirms a major incident:
  // the status-page-confirmed verdict replaces the gateway's guess.
  expect(
    providerHealth(
      runtime({ ...failed, incident: { indicator: "major" } }),
    ).state,
  ).toBe("down");
  expect(
    providerHealth(
      runtime({ ...failed, incident: { indicator: "critical" } }),
    ).state,
  ).toBe("down");
  // A minor incident is context, not confirmation of an outage.
  expect(
    providerHealth(
      runtime({ ...failed, incident: { indicator: "minor" } }),
    ).state,
  ).toBe("unreachable");
  // A major incident alone never downgrades a provider that still serves.
  expect(
    providerHealth(runtime({ incident: { indicator: "major" } })).state,
  ).toBe("degraded");
});

test("shows the provider's own incident description on the card", () => {
  render(
    <ProviderCard
      status={runtime({
        incident: { indicator: "minor", description: "Elevated error rates" },
      })}
      entry={entry}
    />,
  );

  expect(screen.getByTestId("provider-incident").textContent).toBe(
    "Elevated error rates",
  );
});

test("labels a missing credential 'no credential'", () => {
  render(
    <ProviderCard
      status={runtime({ operator_credential: { state: "not_configured" } })}
      entry={entry}
    />,
  );

  expect(screen.getByText("no credential")).toBeDefined();
});

test("surfaces the credential failure reason", () => {
  render(
    <ProviderCard
      status={runtime({
        operator_credential: {
          state: "invalid",
          usable: false,
          reason: "credential_rejected",
        },
      })}
      entry={entry}
    />,
  );

  expect(screen.getByText("invalid")).toBeDefined();
  expect(screen.getByText("credential rejected")).toBeDefined();
});

test("reports an adapter with nothing to serve as 'no models'", () => {
  render(
    <ProviderCard
      status={runtime({ adapter: { state: "no_offerings" } })}
      entry={entry}
    />,
  );

  expect(screen.getByTestId("health-badge").textContent).toBe("no models");
});

test("catalog card links and counts models without runtime status", () => {
  render(
    <CatalogProviderCard entry={{ ...entry, models: ["m1", "m2"] }} />,
  );

  expect(screen.getByTestId("provider-card").getAttribute("href")).toBe(
    "/providers/groq",
  );
  expect(screen.getByText("2 models")).toBeDefined();
});

test("counts circuit states that admit attempts as available", () => {
  expect(
    availableOfferings([
      { provider_model_id: "a", state: "healthy" },
      { provider_model_id: "b", state: "half_open" },
      { provider_model_id: "c", state: "open" },
      { provider_model_id: "d", state: "unavailable" },
    ]),
  ).toBe(2);
  expect(availableOfferings(undefined)).toBe(0);
});

// A healthy offering the route planner drops never receives an attempt, so its
// circuit never opens. Counting it as available is how the card claimed every
// Groq model worked while most of them could not be reached.
test("excludes healthy offerings the route planner cannot reach", () => {
  expect(
    availableOfferings([
      { provider_model_id: "a", state: "healthy", routing: { state: "routable" } },
      {
        provider_model_id: "b",
        state: "healthy",
        routing: { state: "unroutable", reason: "operation_unsupported" },
      },
      { provider_model_id: "c", state: "healthy", routing: { state: "unknown" } },
      { provider_model_id: "d", state: "healthy" },
    ]),
  ).toBe(3);
});

test("ranks usable credentials first and missing credentials last", () => {
  expect(
    credentialRank(runtime({ operator_credential: { state: "ready", usable: true } })),
  ).toBe(0);
  expect(
    credentialRank(
      runtime({ operator_credential: { state: "invalid", usable: false } }),
    ),
  ).toBe(1);
  expect(
    credentialRank(runtime({ operator_credential: { state: "not_configured" } })),
  ).toBe(2);
});
