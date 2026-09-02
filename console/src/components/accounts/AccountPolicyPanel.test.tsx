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

import type { Account } from "@/lib/api";

import { AccountPolicyPanel } from "./AccountPolicyPanel";

// The panel exists to make the operator's account policy true on the wire:
// the three BYOK answers and the provider grants land exactly as chosen, the
// defaults travel as the gateway's clearing sentinels, and granting a
// provider grants every model unless the operator separately narrows it.
// These tests hold the panel to that — what was sent, for which account.
const gateway = vi.hoisted(() => ({
  updated: [] as { accountId: string; body: unknown }[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    listProviderCatalog: async () => [
      { id: "groq", name: "Groq", models: ["llama-a", "llama-b"] },
      { id: "openai", name: "OpenAI", models: ["gpt-x"] },
    ],
    updateAccount: async (accountId: string, body: unknown) => {
      gateway.updated.push({ accountId, body });
      return { id: accountId };
    },
  };
});

beforeEach(() => {
  gateway.updated = [];
});

afterEach(cleanup);

function mount(account: Partial<Account> = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <AccountPolicyPanel account={{ id: "acme", ...account }} />
    </QueryClientProvider>,
  );
}

// An account with no stored policy may bring credentials anywhere and reach
// everything: both defaults must show as the checked answer, not as absence.
test("shows the open defaults for an account with no stored policy", async () => {
  mount();

  expect(
    (screen.getByRole("radio", { name: /For every provider/ }) as HTMLInputElement)
      .checked,
  ).toBe(true);
  expect(
    (
      screen.getByRole("radio", {
        name: /Every provider and model/,
      }) as HTMLInputElement
    ).checked,
  ).toBe(true);
});

// "Not at all" is the first narrowed BYOK answer: the account keeps using the
// operator's credentials and may store none of its own.
test("saves the no-BYOK rule", async () => {
  mount();

  fireEvent.click(screen.getByRole("radio", { name: /Not at all/ }));
  fireEvent.click(screen.getByText("Save policy"));

  await waitFor(() => expect(gateway.updated).toHaveLength(1));
  expect(gateway.updated).toEqual([
    { accountId: "acme", body: { byok_policy: { mode: "none" }, access: [] } },
  ]);
});

// "Only for selected providers" narrows BYOK to a list. An empty list names
// no rule at all, so the save waits until a provider is chosen.
test("saves a selected-providers BYOK rule, and refuses an empty one", async () => {
  mount();

  fireEvent.click(
    screen.getByRole("radio", { name: /Only for selected providers/ }),
  );
  expect(
    screen.getByText("Save policy").hasAttribute("disabled"),
  ).toBe(true);

  await waitFor(() =>
    expect(screen.getByRole("checkbox", { name: "BYOK for Groq" })).toBeTruthy(),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "BYOK for Groq" }));
  fireEvent.click(screen.getByText("Save policy"));

  await waitFor(() => expect(gateway.updated).toHaveLength(1));
  expect(gateway.updated).toEqual([
    {
      accountId: "acme",
      body: { byok_policy: { mode: "selected", providers: ["groq"] }, access: [] },
    },
  ]);
});

// Returning to "for every provider" must erase the stored policy, and the
// gateway's word for that is the {"mode":"all"} sentinel — not a null, and
// not a selected rule with every provider listed.
test("clears a stored BYOK rule with the all sentinel", async () => {
  mount({ byok_policy: { mode: "selected", providers: ["groq"] } });

  // The stored rule is what the panel shows before any click.
  expect(
    (
      screen.getByRole("radio", {
        name: /Only for selected providers/,
      }) as HTMLInputElement
    ).checked,
  ).toBe(true);
  await waitFor(() =>
    expect(screen.getByRole("checkbox", { name: "BYOK for Groq" })).toBeTruthy(),
  );
  expect(
    (screen.getByRole("checkbox", { name: "BYOK for Groq" }) as HTMLInputElement)
      .checked,
  ).toBe(true);

  fireEvent.click(screen.getByRole("radio", { name: /For every provider/ }));
  fireEvent.click(screen.getByText("Save policy"));

  await waitFor(() => expect(gateway.updated).toHaveLength(1));
  expect(gateway.updated).toEqual([
    { accountId: "acme", body: { byok_policy: { mode: "all" }, access: [] } },
  ]);
});

// Granting a provider grants every model it serves: the entry travels with
// no models field, because model granularity is a separate opt-in and not a
// side effect of granting.
test("grants a provider with every model as the unasked default", async () => {
  mount();

  fireEvent.click(
    screen.getByRole("radio", { name: /Only selected providers/ }),
  );
  await waitFor(() =>
    expect(
      screen.getByRole("checkbox", { name: "Access to Groq" }),
    ).toBeTruthy(),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "Access to Groq" }));
  fireEvent.click(screen.getByText("Save policy"));

  await waitFor(() => expect(gateway.updated).toHaveLength(1));
  expect(gateway.updated).toEqual([
    { accountId: "acme", body: { byok_policy: { mode: "all" }, access: [{ provider: "groq" }] } },
  ]);
});

// The model opt-in narrows one provider without touching the others: the
// narrowed entry carries its models, and a save with none chosen would grant
// nothing, so it waits.
test("narrows one provider to chosen models through the opt-in", async () => {
  mount();

  fireEvent.click(
    screen.getByRole("radio", { name: /Only selected providers/ }),
  );
  await waitFor(() =>
    expect(
      screen.getByRole("checkbox", { name: "Access to Groq" }),
    ).toBeTruthy(),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "Access to Groq" }));
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Only specific models on Groq" }),
  );
  expect(
    screen.getByText("Save policy").hasAttribute("disabled"),
  ).toBe(true);

  fireEvent.click(screen.getByRole("checkbox", { name: "Grant llama-b" }));
  fireEvent.click(screen.getByText("Save policy"));

  await waitFor(() => expect(gateway.updated).toHaveLength(1));
  expect(gateway.updated).toEqual([
    {
      accountId: "acme",
      body: { byok_policy: { mode: "all" }, access: [{ provider: "groq", models: ["llama-b"] }] },
    },
  ]);
});

// Returning to "every provider and model" erases the stored grants, and the
// gateway's word for that is the empty list.
test("clears stored grants with the empty-list sentinel", async () => {
  mount({ access: [{ provider: "groq", models: ["llama-a"] }] });

  // The stored grant is what the panel shows before any click.
  expect(
    (
      screen.getByRole("radio", {
        name: /Only selected providers/,
      }) as HTMLInputElement
    ).checked,
  ).toBe(true);
  await waitFor(() =>
    expect(
      screen.getByRole("checkbox", { name: "Access to Groq" }),
    ).toBeTruthy(),
  );
  expect(
    (
      screen.getByRole("checkbox", { name: "Grant llama-a" }) as HTMLInputElement
    ).checked,
  ).toBe(true);

  fireEvent.click(
    screen.getByRole("radio", { name: /Every provider and model/ }),
  );
  fireEvent.click(screen.getByText("Save policy"));

  await waitFor(() => expect(gateway.updated).toHaveLength(1));
  expect(gateway.updated).toEqual([
    { accountId: "acme", body: { byok_policy: { mode: "all" }, access: [] } },
  ]);
});
