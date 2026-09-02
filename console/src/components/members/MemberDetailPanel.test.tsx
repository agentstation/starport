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

import { MemberDetailPanel } from "./MemberDetailPanel";

// The panel is the operator's view of one member: the direct grants it can
// edit and the reachable-accounts answer the gateway computes. These tests
// hold it to what actually travels for a grant and to rendering both lists
// as the gateway's, not the browser's.
const gateway = vi.hoisted(() => ({
  granted: [] as unknown[],
  removed: [] as unknown[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    listMemberGrants: async () => [{ account_id: "acct-direct", user_id: "u-1" }],
    listReachableAccounts: async () => ["acct-direct", "acct-team"],
    listAccounts: async () => [
      { id: "acct-direct" },
      { id: "acct-team" },
      { id: "acct-new", name: "New account" },
    ],
    createAccountGrant: async (body: unknown) => {
      gateway.granted.push(body);
      return body;
    },
    deleteAccountGrant: async (grant: unknown) => {
      gateway.removed.push(grant);
      return {};
    },
  };
});

beforeEach(() => {
  gateway.granted = [];
  gateway.removed = [];
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemberDetailPanel
        member={{ id: "u-1", subject: "google:1", email: "ada@example.com" }}
        onClose={() => {}}
      />
    </QueryClientProvider>,
  );
}

// The reachable list is the gateway's fold of direct and team grants: the
// team-granted account shows even though no direct grant names it.
test("shows the reachable accounts the gateway resolved", async () => {
  mount();

  const reachable = await screen.findAllByTestId("reachable-account-row");
  expect(reachable.map((row) => row.textContent)).toEqual([
    "acct-direct",
    "acct-team",
  ]);
  expect(screen.getAllByTestId("member-grant-row")).toHaveLength(1);
});

// Granting sends exactly one row: the chosen account and this member.
test("grants an account to the member", async () => {
  mount();

  // The account list arrives from the gateway; the option must exist
  // before the choice can land on it.
  await screen.findByRole("option", { name: "New account" });
  fireEvent.change(screen.getByRole("combobox", { name: "Account to grant" }), {
    target: { value: "acct-new" },
  });
  fireEvent.click(screen.getByText("Grant"));

  await waitFor(() => expect(gateway.granted).toHaveLength(1));
  expect(gateway.granted).toEqual([
    { account_id: "acct-new", user_id: "u-1" },
  ]);
});

// Removing a direct grant names the whole row: the account and the member.
// The write travels only after the dialog that names both confirms it.
test("removes a direct grant only after the operator confirms", async () => {
  mount();

  fireEvent.click(
    await screen.findByRole("button", {
      name: "Remove the acct-direct grant",
    }),
  );
  const dialog = await screen.findByRole("dialog", { name: "Remove grant" });
  expect(within(dialog).getByText("acct-direct")).toBeTruthy();
  expect(gateway.removed).toEqual([]);

  fireEvent.click(within(dialog).getByRole("button", { name: "Remove grant" }));

  await waitFor(() =>
    expect(gateway.removed).toEqual([
      { account_id: "acct-direct", user_id: "u-1" },
    ]),
  );
});
