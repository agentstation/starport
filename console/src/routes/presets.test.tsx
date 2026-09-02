// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { json, openConsole, resetGateway, stubGateway } from "@/test/console";

afterEach(resetGateway);

const HEAD = {
  name: "draft",
  revision: 5,
  config: { model: "openai/gpt-x" },
  updated_at: "2026-09-01T10:00:00Z",
};
const OLD = {
  name: "draft",
  revision: 3,
  config: { model: "openai/gpt-w" },
  updated_at: "2026-08-30T10:00:00Z",
};

function rollbackBodies(): unknown[] {
  return vi
    .mocked(fetch)
    .mock.calls.filter(([input]) => String(input).endsWith("/rollback"))
    .map(([, init]) => JSON.parse(String(init?.body)));
}

describe("presets", () => {
  // JSX text does not decode an escape, so an empty cell once printed the
  // six characters of the escape instead of the dash.
  it("renders a dash, not an escape, for an empty overrides cell", async () => {
    stubGateway({ "/api/v1/presets": { data: [HEAD] } });
    openConsole("/presets");

    await screen.findByRole("button", { name: "History of draft" });
    expect(screen.queryByText("\\u2014")).toBeNull();
    expect(screen.getAllByText("\u2014").length).toBeGreaterThan(0);
  });

  // History reads as what each save changed, so the model swap shows the
  // old value struck through beside the new one, and the oldest revision
  // lists what it set.
  it("reads each revision as the fields it changed", async () => {
    stubGateway({
      "/api/v1/presets": { data: [HEAD] },
      "/api/v1/presets/draft/history": { data: [HEAD, OLD] },
    });
    openConsole("/presets");

    fireEvent.click(await screen.findByRole("button", { name: "History of draft" }));
    const dialog = await screen.findByRole("dialog", { name: "History of @preset/draft" });
    const rows = (await within(dialog).findAllByRole("row")).slice(1);
    expect(rows.map((row) => row.textContent)).toEqual([
      expect.stringContaining("Modelopenai/gpt-wopenai/gpt-x"),
      expect.stringContaining("Modelopenai/gpt-w"),
    ]);
    expect(rows[1]?.textContent).not.toContain("gpt-x");
  });

  // A restore lands as a new head, so the confirmation names the head the
  // operator read and the revision it copies before anything travels.
  it("names both revisions before a restore travels", async () => {
    stubGateway({
      "/api/v1/presets": { data: [HEAD] },
      "/api/v1/presets/draft/history": { data: [HEAD, OLD] },
      "/api/v1/presets/draft/rollback": () => json({ ...HEAD, revision: 6 }),
    });
    openConsole("/presets");

    fireEvent.click(await screen.findByRole("button", { name: "History of draft" }));
    const dialog = await screen.findByRole("dialog", { name: "History of @preset/draft" });
    fireEvent.click(await within(dialog).findByRole("button", { name: "Restore revision 3" }));

    const confirmation = within(dialog).getByRole("group", { name: "Restore confirmation" });
    expect(confirmation.textContent).toContain("from revision 5 to revision 3");
    expect(confirmation.textContent).toContain("lands as revision 6");
    expect(rollbackBodies()).toEqual([]);

    fireEvent.click(
      within(confirmation).getByRole("button", { name: "Restore to revision 3" }),
    );

    await waitFor(() =>
      expect(rollbackBodies()).toEqual([{ to_revision: 3, revision: 5 }]),
    );
  });
});
