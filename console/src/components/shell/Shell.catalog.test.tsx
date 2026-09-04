// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { PANEL_WIDTH } from "@/components/shell/CatalogPanel";
import { SMALL_SCREEN } from "@/lib/useMediaQuery";
import { openConsole, resetGateway, stubGateway } from "@/test/console";

// The catalog indicator belongs to the shell, not to a page. A reader who
// walks from Overview to Models to Chat keeps the same chip in the same
// place, and a small screen keeps it inside the top bar as a 44 px control
// that a thumb reaches.

const SUMMARY = {
  generation_id: "01J9ABCDEFGHJKMNPQRSTVWXYZ",
  generated_at: "2026-09-01T00:00:00Z",
  age_seconds: 7200,
  usable: true,
  freshness: "current",
  providers: 12,
  models: 511,
  next_update_at: "2026-09-02T00:00:00Z",
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
    "/api/v1/catalog": SUMMARY,
    "/api/v1/models": { data: [] },
    "/api/v1/admin/providers": { providers: [] },
    "/api/v1/providers": { providers: [] },
    "/api/v1/presets": { data: [] },
    "/console/identity/providers": { providers: [] },
  });
});
afterEach(resetGateway);

for (const route of ["/", "/models", "/chat"]) {
  test(`the shell renders the catalog chip on ${route}`, async () => {
    stubViewport(false);
    openConsole(route);

    const chip = await screen.findByTestId("catalog-chip");
    expect(chip.tagName).toBe("BUTTON");
    expect(screen.getByTestId("catalog-slot")).toBeTruthy();
  });
}

test("a small screen drops the header slot and puts a 44 px control in the top bar", async () => {
  stubViewport(true);
  // The top bar is a lazy chunk; load it so the first query finds it.
  await import("@/components/shell/SmallScreenNav");
  openConsole("/models");

  const chip = await screen.findByTestId("catalog-chip");
  expect(chip.getAttribute("class")).toContain("size-11");
  expect(screen.getByTestId("top-bar-status").contains(chip)).toBe(true);
  expect(screen.queryByTestId("catalog-slot")).toBeNull();
  // The icon-only control carries the verdict and the age in its name.
  const named = await screen.findByLabelText(/The catalog is fresh\. It is 2h old\./);
  expect(named).toBe(chip);
});

test("the panel the chip opens is bounded to the viewport", async () => {
  stubViewport(false);
  openConsole("/");

  const chip = await screen.findByTestId("catalog-chip");
  fireEvent.click(chip);

  const panel = await screen.findByTestId("catalog-panel");
  expect(panel.getAttribute("class")).toContain(PANEL_WIDTH);
  expect(chip.getAttribute("aria-expanded")).toBe("true");
});
