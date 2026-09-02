// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { json, openConsole, resetGateway, stubGateway } from "@/test/console";

afterEach(resetGateway);

const TEAM = { id: "t-1", name: "Platform", created_at: "2026-09-01T10:00:00Z" };

function deleteCalls(): string[] {
  return vi
    .mocked(fetch)
    .mock.calls.filter(([, init]) => init?.method === "DELETE")
    .map(([input]) => new URL(String(input), "http://localhost").pathname);
}

describe("teams", () => {
  // Nobody can join a team on a gateway with no identity provider, and the
  // gateway refuses the roster outright, so the page reads as the setup it
  // needs and offers no create control.
  it("reads the identity setup instead of a create control when no provider exists", async () => {
    stubGateway({
      "/api/v1/admin/teams": () =>
        json({ error: { message: "Identity is not configured for this gateway" } }, 503),
      "/console/identity/providers": { providers: [] },
    });
    openConsole("/teams");

    const empty = await screen.findByTestId("identity-required");
    expect(empty.textContent).toContain("No identity provider is configured");
    expect(empty.textContent).toContain("STARPORT_IDENTITY_CALLBACK_BASE_URL");
    expect(screen.queryByRole("button", { name: "New team" })).toBeNull();
    expect(screen.queryByLabelText("Team name")).toBeNull();
    expect(screen.queryByText(/Failed to load teams/)).toBeNull();
  });

  // With a provider configured the empty roster stays a plain invitation
  // and the create control stays enabled.
  it("keeps the create control when a provider exists and no team does", async () => {
    stubGateway({
      "/api/v1/admin/teams": { teams: [] },
      "/console/identity/providers": { providers: ["github"] },
    });
    openConsole("/teams");

    await screen.findByText(/No team yet/);
    expect(screen.queryByTestId("identity-required")).toBeNull();
    expect(screen.getByRole("button", { name: "New team" })).toBeTruthy();
  });

  // A team delete takes the roster and the grants with it, so the write
  // travels only after a dialog that names both counts confirms it.
  it("deletes a team only after the dialog names what goes with it", async () => {
    stubGateway({
      "/api/v1/admin/teams": { teams: [TEAM] },
      "/console/identity/providers": { providers: ["github"] },
      "/api/v1/admin/teams/t-1/members": {
        members: [
          { user_id: "u-1", team_id: "t-1" },
          { user_id: "u-2", team_id: "t-1" },
          { user_id: "u-3", team_id: "t-1" },
        ],
      },
      "/api/v1/admin/teams/t-1/grants": {
        grants: [{ account_id: "acct-team", team_id: "t-1" }],
      },
      "/api/v1/admin/teams/t-1": () => json({}),
    });
    openConsole("/teams");

    fireEvent.click(
      await screen.findByRole("button", { name: "Delete the Platform team" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Delete team" });
    await within(dialog).findByText(/Its 3 members leave the roster/);
    expect(dialog.textContent).toContain("its 1 account grant end with the team");
    expect(dialog.textContent).toContain("There is no undo.");
    expect(deleteCalls()).toEqual([]);

    fireEvent.click(within(dialog).getByRole("button", { name: "Delete team" }));

    await waitFor(() => expect(deleteCalls()).toEqual(["/api/v1/admin/teams/t-1"]));
  });

  it("keeps the team when the operator cancels the dialog", async () => {
    stubGateway({
      "/api/v1/admin/teams": { teams: [TEAM] },
      "/console/identity/providers": { providers: ["github"] },
      "/api/v1/admin/teams/t-1/members": { members: [] },
      "/api/v1/admin/teams/t-1/grants": { grants: [] },
    });
    openConsole("/teams");

    fireEvent.click(
      await screen.findByRole("button", { name: "Delete the Platform team" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Delete team" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(deleteCalls()).toEqual([]);
    expect(screen.getByText("Platform")).toBeTruthy();
  });
});
