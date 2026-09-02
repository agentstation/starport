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

import { TeamDetailPanel } from "./TeamDetailPanel";

// The panel governs one team's roster and its account grants. These tests
// hold every edit to what actually travels: the member added or removed and
// the grant row named in full.
const gateway = vi.hoisted(() => ({
  added: [] as { teamId: string; userId: string }[],
  dropped: [] as { teamId: string; userId: string }[],
  granted: [] as unknown[],
  removed: [] as unknown[],
  updated: [] as { teamId: string; body: unknown }[],
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
    updateTeam: async (teamId: string, body: unknown) => {
      gateway.updated.push({ teamId, body });
      return body;
    },
  };
});

beforeEach(() => {
  gateway.added = [];
  gateway.dropped = [];
  gateway.granted = [];
  gateway.removed = [];
  gateway.updated = [];
});

afterEach(cleanup);

function mount(team: import("@/lib/api").Team = { id: "t-1", name: "Platform" }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <TeamDetailPanel team={team} onClose={() => {}} />
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

// Saving a budget sends the team's whole mutable surface — the name and the
// budget — with the dollars converted to the gateway's nano-USD unit.
test("saves a team spend budget", async () => {
  mount();

  fireEvent.change(
    screen.getByRole("spinbutton", { name: "Team spend budget (USD)" }),
    { target: { value: "5" } },
  );
  fireEvent.change(
    screen.getByRole("combobox", { name: "Team budget interval" }),
    { target: { value: "week" } },
  );
  fireEvent.click(screen.getByText("Save budget"));

  await waitFor(() =>
    expect(gateway.updated).toEqual([
      {
        teamId: "t-1",
        body: {
          name: "Platform",
          budget: { limit: 5_000_000_000, interval: "week" },
        },
      },
    ]),
  );
});

// An emptied amount never clears the budget on its own: the panel refuses
// the save and points at the one action that does.
test("refuses an emptied amount instead of clearing the budget", async () => {
  mount({
    id: "t-1",
    name: "Platform",
    budget: { limit: 2_000_000_000, interval: "day" },
  });

  const amount = screen.getByRole("spinbutton", {
    name: "Team spend budget (USD)",
  }) as HTMLInputElement;
  expect(amount.value).toBe("2");
  fireEvent.change(amount, { target: { value: "" } });
  fireEvent.click(screen.getByText("Save budget"));

  expect(
    await screen.findByText("Enter an amount, or remove the budget below."),
  ).toBeTruthy();
  expect(gateway.updated).toEqual([]);
});

// Removing the budget restates the team and the ceiling, waits for the
// confirmation, then PUTs the team whole without a budget at the revision
// the panel read.
test("removes the budget only after the operator confirms", async () => {
  mount({
    id: "t-1",
    name: "Platform",
    budget: { limit: 2_000_000_000, interval: "day" },
    revision: 7,
  });

  fireEvent.click(screen.getByRole("button", { name: "Remove budget" }));
  const dialog = await screen.findByRole("dialog", { name: "Remove budget" });
  expect(dialog.textContent).toContain("$2 per day");
  expect(gateway.updated).toEqual([]);

  fireEvent.click(within(dialog).getByRole("button", { name: "Remove budget" }));

  await waitFor(() =>
    expect(gateway.updated).toEqual([
      { teamId: "t-1", body: { name: "Platform", revision: 7 } },
    ]),
  );
});

// A budget save names the revision the panel read, so the gateway can
// refuse a write over a rename that landed in between.
test("sends the revision it read with a budget save", async () => {
  mount({ id: "t-1", name: "Platform", revision: 3 });

  fireEvent.change(
    screen.getByRole("spinbutton", { name: "Team spend budget (USD)" }),
    { target: { value: "1" } },
  );
  fireEvent.click(screen.getByText("Save budget"));

  await waitFor(() =>
    expect(gateway.updated).toEqual([
      {
        teamId: "t-1",
        body: {
          name: "Platform",
          budget: { limit: 1_000_000_000, interval: "month" },
          revision: 3,
        },
      },
    ]),
  );
});

// A team four fifths through its window shows the meter at that point with
// what is left and when the window resets.
test("draws the budget meter at 80 percent", () => {
  mount({
    id: "t-1",
    name: "Platform",
    budget: { limit: 10_000_000_000, interval: "month" },
    budgets: {
      spend: {
        limit: 10_000_000_000,
        interval: "month",
        used: 8_000_000_000,
        remaining: 2_000_000_000,
        window_start: "2026-09-01T00:00:00Z",
        window_end: "2026-10-01T00:00:00Z",
      },
    },
  });

  const meter = screen.getByRole("meter", { name: "spend budget" });
  expect(meter.getAttribute("aria-valuenow")).toBe("8000000000");
  expect(meter.getAttribute("aria-valuemax")).toBe("10000000000");
  expect(meter.getAttribute("aria-valuetext")).toBe("$8 of $10, 80%");
  expect((meter.firstElementChild as HTMLElement).style.width).toBe("80%");
  expect(screen.getByText("$2 left")).toBeTruthy();
  expect(screen.getByText(/resets/)).toBeTruthy();
});

// A meter the gateway could not read says so instead of drawing an empty
// bar.
test("names an unreadable budget meter", () => {
  mount({
    id: "t-1",
    name: "Platform",
    budget: { limit: 10_000_000_000, interval: "month" },
    budgets: { spend: { limit: 10_000_000_000, interval: "month", error: "unavailable" } },
  });

  expect(screen.queryByRole("meter")).toBeNull();
  expect(screen.getByText("· usage unavailable")).toBeTruthy();
});

// A negative amount never travels: the panel refuses it locally.
test("refuses a non-positive budget amount", async () => {
  mount();

  fireEvent.change(
    screen.getByRole("spinbutton", { name: "Team spend budget (USD)" }),
    { target: { value: "-1" } },
  );
  fireEvent.click(screen.getByText("Save budget"));

  expect(
    await screen.findByText("The spend budget must be a positive amount."),
  ).toBeTruthy();
  expect(gateway.updated).toEqual([]);
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
