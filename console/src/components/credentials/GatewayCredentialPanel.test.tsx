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

import type { CredentialField } from "@/lib/api";

import { GatewayCredentialPanel } from "./GatewayCredentialPanel";

// The panel exists to make one claim true: an operator applies the
// deployment's provider credential from the provider's own screen, without
// visiting the keys page and without naming an account. These tests hold the
// gateway to that — what was sent, and to which route.
const gateway = vi.hoisted(() => ({
  applied: [] as { provider: string; body: unknown }[],
  validated: [] as { provider: string; credentialId: string }[],
  stored: [] as { id: string; has_credentials: boolean; created_at: string }[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    listSharedCredentials: async (provider: string) =>
      gateway.stored.map((entry) => ({ provider, ...entry })),
    createSharedCredential: async (provider: string, body: unknown) => {
      gateway.applied.push({ provider, body });
      const entry = {
        id: `shared-${gateway.stored.length + 1}`,
        has_credentials: true,
        created_at: "2026-08-25T00:00:00Z",
      };
      gateway.stored.push(entry);
      return { provider, ...entry };
    },
    validateSharedCredential: async (
      provider: string,
      credentialId: string,
    ) => {
      gateway.validated.push({ provider, credentialId });
      return { valid: true };
    },
  };
});

const FIELDS: CredentialField[] = [
  { id: "api_key", kind: "secret", required: true },
  { id: "base_url", kind: "config" },
];

beforeEach(() => {
  gateway.applied = [];
  gateway.validated = [];
  gateway.stored = [];
});

afterEach(cleanup);

function mount(fields: CredentialField[] = FIELDS) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <GatewayCredentialPanel
        providerId="groq"
        name="Groq"
        fields={fields}
        active
      />
    </QueryClientProvider>,
  );
}

// The apply flow lives in a modal: the row offers one primary action, and the
// dialog validates right after storing so the answer arrives before it closes.
async function openApplyModal() {
  await waitFor(() => expect(screen.getByText("Set credential")).toBeTruthy());
  fireEvent.click(screen.getByText("Set credential"));
}

// An empty list is the gateway saying "no credential here", which is a state
// to render rather than a failure to report. Reading it as an error would put
// a red message on every provider an operator has not configured yet.
test("reads a missing credential as a state, not a failure", async () => {
  mount();

  await waitFor(() =>
    expect(
      screen.getByText(/No shared credential is stored for this provider/),
    ).toBeTruthy(),
  );
  expect(screen.queryByText(/Failed to load/)).toBeNull();
  expect(screen.getByText("Not set")).toBeTruthy();
});

test("applies the deployment credential without naming an account", async () => {
  mount();
  await openApplyModal();
  await waitFor(() => expect(screen.getByLabelText("api_key")).toBeTruthy());

  fireEvent.change(screen.getByLabelText("api_key"), {
    target: { value: "gsk-secret" },
  });
  fireEvent.change(screen.getByLabelText("base_url"), {
    target: { value: "https://example.invalid" },
  });
  fireEvent.click(screen.getByText("Apply credential"));

  await waitFor(() => expect(gateway.applied).toHaveLength(1));
  // The provider is the whole address. Nothing here carries an account, because
  // this credential belongs to no account.
  expect(gateway.applied).toEqual([
    {
      provider: "groq",
      body: {
        credentials: { api_key: "gsk-secret" },
        config: { base_url: "https://example.invalid" },
      },
    },
  ]);

  // Validation ran inside the dialog, so the operator learns the credential
  // works before the modal closes — and it addressed the credential the
  // create returned, not the whole plane.
  await waitFor(() =>
    expect(screen.getByText(/Applied and validated/)).toBeTruthy(),
  );
  expect(gateway.validated).toEqual([
    { provider: "groq", credentialId: "shared-1" },
  ]);
});

// A secret with no value would rotate a working credential to nothing, and the
// catalog is what says which field is the secret.
test("refuses to submit configuration with no secret", async () => {
  mount();
  await openApplyModal();
  await waitFor(() => expect(screen.getByLabelText("base_url")).toBeTruthy());

  fireEvent.change(screen.getByLabelText("base_url"), {
    target: { value: "https://example.invalid" },
  });

  expect(
    screen.getByText("Apply credential").hasAttribute("disabled"),
  ).toBe(true);
  expect(gateway.applied).toHaveLength(0);
});

// The word BYOK names a credential an account brings for itself. This panel
// shows the opposite thing, and saying BYOK here is the confusion the whole
// separation exists to remove.
test("never calls the deployment credential BYOK", async () => {
  const { container } = mount();
  await waitFor(() =>
    expect(
      screen.getByText(/No shared credential is stored for this provider/),
    ).toBeTruthy(),
  );

  expect(container.textContent?.toLowerCase()).toContain("shared credential");
  expect(container.textContent?.toLowerCase()).not.toContain("byok");
});
