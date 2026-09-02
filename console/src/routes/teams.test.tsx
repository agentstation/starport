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
  // A team delete takes the roster and the grants with it, so the write
  // travels only after a dialog that names both counts confirms it.
  it("deletes a team only after the dialog names what goes with it", async () => {
    stubGateway({
      "/api/v1/admin/teams": { teams: [TEAM] },
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
