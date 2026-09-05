// @vitest-environment jsdom
import { fireEvent, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { COMPACT_SCREEN } from "@/lib/useMediaQuery";
import { openConsole, resetGateway, stubGateway } from "@/test/console";

// Between 768 and 1023 px the sidebar is the icon rail. Expanding it opens
// the full sidebar as an overlay that a navigation, Escape, or a click on
// the backdrop closes. The chat thread list is a sheet on this tier, since
// the column has no width to spare for it.
const MODELS = {
  data: [
    {
      id: "openai/gpt-x",
      name: "GPT X",
      offerings: [
        { provider: "openai", provider_model_id: "gpt-x", operations: ["chat-completions"] },
      ],
    },
  ],
};

function stubCompactViewport() {
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: query === COMPACT_SCREEN,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  );
}

beforeEach(() => {
  stubGateway({
    "/api/v1/models": MODELS,
    "/api/v1/admin/providers": { providers: [] },
    "/api/v1/providers": { providers: [] },
    "/api/v1/presets": { data: [] },
    "/console/identity/providers": { providers: [] },
  });
  stubCompactViewport();
});
afterEach(resetGateway);

test("an 800 px screen gets the icon rail, and expanding it opens an overlay", async () => {
  openConsole("/docs");
  const nav = await screen.findByRole("navigation", { name: "Console" });
  // The rail keeps every destination reachable by name, without a label.
  const models = within(nav).getByRole("link", { name: "Models" });
  expect(models.textContent).toBe("");
  expect(screen.queryByRole("button", { name: "Open navigation" })).toBeNull();
  expect(screen.queryByTestId("sidebar-backdrop")).toBeNull();

  fireEvent.click(screen.getByRole("button", { name: "Expand sidebar" }));
  expect(models.textContent).toBe("Models");
  expect(screen.getByTestId("sidebar-backdrop")).toBeTruthy();

  // A navigation closes the overlay; the rail is back.
  fireEvent.click(models);
  await screen.findByRole("button", { name: "Expand sidebar" });
  expect(screen.queryByTestId("sidebar-backdrop")).toBeNull();
});

test("Escape and the backdrop close the overlay, and the preference is untouched", async () => {
  openConsole("/docs");
  await screen.findByRole("navigation", { name: "Console" });
  fireEvent.click(screen.getByRole("button", { name: "Expand sidebar" }));
  fireEvent.keyDown(window, { key: "Escape" });
  await screen.findByRole("button", { name: "Expand sidebar" });

  fireEvent.click(screen.getByRole("button", { name: "Expand sidebar" }));
  fireEvent.click(screen.getByTestId("sidebar-backdrop"));
  await screen.findByRole("button", { name: "Expand sidebar" });
  // The rail on this tier is not the operator's collapse preference.
  expect(localStorage.getItem("starport.sidebar.collapsed")).toBe("0");
});

test("the compact tier keeps the catalog chip on the title line", async () => {
  openConsole("/models");
  await screen.findByRole("navigation", { name: "Console" });
  expect(screen.getByTestId("catalog-slot")).toBeTruthy();
  expect(screen.queryByTestId("top-bar-status")).toBeNull();
});

test("the chat thread list is a sheet on the compact tier", async () => {
  openConsole("/chat");
  const show = await screen.findByRole("button", { name: "Show conversations" });
  expect(screen.queryByRole("dialog", { name: "Conversations" })).toBeNull();
  fireEvent.click(show);
  expect(await screen.findByRole("dialog", { name: "Conversations" })).toBeTruthy();
});
