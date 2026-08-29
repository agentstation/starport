// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import type { ProviderRuntimeStatus } from "@/lib/api";

import { ProviderCredentialCard } from "./ProviderCredentialCard";

// The card's contract is one glanceable answer — who pays — with everything
// else behind Manage. These tests hold it to that: the configured states name
// their payer in a sentence, the empty state teaches the three ways to make a
// payer exist, and the drawer separates the operator's shared sources from the
// account's own.

vi.mock("@tanstack/react-router", () => ({
  Link: ({
    to,
    children,
    ...rest
  }: { to: string; children?: ReactNode } & Record<string, unknown>) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}));

const gateway = vi.hoisted(() => ({
  stored: null as { has_credentials: boolean; created_at: string } | null,
  locked: false,
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    getGatewayCredential: async (provider: string) => {
      if (gateway.locked) {
        throw new actual.ApiError(401, "admin scope required", null);
      }
      if (!gateway.stored) {
        throw new actual.ApiError(404, `no credential for ${provider}`, null);
      }
      return { provider, ...gateway.stored };
    },
    listAccounts: async () => {
      if (gateway.locked) {
        throw new actual.ApiError(401, "admin scope required", null);
      }
      return [{ id: "acme", name: "Acme", active: true }];
    },
  };
});

beforeEach(() => {
  gateway.stored = null;
  gateway.locked = false;
});

afterEach(cleanup);

function mount(credential: ProviderRuntimeStatus["operator_credential"]) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ProviderCredentialCard
        providerId="groq"
        name="Groq"
        credential={credential}
        fields={[{ id: "api_key", kind: "secret", required: true }]}
      />
    </QueryClientProvider>,
  );
}

test("a usable environment credential is named as the payer", async () => {
  mount({ state: "ready", usable: true });

  await waitFor(() =>
    expect(
      screen.getByText(/Paid by the shared environment credential/),
    ).toBeTruthy(),
  );
  expect(screen.getByText("GROQ_API_KEY")).toBeTruthy();
  expect(screen.getByText("Active")).toBeTruthy();
});

test("a stored shared credential pays when the environment does not", async () => {
  gateway.stored = { has_credentials: true, created_at: "2026-08-25T00:00:00Z" };
  mount({ state: "not_configured", usable: false });

  await waitFor(() =>
    expect(
      screen.getByText(/Paid by the shared credential stored on this gateway/),
    ).toBeTruthy(),
  );
});

// The empty state is the setup lesson. It teaches all three sources and leads
// with the one this screen can finish, and it never sends anyone to the keys
// page: a gateway API key identifies a caller and cannot pay a provider.
test("an unpaid provider teaches the three sources without a keys link", async () => {
  const { container } = mount({ state: "not_configured", usable: false });

  await waitFor(() =>
    expect(screen.getByText(/Nothing pays Groq yet/)).toBeTruthy(),
  );
  expect(screen.getByText("Set shared credential")).toBeTruthy();
  expect(screen.getByText("STARPORT_GROQ_API_KEY")).toBeTruthy();
  expect(container.querySelector('a[href="/keys"]')).toBeNull();
  expect(container.querySelector('a[href="/accounts"]')).toBeTruthy();
});

// Who is reading is inferred, not asked: the stored-credential read needs the
// admin scope, so a locked answer means an account-side reader, who gets a
// pointer to their own credential instead of controls that would 403.
test("a reader without the admin scope is sent to their own credential", async () => {
  gateway.locked = true;
  mount({ state: "not_configured", usable: false });

  await waitFor(() =>
    expect(
      screen.getByText(/Shared credentials are applied by an operator/),
    ).toBeTruthy(),
  );
  expect(screen.queryByText("Set shared credential")).toBeNull();
});

test("the manage drawer separates shared sources from the account's own", async () => {
  gateway.stored = { has_credentials: true, created_at: "2026-08-25T00:00:00Z" };
  const { container } = mount({ state: "ready", usable: true });

  await waitFor(() => expect(screen.getByText("manage…")).toBeTruthy());
  fireEvent.click(screen.getByText("manage…"));

  await waitFor(() =>
    expect(screen.getByRole("heading", { name: "Shared" })).toBeTruthy(),
  );
  expect(screen.getByRole("heading", { name: "Accounts" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Environment" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Stored" })).toBeTruthy();
  // The environment credential is usable, so the stored one is shadowed.
  expect(screen.getByText("Applied")).toBeTruthy();
  // The account word on this screen is never BYOK; that word is taught on
  // the accounts screen.
  expect(container.textContent?.toLowerCase()).not.toContain("byok");
});
