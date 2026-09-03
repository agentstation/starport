// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { UNAUTHORIZED_SENTENCE } from "@/components/shell/CatalogSummary";
import { json, openConsole, resetGateway, stubGateway } from "@/test/console";

// A gateway that refuses the catalog read gives the same answer to every
// repeat of the same request, so the console asks once. The chip states the
// limit of the session in a sentence, and the shell asks for neither the
// summary again nor the operator status it could never read.

function catalogRequests(): string[] {
  const calls = vi.mocked(globalThis.fetch).mock.calls;
  return calls
    .map((call) => new URL(String(call[0]), "http://localhost").pathname)
    .filter((path) => path.startsWith("/api/v1/catalog") || path.startsWith("/api/v1/admin/catalog"));
}

beforeEach(() => {
  stubGateway({
    "/api/v1/catalog": () => json({ error: { message: "denied" } }, 403),
    "/api/v1/admin/catalog/status": () => json({ error: { message: "denied" } }, 403),
    "/api/v1/models": { data: [] },
    "/api/v1/admin/providers": { providers: [] },
    "/api/v1/providers": { providers: [] },
    "/console/identity/providers": { providers: [] },
  });
});
afterEach(resetGateway);

test("after a 403 the chip states the limit of the session", async () => {
  openConsole("/");

  const chip = await screen.findByLabelText(UNAUTHORIZED_SENTENCE);
  expect(chip.getAttribute("data-posture")).toBe("authorization");
  expect(screen.getByTestId("catalog-authorization-glyph")).toBeTruthy();
  expect(screen.getByTestId("catalog-label").textContent).toBe("Catalog");
  // A refused read is not a freshness, so no dot claims one.
  expect(screen.queryByTestId("catalog-freshness-dot")).toBeNull();
});

test("after a 403 the shell sends no second catalog request", async () => {
  openConsole("/");

  await screen.findByLabelText(UNAUTHORIZED_SENTENCE);
  await waitFor(() => expect(catalogRequests().length).toBe(1));

  // A refused read must not come back on a window focus, on a reconnect, or
  // on a route change.
  fireEvent(window, new Event("focus"));
  fireEvent(window, new Event("online"));
  fireEvent.click(await screen.findByLabelText(UNAUTHORIZED_SENTENCE));
  await screen.findByTestId("catalog-panel");

  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(catalogRequests()).toEqual(["/api/v1/catalog"]);
});

test("the panel of a refused session states the same sentence and no scope error", async () => {
  openConsole("/");

  fireEvent.click(await screen.findByLabelText(UNAUTHORIZED_SENTENCE));

  const panel = await screen.findByTestId("catalog-unauthorized");
  expect(panel.textContent).toBe(UNAUTHORIZED_SENTENCE);
  expect(screen.queryByTestId("catalog-layers")).toBeNull();
});
