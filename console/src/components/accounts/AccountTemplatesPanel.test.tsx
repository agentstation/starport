// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { AccountTemplatesPanel } from "./AccountTemplatesPanel";

// The panel is the management surface for account templates: it lists what
// the gateway holds, creates new ones, edits the two named fields, and
// deletes. These tests hold it to what actually travels for each of those.
const gateway = vi.hoisted(() => ({
  created: [] as unknown[],
  updated: [] as { templateId: string; body: unknown }[],
  deleted: [] as string[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    listAccountTemplates: async () => [
      {
        id: "org-default",
        name: "Org default",
        credential_strategy: "byok_first",
      },
      { id: "trial", name: "Trial" },
    ],
    listProviderCatalog: async () => [
      { id: "groq", name: "Groq", models: ["llama-a"] },
    ],
    createAccountTemplate: async (body: unknown) => {
      gateway.created.push(body);
      return body;
    },
    updateAccountTemplate: async (templateId: string, body: unknown) => {
      gateway.updated.push({ templateId, body });
      return { id: templateId };
    },
    deleteAccountTemplate: async (templateId: string) => {
      gateway.deleted.push(templateId);
      return {};
    },
  };
});

beforeEach(() => {
  gateway.created = [];
  gateway.updated = [];
  gateway.deleted = [];
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <AccountTemplatesPanel onClose={() => {}} />
    </QueryClientProvider>,
  );
}

// The list is the gateway's, not the browser's: both stored templates show.
test("lists the account templates the gateway holds", async () => {
  mount();

  expect(await screen.findByText("Org default")).toBeTruthy();
  expect(screen.getByText("Trial")).toBeTruthy();
});

// Creating a template sends exactly what the operator named.
test("creates a template with the named defaults", async () => {
  mount();

  fireEvent.click(await screen.findByText("New template"));
  fireEvent.change(screen.getByRole("textbox", { name: "Template ID" }), {
    target: { value: "team-default" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Template name" }), {
    target: { value: "Team default" },
  });
  fireEvent.change(
    screen.getByRole("combobox", { name: "Template credential strategy" }),
    { target: { value: "byok_only" } },
  );
  fireEvent.click(screen.getByText("Create template"));

  await waitFor(() => expect(gateway.created).toHaveLength(1));
  expect(gateway.created).toEqual([
    {
      id: "team-default",
      name: "Team default",
      credential_strategy: "byok_only",
    },
  ]);
});

// Editing renames one template and moves its strategy; nothing else travels.
test("saves an edited name and strategy for one template", async () => {
  mount();

  fireEvent.click(await screen.findByText("Org default"));
  const name = screen.getByRole("textbox", { name: "Template name" });
  fireEvent.change(name, { target: { value: "Org default v2" } });
  fireEvent.click(screen.getByText("Save template"));

  await waitFor(() => expect(gateway.updated).toHaveLength(1));
  expect(gateway.updated).toEqual([
    {
      templateId: "org-default",
      body: { name: "Org default v2", credential_strategy: "byok_first" },
    },
  ]);
});

// An open template offers the same policy editor an account has, saving to
// the template instead of any account.
test("saves a template's BYOK rule through the shared policy panel", async () => {
  mount();

  fireEvent.click(await screen.findByText("Org default"));
  fireEvent.click(screen.getByRole("radio", { name: /Not at all/ }));
  fireEvent.click(screen.getByText("Save policy"));

  await waitFor(() => expect(gateway.updated).toHaveLength(1));
  expect(gateway.updated).toEqual([
    {
      templateId: "org-default",
      body: { byok_policy: { mode: "none" }, access: [] },
    },
  ]);
});

// Deleting is addressed by id and touches nothing else, and it travels only
// after the operator confirms it in the dialog that names the template.
test("deletes one template only after the operator confirms", async () => {
  mount();

  fireEvent.click(
    await screen.findByRole("button", { name: "Delete the trial template" }),
  );
  const dialog = await screen.findByRole("dialog", { name: "Delete template" });
  expect(within(dialog).getByText("trial")).toBeTruthy();
  expect(gateway.deleted).toEqual([]);

  fireEvent.click(within(dialog).getByRole("button", { name: "Delete template" }));

  await waitFor(() => expect(gateway.deleted).toEqual(["trial"]));
});
