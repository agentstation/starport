// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";

import { json, openConsole, resetGateway, stubGateway } from "@/test/console";

afterEach(resetGateway);

const KEY = {
  id: "sk-starport-test-0001",
  name: "Ops key",
  active: true,
  scopes: ["models:read"],
  created_at: "2026-09-01T00:00:00Z",
};

// openDeleteDialog lands on the keys page with one key, opens its delete
// dialog from the row action, and returns the dialog and the trigger.
async function openDeleteDialog(answers: Record<string, unknown> = {}) {
  stubGateway({ "/api/v1/admin/keys": { keys: [KEY] }, ...answers });
  openConsole("/keys");
  const trigger = await screen.findByRole("button", { name: "Delete Ops key" });
  trigger.focus();
  fireEvent.click(trigger);
  const dialog = await screen.findByRole("dialog", { name: "Delete key" });
  return { dialog, trigger };
}

// A modal dialog names itself from its title and locks the page behind it,
// so a screen reader announces the question and the page cannot scroll away.
test("the delete dialog is labelled by its title and locks body scroll", async () => {
  const { dialog } = await openDeleteDialog();
  const labelledBy = dialog.getAttribute("aria-labelledby");
  expect(labelledBy).toBeTruthy();
  expect(document.getElementById(labelledBy!)?.textContent).toBe("Delete key");
  await waitFor(() => expect(document.body.style.overflowY).toBe("hidden"));
});

// Focus stays inside the dialog: the trap sends focus that leaves past the
// last control back into the dialog instead of into the page behind it.
test("focus that tabs past the last control stays inside the dialog", async () => {
  const { dialog } = await openDeleteDialog();
  await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
  const last = within(dialog).getByRole("button", { name: "Delete key" });
  last.focus();
  expect(document.activeElement).toBe(last);
  const guards = document.querySelectorAll<HTMLElement>("[data-base-ui-focus-guard]");
  const trailing = guards[guards.length - 1];
  expect(trailing).toBeDefined();
  trailing?.focus();
  await waitFor(() => {
    expect(document.activeElement).not.toBe(last);
    expect(dialog.contains(document.activeElement)).toBe(true);
  });
});

// Closing the dialog returns focus to the control that opened it, so a
// keyboard user continues from the row they were on.
test("closing the delete dialog returns focus to its trigger", async () => {
  const { dialog, trigger } = await openDeleteDialog();
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  await waitFor(() => expect(document.activeElement).toBe(trigger));
});

// A failed delete reports inside the dialog, where the operator is looking,
// and keeps the dialog open for a retry.
test("a failed delete shows its error inside the dialog", async () => {
  const { dialog } = await openDeleteDialog({
    [`/api/v1/admin/keys/${KEY.id}`]: () =>
      json({ error: { message: "key is in use" } }, 409),
  });
  fireEvent.click(within(dialog).getByRole("button", { name: "Delete key" }));
  const alert = await within(dialog).findByRole("alert");
  expect(alert.textContent).toMatch(/^Delete failed: /);
  expect(screen.getByRole("dialog", { name: "Delete key" })).toBeTruthy();
});

// A control names its outcome. The create dialog hands off to the secret
// dialog, so the toast is the one place that says the key now exists.
test("creating a key announces it in a toast", async () => {
  stubGateway({
    "/api/v1/admin/keys": {
      keys: [KEY],
      key: { ...KEY, id: "sk-starport-test-0002", key: "sk-starport-secret" },
    },
  });
  openConsole("/keys");
  fireEvent.click(await screen.findByRole("button", { name: "New key" }));
  const dialog = await screen.findByRole("dialog", { name: "New API key" });
  fireEvent.change(within(dialog).getByLabelText("Name"), {
    target: { value: "CI key" },
  });
  fireEvent.click(within(dialog).getByRole("button", { name: "Create key" }));

  expect(await screen.findByText("Key created")).toBeTruthy();
  expect(await screen.findByText("sk-starport-secret")).toBeTruthy();
});
