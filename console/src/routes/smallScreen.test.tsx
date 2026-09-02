// @vitest-environment jsdom
import { fireEvent, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { SMALL_SCREEN } from "@/lib/useMediaQuery";
import { openConsole, resetGateway, stubGateway } from "@/test/console";

// CPL-G1. Below 640 px the sidebar is a sheet behind a top-bar trigger and
// the model picker is a bottom sheet. The viewport is a matchMedia answer,
// so the same tree renders both layouts here.
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

function stubViewport(small: boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: small && query === SMALL_SCREEN,
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
});
afterEach(resetGateway);

test("a 375 px screen gets a top bar whose trigger opens the navigation sheet", async () => {
  stubViewport(true);
  // The top bar is a lazy chunk; load it so the first query finds it.
  await import("@/components/shell/SmallScreenNav");
  openConsole("/docs");
  const trigger = await screen.findByRole("button", { name: "Open navigation" });
  expect(screen.queryByRole("navigation", { name: "Console" })).toBeNull();
  fireEvent.click(trigger);
  const sheet = await screen.findByRole("dialog", { name: "Navigation" });
  expect(within(sheet).getByRole("link", { name: "Models" })).toBeTruthy();
});

test("a desktop screen keeps the sidebar and has no sheet trigger", async () => {
  stubViewport(false);
  openConsole("/docs");
  await screen.findByRole("navigation", { name: "Console" });
  expect(screen.queryByRole("button", { name: "Open navigation" })).toBeNull();
});

test("the model picker opens as a bottom sheet on a small screen", async () => {
  stubViewport(true);
  openConsole("/chat");
  fireEvent.click(await screen.findByRole("button", { name: "Choose model" }));
  const sheet = await screen.findByRole("dialog", { name: "Choose model" });
  // The desktop popover is a dialog too; the slot tells the two apart.
  expect(sheet.getAttribute("data-slot")).toBe("sheet-content");
  expect(within(sheet).getByRole("option", { name: /gpt-x/i })).toBeTruthy();
});
