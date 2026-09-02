// @vitest-environment jsdom
import { fireEvent, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { json, openConsole, resetGateway, stubGateway } from "@/test/console";

afterEach(resetGateway);

const ACCOUNTS = {
  accounts: [
    { id: "default", name: "Default" },
    { id: "acme", name: "Acme" },
  ],
};

function deleteCalls(): string[] {
  return vi
    .mocked(fetch)
    .mock.calls.filter(([, init]) => init?.method === "DELETE")
    .map(([input]) => new URL(String(input), "http://localhost").pathname);
}

describe("accounts", () => {
  // The gateway refuses a delete while the account still holds keys. The
  // dialog names the count up front, and the refusal lands in the dialog
  // instead of a toast, so the operator reads the next step where they are.
  it("names the keys an account holds and keeps the refusal in the dialog", async () => {
    stubGateway({
      "/api/v1/admin/accounts": ACCOUNTS,
      "/api/v1/admin/keys": {
        keys: [
          { id: "k-1", name: "ci", account_id: "acme" },
          { id: "k-2", name: "dev", account_id: "acme" },
        ],
      },
      "/api/v1/admin/accounts/acme": () =>
        json(
          {
            error: {
              message:
                "This account still holds gateway API keys; delete or reassign them first",
            },
          },
          409,
        ),
    });
    openConsole("/accounts");

    fireEvent.click(
      await screen.findByRole("button", { name: "Delete the acme account" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Delete account" });
    expect(dialog.textContent).toContain("It holds 2 gateway API keys.");
    expect(deleteCalls()).toEqual([]);

    fireEvent.click(within(dialog).getByRole("button", { name: "Delete account" }));

    await within(dialog).findByText(/still holds gateway API keys/);
    expect(deleteCalls()).toEqual(["/api/v1/admin/accounts/acme"]);
    expect(screen.getByRole("dialog", { name: "Delete account" })).toBeTruthy();
  });
});
