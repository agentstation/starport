// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import type { ProviderCatalogEntry, ProviderRuntimeStatus } from "@/lib/api";

import { CatalogProviderCard, ProviderCard, credentialRank } from "./ProviderCard";

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
      { provider_model_id: "a", state: "available" },
      { provider_model_id: "b", state: "available" },
      { provider_model_id: "c", state: "unavailable" },
    ],
    ...overrides,
  };
}

const entry: ProviderCatalogEntry = {
  id: "groq",
  name: "Groq",
  description: "Fast inference on custom LPU hardware.",
};

test("renders the slug inline beside the name, not as its own row", () => {
  render(<ProviderCard status={runtime()} entry={entry} />);

  const name = screen.getByText("Groq");
  const slug = screen.getByText("groq");
  // Same flex row: the identity line holds logo, name, and slug together.
  expect(slug.parentElement).toBe(name.parentElement);
  expect(slug.className).toContain("font-mono");
  expect(slug.className).not.toContain("rounded");
});

test("states the model count as 'N models · M available'", () => {
  render(<ProviderCard status={runtime()} entry={entry} />);

  expect(screen.getByText("3 models · 2 available")).toBeDefined();
});

test("links the whole card to the provider detail route", () => {
  render(<ProviderCard status={runtime()} entry={entry} />);

  expect(screen.getByTestId("provider-card").getAttribute("href")).toBe(
    "/providers/groq",
  );
});

test("shows the credential pill as the single status on a healthy card", () => {
  render(<ProviderCard status={runtime()} entry={entry} />);

  expect(screen.getByText("ready")).toBeDefined();
  // A ready adapter renders no second status treatment.
  expect(screen.queryByText("no offerings")).toBeNull();
  expect(screen.queryByText("unknown")).toBeNull();
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

test("shows the adapter state only when the adapter is at fault", () => {
  render(
    <ProviderCard
      status={runtime({ adapter: { state: "no_offerings" } })}
      entry={entry}
    />,
  );

  expect(screen.getByText("no offerings")).toBeDefined();
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
