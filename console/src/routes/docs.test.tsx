// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test } from "vitest";

import { NAV_SECTIONS } from "@/components/shell/Shell";
import { openConsole, resetGateway, stubGateway } from "@/test/console";

// CPL-F5. The docs page is the console's own map: every sidebar destination
// has a sentence here that links to it. The test reads the sidebar's real
// destination list rather than a copy, so a page added to the shell without
// a docs sentence fails here.
beforeEach(() => {
  stubGateway({
    "/api/v1/models": { data: [] },
    "/api/v1/admin/providers": { providers: [] },
    "/console/identity/providers": { providers: [] },
  });
});
afterEach(resetGateway);

const PERSONAS = ["Build with Starport", "Manage your account", "Operate the gateway"];

// Links inside the open persona panel. The sidebar links every destination
// too, so the panel is the scope, not the document.
async function panelLinks(persona: string): Promise<Set<string>> {
  fireEvent.click(screen.getByRole("tab", { name: persona }));
  await waitFor(() =>
    expect(screen.getByRole("tab", { name: persona }).getAttribute("aria-selected")).toBe("true"),
  );
  const panel = screen.getByRole("tabpanel");
  return new Set(
    within(panel)
      .getAllByRole("link")
      .map((link) => link.getAttribute("href") ?? ""),
  );
}

test("the docs page links every sidebar destination", async () => {
  openConsole("/docs");
  await screen.findByRole("heading", { name: "Documentation" });
  const linked = new Set<string>();
  for (const persona of PERSONAS) {
    for (const href of await panelLinks(persona)) linked.add(href);
  }
  const destinations = NAV_SECTIONS.flatMap((section) => section.items.map((item) => item.to));
  const missing = destinations.filter((to) => to !== "/docs" && !linked.has(to));
  expect(missing).toEqual([]);
});

test("the operate persona names the real health paths and the two newer scopes", async () => {
  openConsole("/docs");
  await screen.findByRole("heading", { name: "Documentation" });
  fireEvent.click(screen.getByRole("tab", { name: "Operate the gateway" }));
  const operate = await screen.findByRole("tabpanel");
  expect(operate.textContent).toContain("/health/live");
  expect(operate.textContent).toContain("/health/ready");
  expect(operate.textContent).not.toMatch(/GET \/health\b(?!\/)/);

  fireEvent.click(screen.getByRole("tab", { name: "Manage your account" }));
  const account = await screen.findByRole("tabpanel");
  expect(within(account).getByRole("row", { name: /moderations:write/ })).toBeTruthy();
  expect(within(account).getByRole("row", { name: /batches:write/ })).toBeTruthy();
});

test("every snippet reads the key from STARPORT_API_KEY", async () => {
  openConsole("/docs");
  await screen.findByRole("heading", { name: "Documentation" });
  for (const persona of PERSONAS) {
    fireEvent.click(screen.getByRole("tab", { name: persona }));
    const panel = await screen.findByRole("tabpanel");
    for (const block of Array.from(panel.querySelectorAll("pre"))) {
      const text = block.textContent ?? "";
      if (/api_key|apiKey|Authorization/.test(text)) {
        expect(text).toContain("STARPORT_API_KEY");
      }
    }
  }
});
