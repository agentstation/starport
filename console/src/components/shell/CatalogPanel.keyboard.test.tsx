// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { CatalogIndicator } from "@/components/shell/CatalogPanel";
import { TooltipProvider } from "@/components/ui/tooltip";

// A reader who never touches a pointer must reach the panel and get back out
// of it. Enter and Space open it, Escape closes it, and the chip that opened
// it takes the focus again, so the next Tab continues from where the reader
// was rather than from the top of the page.

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    sessionIdentity: () => "session",
    catalogSummary: async () => ({
      generation_id: "01J9ABCDEFGHJKMNPQRSTVWXYZ",
      age_seconds: 60,
      usable: true,
      freshness: "current",
    }),
    catalogStatus: async () => {
      throw new actual.ApiError(403, "denied", null);
    },
    catalogChanges: async () => ({ available: false }),
  };
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <TooltipProvider>
        <CatalogIndicator />
      </TooltipProvider>
    </QueryClientProvider>,
  );
}

async function chip() {
  return await screen.findByTestId("catalog-chip");
}

test("Enter opens the panel", async () => {
  mount();
  const control = await chip();

  fireEvent.keyDown(control, { key: "Enter" });

  expect(await screen.findByTestId("catalog-panel")).toBeTruthy();
  expect(control.getAttribute("aria-expanded")).toBe("true");
});

test("Space opens the panel", async () => {
  mount();
  const control = await chip();

  fireEvent.keyDown(control, { key: " " });

  expect(await screen.findByTestId("catalog-panel")).toBeTruthy();
  expect(control.getAttribute("aria-expanded")).toBe("true");
});

test("Escape closes the panel and the focus returns to the chip", async () => {
  mount();
  const control = await chip();

  fireEvent.keyDown(control, { key: "Enter" });
  const panel = await screen.findByTestId("catalog-panel");

  fireEvent.keyDown(panel, { key: "Escape" });

  await waitFor(() => expect(screen.queryByTestId("catalog-panel")).toBeNull());
  expect(control.getAttribute("aria-expanded")).toBe("false");
  await waitFor(() => expect(document.activeElement).toBe(control));
});
