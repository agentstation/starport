// @vitest-environment jsdom
import { fireEvent, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test } from "vitest";

import { openConsole, resetGateway, stubGateway } from "@/test/console";

beforeEach(() => stubGateway({ "/api/v1/models": { data: [] } }));
afterEach(resetGateway);

const FOCUSABLE = 'a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])';

// A keyboard reader's first Tab lands on the skip link, which jumps past the
// sidebar to the main landmark.
test("the skip link is the first focusable element and targets the main landmark", async () => {
  openConsole("/models");

  const link = await screen.findByRole("link", { name: "Skip to content" });
  expect(document.querySelector(FOCUSABLE)).toBe(link);
  expect(link.getAttribute("href")).toBe("#main");
  expect(document.getElementById("main")?.tagName).toBe("MAIN");
});

// The two labels that were title case now read in sentence case, like every
// other destination in the rail.
test("the navigation labels read in sentence case", async () => {
  openConsole("/models");

  const nav = await screen.findByRole("navigation", { name: "Console" });
  expect(within(nav).getByRole("link", { name: "API keys" }).getAttribute("href")).toBe("/keys");
  expect(within(nav).getByRole("link", { name: "Audit log" }).getAttribute("href")).toBe("/audit");
  expect(within(nav).queryByText("API Keys")).toBeNull();
  expect(within(nav).queryByText("Audit Log")).toBeNull();
});

// The collapse toggle names the rail it resizes and reports whether the rail
// is open. Clicking sidebar whitespace no longer flips it.
test("the collapse toggle exposes the rail state and whitespace does not toggle it", async () => {
  openConsole("/models");

  const toggle = await screen.findByRole("button", { name: "Collapse sidebar" });
  const rail = document.getElementById(toggle.getAttribute("aria-controls") ?? "");
  expect(rail?.tagName).toBe("ASIDE");
  expect(toggle.getAttribute("aria-expanded")).toBe("true");

  fireEvent.click(rail as HTMLElement);
  expect(screen.getByRole("button", { name: "Collapse sidebar" }).getAttribute("aria-expanded")).toBe("true");

  fireEvent.click(toggle);
  const expand = screen.getByRole("button", { name: "Expand sidebar" });
  expect(expand.getAttribute("aria-expanded")).toBe("false");
  expect(expand.getAttribute("aria-controls")).toBe(rail?.id);
});
