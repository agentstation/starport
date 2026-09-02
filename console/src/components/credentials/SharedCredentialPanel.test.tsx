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
import { queries } from "@/lib/queries";

import { SharedCredentialPanel } from "./SharedCredentialPanel";

// The panel exists to make one claim true: an operator stores the shared
// provider credentials from the provider's own screen, sees every one of
// them with its access rule, and chooses that rule at creation — without
// visiting the keys page. These tests hold the gateway to that — what was
// sent, and to which route.
const gateway = vi.hoisted(() => ({
  applied: [] as { provider: string; body: unknown }[],
  updated: [] as { provider: string; credentialId: string; body: unknown }[],
  validated: [] as { provider: string; credentialId: string }[],
  stored: [] as {
    id: string;
    label?: string;
    access?: "open" | "granted";
    grants?: string[];
    has_credentials: boolean;
    created_at: string;
  }[],
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
    updateSharedCredential: async (
      provider: string,
      credentialId: string,
      body: unknown,
    ) => {
      gateway.updated.push({ provider, credentialId, body });
      return { provider, id: credentialId, has_credentials: true };
    },
    validateSharedCredential: async (
      provider: string,
      credentialId: string,
    ) => {
      gateway.validated.push({ provider, credentialId });
      return { valid: true };
    },
    listAccounts: async () => [
      { id: "acme", name: "Acme", active: true },
      { id: "globex", name: "Globex", active: true },
    ],
  };
});

const FIELDS: CredentialField[] = [
  { id: "api_key", kind: "secret", required: true },
  { id: "base_url", kind: "config" },
];

beforeEach(() => {
  gateway.applied = [];
  gateway.updated = [];
  gateway.validated = [];
  gateway.stored = [];
});

afterEach(cleanup);

function mount(fields: CredentialField[] = FIELDS) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  // Provider status is what a stored credential changes on the rest of the
  // console, so the harness seeds it and the save test reads its state.
  client.setQueryData(queries.providerStatus().queryKey, { providers: [] });
  const view = render(
    <QueryClientProvider client={client}>
      <SharedCredentialPanel
        providerId="groq"
        name="Groq"
        fields={fields}
        active
      />
    </QueryClientProvider>,
  );
  return { ...view, client };
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

// The access question is asked at creation and defaults to open: an operator
// who never touches it shares with every account, which is the shared
// plane's promise.
test("creates with every account as the unasked default", async () => {
  const { client } = mount();
  await openApplyModal();
  await waitFor(() => expect(screen.getByLabelText("api_key")).toBeTruthy());

  expect(
    (screen.getByRole("radio", { name: /Every account/ }) as HTMLInputElement)
      .checked,
  ).toBe(true);

  fireEvent.change(screen.getByLabelText("api_key"), {
    target: { value: "placeholder-value" },
  });
  fireEvent.change(screen.getByLabelText("base_url"), {
    target: { value: "https://example.invalid" },
  });
  fireEvent.click(screen.getByText("Apply credential"));

  await waitFor(() => expect(gateway.applied).toHaveLength(1));
  // The provider is the whole address. Nothing here carries an account,
  // because an open credential belongs to no account.
  expect(gateway.applied).toEqual([
    {
      provider: "groq",
      body: {
        credentials: { api_key: "placeholder-value" },
        config: { base_url: "https://example.invalid" },
        access: "open",
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

  // The save changed what the provider can serve, so the status read the
  // provider page and the chat fallback share is stale now.
  expect(
    client.getQueryState(queries.providerStatus().queryKey)?.isInvalidated,
  ).toBe(true);
});

// Choosing "only granted accounts" narrows the credential at birth: the
// grants ride the same create call, so there is no moment when the
// credential exists wider than the operator meant.
test("creates a granted credential with the chosen accounts", async () => {
  mount();
  await openApplyModal();
  await waitFor(() => expect(screen.getByLabelText("api_key")).toBeTruthy());

  fireEvent.click(screen.getByRole("radio", { name: /Only granted accounts/ }));
  await waitFor(() =>
    expect(screen.getByRole("checkbox", { name: "Acme" })).toBeTruthy(),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "Acme" }));
  fireEvent.change(screen.getByLabelText("Label"), {
    target: { value: "team-a" },
  });
  fireEvent.change(screen.getByLabelText("api_key"), {
    target: { value: "placeholder-value" },
  });
  fireEvent.click(screen.getByText("Apply credential"));

  await waitFor(() => expect(gateway.applied).toHaveLength(1));
  expect(gateway.applied).toEqual([
    {
      provider: "groq",
      body: {
        credentials: { api_key: "placeholder-value" },
        label: "team-a",
        access: "granted",
        grants: ["acme"],
      },
    },
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

// A provider can hold several shared credentials, and an operator telling
// them apart needs each one on screen with its label and its access rule.
// A drawer that collapses the plane to one row hides every credential after
// the first.
test("renders every shared credential with its label and access", async () => {
  gateway.stored = [
    {
      id: "shared-1",
      label: "team-a",
      access: "open",
      has_credentials: true,
      created_at: "2026-08-25T00:00:00Z",
    },
    {
      id: "shared-2",
      label: "team-b",
      access: "granted",
      grants: ["acme"],
      has_credentials: true,
      created_at: "2026-08-26T00:00:00Z",
    },
  ];
  mount();

  await waitFor(() => expect(screen.getByText("team-a")).toBeTruthy());
  expect(screen.getByText("team-b")).toBeTruthy();
  // The access rule is part of the row: which accounts each credential
  // serves is the fact the list exists to show.
  expect(screen.getByText(/Every account/)).toBeTruthy();
  expect(screen.getByText(/Only granted accounts/)).toBeTruthy();
  expect(screen.getByText(/acme/)).toBeTruthy();
  // Resolution walks the list in order, so only the first row can be the
  // one requests use.
  expect(screen.getAllByText("Active")).toHaveLength(1);
});

// The grants stay editable after creation: narrowing or widening a
// credential must not require rotating its value.
test("edits a credential's access without touching its value", async () => {
  gateway.stored = [
    {
      id: "shared-1",
      label: "team-a",
      access: "open",
      has_credentials: true,
      created_at: "2026-08-25T00:00:00Z",
    },
  ];
  mount();

  await waitFor(() => expect(screen.getByText("access…")).toBeTruthy());
  fireEvent.click(screen.getByText("access…"));

  await waitFor(() =>
    expect(
      screen.getByRole("radio", { name: /Only granted accounts/ }),
    ).toBeTruthy(),
  );
  fireEvent.click(screen.getByRole("radio", { name: /Only granted accounts/ }));
  await waitFor(() =>
    expect(screen.getByRole("checkbox", { name: "Globex" })).toBeTruthy(),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "Globex" }));
  fireEvent.click(screen.getByText("Save access"));

  await waitFor(() => expect(gateway.updated).toHaveLength(1));
  // The rule travels whole — the access word and the grant list together —
  // and no credential value rides along.
  expect(gateway.updated).toEqual([
    {
      provider: "groq",
      credentialId: "shared-1",
      body: { access: "granted", grants: ["globex"] },
    },
  ]);
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
