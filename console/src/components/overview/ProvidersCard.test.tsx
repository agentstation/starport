// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, test, vi } from "vitest";

import type { ProviderRuntimeStatus } from "@/lib/api";

import { credentialBlocked, ProvidersCard } from "./ProvidersCard";

// The card's one read is the provider status. Each test sets the
// providers the stub answers with.
const gateway = vi.hoisted(() => ({
  providers: [] as unknown[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    providerStatus: async () => ({
      revision: 1,
      catalog_generation_id: "gen-0123456789abcdef",
      providers: gateway.providers,
    }),
  };
});

// The card links each blocked provider to its page. Outside a live
// router, Link renders as a plain anchor whose href is the resolved path.
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

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <ProvidersCard />
    </QueryClientProvider>,
  );
}

const usable: ProviderRuntimeStatus = {
  provider_id: "groq",
  adapter: { state: "ready" },
  operator_credential: { state: "ready", usable: true },
};

const invalid: ProviderRuntimeStatus = {
  provider_id: "anthropic",
  adapter: { state: "ready" },
  operator_credential: {
    state: "invalid",
    reason: "authentication_failed",
    usable: false,
  },
};

const unconfigured: ProviderRuntimeStatus = {
  provider_id: "openai",
  adapter: { state: "ready" },
  operator_credential: { state: "not_configured", usable: false },
};

test("a credentialed provider the gateway cannot use is named with its reason", async () => {
  gateway.providers = [usable, invalid, unconfigured];
  mount();

  const list = await screen.findByTestId("credential-reasons");
  const rows = within(list).getAllByRole("listitem");
  expect(rows.map((row) => row.textContent)).toEqual([
    "anthropicauthentication failed",
  ]);
  // The row links to the provider page, where the fix lives.
  expect(within(list).getByRole("link").getAttribute("href")).toBe(
    "/providers/anthropic",
  );
  // The counts still read as before: three known, two credentialed, one usable.
  expect(screen.getByText("Known").nextElementSibling?.textContent).toBe("3");
  expect(screen.getByText("Credentialed").nextElementSibling?.textContent).toBe("2");
  expect(screen.getByText("Usable").nextElementSibling?.textContent).toBe("1");
});

test("each count opens the providers it counts, and each name links to its page", async () => {
  gateway.providers = [usable, invalid, unconfigured];
  mount();

  fireEvent.click(await screen.findByTestId("count-credentialed"));
  const list = await screen.findByTestId("count-credentialed-list");
  const links = within(list).getAllByRole("link");
  expect(links.map((link) => link.textContent)).toEqual(["anthropic", "groq"]);
  expect(links.map((link) => link.getAttribute("href"))).toEqual([
    "/providers/anthropic",
    "/providers/groq",
  ]);
});

test("a count of zero explains itself instead of opening an empty list", async () => {
  gateway.providers = [unconfigured];
  mount();

  fireEvent.click(await screen.findByTestId("count-usable"));
  expect(await screen.findByText("No provider credential is usable.")).toBeTruthy();
  expect(screen.queryByTestId("count-usable-list")).toBeNull();
});

test("the reason list stays absent when every credential is usable", async () => {
  gateway.providers = [usable, unconfigured];
  mount();

  await screen.findByText("Known");
  expect(screen.queryByTestId("credential-reasons")).toBeNull();
});

test("credentialBlocked falls back to the state when the reason is absent", () => {
  expect(
    credentialBlocked([
      { provider_id: "b", operator_credential: { state: "refreshing", usable: false } },
      { provider_id: "a", operator_credential: { state: "denied", reason: "credential_source_denied", usable: false } },
      unconfigured,
      usable,
    ]),
  ).toEqual([
    { providerId: "a", reason: "credential source denied" },
    { providerId: "b", reason: "refreshing" },
  ]);
});
