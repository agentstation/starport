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

import { TeamDetailPanel } from "./TeamDetailPanel";

// The panel governs one team's roster and its account grants. These tests
// hold every edit to what actually travels: the member added or removed and
// the grant row named in full.
const gateway = vi.hoisted(() => ({
  added: [] as { teamId: string; userId: string }[],
  dropped: [] as { teamId: string; userId: string }[],
  granted: [] as unknown[],
  removed: [] as unknown[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    listTeamMembers: async () => [{ user_id: "u-1", team_id: "t-1" }],
    listMembers: async () => [
      { id: "u-1", subject: "google:1", display_name: "Ada" },
      { id: "u-2", subject: "google:2", display_name: "Grace" },
    ],
    listTeamGrants: async () => [{ account_id: "acct-team", team_id: "t-1" }],
    listAccounts: async () => [{ id: "acct-team" }, { id: "acct-new" }],
    addTeamMember: async (teamId: string, userId: string) => {
      gateway.added.push({ teamId, userId });
      return { user_id: userId, team_id: teamId };
    },
    removeTeamMember: async (teamId: string, userId: string) => {
      gateway.dropped.push({ teamId, userId });
      return {};
    },
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
  gateway.added = [];
  gateway.dropped = [];
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
      <TeamDetailPanel
        team={{ id: "t-1", name: "Platform" }}
        onClose={() => {}}
      />
    </QueryClientProvider>,
  );
}

// The roster names people, not identifiers, when the member list knows them.
test("shows the roster with resolved member names", async () => {
  mount();

  expect(await screen.findByText("Ada")).toBeTruthy();
  expect(screen.getAllByTestId("team-member-row")).toHaveLength(1);
});

// Adding sends the chosen member to this team; nobody already on the roster
// is offered again.
test("adds a member to the roster", async () => {
  mount();

  // The member list arrives from the gateway; the option must exist
  // before the choice can land on it.
  await screen.findByRole("option", { name: "Grace" });
  fireEvent.change(screen.getByRole("combobox", { name: "Member to add" }), {
    target: { value: "u-2" },
  });
  fireEvent.click(screen.getByText("Add"));

  await waitFor(() =>
    expect(gateway.added).toEqual([{ teamId: "t-1", userId: "u-2" }]),
  );
});

// Removing names the member and this team, nothing else.
test("removes a member from the roster", async () => {
  mount();

  fireEvent.click(
    await screen.findByRole("button", { name: "Remove u-1 from the team" }),
  );

  await waitFor(() =>
    expect(gateway.dropped).toEqual([{ teamId: "t-1", userId: "u-1" }]),
  );
});

// Granting sends exactly one row: the chosen account and this team.
test("grants an account to the team", async () => {
  mount();

  // The account list arrives from the gateway; the option must exist
  // before the choice can land on it.
  await screen.findByRole("option", { name: "acct-new" });
  fireEvent.change(screen.getByRole("combobox", { name: "Account to grant" }), {
    target: { value: "acct-new" },
  });
  fireEvent.click(screen.getByText("Grant"));

  await waitFor(() =>
    expect(gateway.granted).toEqual([
      { account_id: "acct-new", team_id: "t-1" },
    ]),
  );
});

// Removing a grant names the whole row: the account and this team.
test("removes a team grant", async () => {
  mount();

  fireEvent.click(
    await screen.findByRole("button", { name: "Remove the acct-team grant" }),
  );

  await waitFor(() =>
    expect(gateway.removed).toEqual([
      { account_id: "acct-team", team_id: "t-1" },
    ]),
  );
});
