// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { ByokPanel } from "./ByokPanel";

// BYOK is addressed by account. The panel's whole job is to store a credential
// against a tenant, and these tests hold it to that address — a call that
// dropped the tenant would silently write the deployment credential instead.
const gateway = vi.hoisted(() => ({
  stored: [] as { tenant: string; provider: string; body: unknown }[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    listProviderCatalog: async () => [
      {
        id: "groq",
        name: "Groq",
        credential_fields: [{ id: "api_key", kind: "secret", required: true }],
      },
    ],
    listBYOKCredentials: async (tenant: string) =>
      gateway.stored
        .filter((record) => record.tenant === tenant)
        .map((record) => ({
          provider: record.provider,
          has_credentials: true,
          created_at: "2026-08-25T00:00:00Z",
        })),
    putBYOKCredential: async (
      tenant: string,
      provider: string,
      body: unknown,
    ) => {
      gateway.stored.push({ tenant, provider, body });
      return {};
    },
    validateBYOKCredential: async () => ({ valid: true }),
  };
});

beforeEach(() => {
  gateway.stored = [];
});

afterEach(cleanup);

function mount(tenantId = "acme") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ByokPanel tenantId={tenantId} />
    </QueryClientProvider>,
  );
}

test("stores a credential against the account, not the deployment", async () => {
  mount("acme");
  await waitFor(() =>
    expect(
      screen.getByText("This account brings no credentials of its own."),
    ).toBeTruthy(),
  );

  // The add flow is a dialog: pick the provider, paste the secret, and the
  // store is validated before the dialog closes.
  fireEvent.click(screen.getByText("add credential…"));
  fireEvent.change(screen.getByLabelText("Provider"), {
    target: { value: "groq" },
  });
  await waitFor(() => expect(screen.getByLabelText("api_key")).toBeTruthy());
  fireEvent.change(screen.getByLabelText("api_key"), {
    target: { value: "gsk-tenant-secret" },
  });
  fireEvent.click(screen.getByText("Store credential"));

  await waitFor(() => expect(gateway.stored).toHaveLength(1));
  expect(gateway.stored[0]).toEqual({
    tenant: "acme",
    provider: "groq",
    body: { credentials: { api_key: "gsk-tenant-secret" } },
  });
  await waitFor(() =>
    expect(screen.getByText(/Applied and validated/)).toBeTruthy(),
  );
});

// This is the one screen that says BYOK. A reader arriving here has to be able
// to tell what makes these credentials different from the ones on a provider
// screen, and the word is how they tell.
test("names these credentials BYOK and says whose they are", async () => {
  const { container } = mount();
  await waitFor(() =>
    expect(
      screen.getByText("This account brings no credentials of its own."),
    ).toBeTruthy(),
  );

  expect(screen.getByText("BYOK credentials")).toBeTruthy();
  expect(container.textContent).toContain(
    "separate from the deployment credential",
  );
});

// A provider whose credential is already stored is not offered again: the add
// control lists what is missing, so the panel cannot invite a silent rotation
// that looks like a first-time apply.
test("offers only the providers this account has not covered", async () => {
  gateway.stored.push({ tenant: "acme", provider: "groq", body: {} });
  mount("acme");

  await waitFor(() => expect(screen.getByText("Groq")).toBeTruthy());
  fireEvent.click(screen.getByText("add credential…"));
  const options = screen.getByLabelText("Provider").querySelectorAll("option");
  expect([...options].map((option) => option.getAttribute("value"))).toEqual([
    "",
  ]);
});
