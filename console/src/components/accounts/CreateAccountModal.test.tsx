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

import { CreateAccountModal } from "./CreateAccountModal";

// The modal is where an account template earns its keep: picking one sends
// the template's id and nothing that would override its stamp, because an
// explicit field in the create request wins over the template on the wire.
// These tests hold the modal to that — what was sent, and what was not.
const gateway = vi.hoisted(() => ({
  created: [] as unknown[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    listAccountTemplates: async () => [
      { id: "org-default", name: "Org default" },
      { id: "trial", name: "Trial" },
    ],
    createAccount: async (body: unknown) => {
      gateway.created.push(body);
      return { id: (body as { id: string }).id };
    },
  };
});

beforeEach(() => {
  gateway.created = [];
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <CreateAccountModal onClose={() => {}} onCreated={() => {}} />
    </QueryClientProvider>,
  );
}

// Without a template the modal behaves as it always has: the chosen strategy
// travels explicitly, and no template field travels at all.
test("creates an account with the chosen strategy when no template is picked", async () => {
  mount();

  fireEvent.change(screen.getByPlaceholderText("acme"), {
    target: { value: "acme" },
  });
  fireEvent.click(screen.getByText("Create account"));

  await waitFor(() => expect(gateway.created).toHaveLength(1));
  const body = gateway.created[0] as Record<string, unknown>;
  expect(body.id).toBe("acme");
  expect(body.credential_strategy).toBe("operator_first");
  expect("template" in body).toBe(false);
});

// Picking a template sends its id and omits the explicit strategy: an
// explicit field would win over the template, and the point of picking one
// is that the template's defaults stamp the account.
test("creates from a template, sending its id and no overriding strategy", async () => {
  mount();

  const picker = await screen.findByRole("combobox", {
    name: "Account template",
  });
  fireEvent.change(picker, { target: { value: "org-default" } });

  // With a template chosen, the strategy control leaves the form entirely:
  // the template answers that question now.
  expect(
    screen.queryByRole("combobox", { name: "Credential strategy" }),
  ).toBeNull();

  fireEvent.change(screen.getByPlaceholderText("acme"), {
    target: { value: "acme" },
  });
  fireEvent.click(screen.getByText("Create account"));

  await waitFor(() => expect(gateway.created).toHaveLength(1));
  const body = gateway.created[0] as Record<string, unknown>;
  expect(body).toMatchObject({ id: "acme", template: "org-default" });
  expect("credential_strategy" in body).toBe(false);
});

// Returning to "no template" restores the explicit strategy control, so the
// operator is never stuck with a choice they backed out of.
test("returning to no template restores the strategy control", async () => {
  mount();

  const picker = await screen.findByRole("combobox", {
    name: "Account template",
  });
  fireEvent.change(picker, { target: { value: "trial" } });
  fireEvent.change(picker, { target: { value: "" } });

  expect(
    screen.getByRole("combobox", { name: "Credential strategy" }),
  ).toBeTruthy();
});

// The console's word for this thing is "template" — decision 2 of the plan.
test("the picker speaks the template word", async () => {
  mount();

  expect(await screen.findByText("Start from a template")).toBeTruthy();
});
