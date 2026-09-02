// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
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

// An address that names a key opens its editor, so a reload or a shared
// link lands on the same panel.
test("an address with a selected key opens that key's editor", async () => {
  stubGateway({ "/api/v1/admin/keys": { keys: [KEY] } });
  openConsole(`/keys?selected=${KEY.id}`);
  const dialog = await screen.findByRole("dialog", { name: "Edit key · Ops key" });
  expect(dialog).toBeTruthy();
});

const RECORD = {
  request_id: "req_0001",
  timestamp: "2026-09-01T12:00:00Z",
  model_requested: "acme/test-model-one",
  model_used: "acme/test-model-one",
  provider: "acme",
  status: "ok",
  latency_ms: 120,
  tokens: { total: 42 },
};

// A filter change keeps the previous rows on screen until the filtered
// page lands, instead of blanking the table.
test("the previous usage rows stay visible while a filter change loads", async () => {
  let release: (() => void) | undefined;
  stubGateway({
    "/api/v1/admin/activity": async (url: URL) => {
      if (url.searchParams.get("status") === "error") {
        await new Promise<void>((resolve) => {
          release = resolve;
        });
        return json({ data: [] });
      }
      return json({ data: [RECORD] });
    },
  });
  openConsole("/usage");
  await screen.findByText("acme/test-model-one", { selector: ".truncate" });

  fireEvent.change(screen.getByLabelText("Filter by status"), {
    target: { value: "error" },
  });
  await waitFor(() => expect(release).toBeDefined());
  expect(screen.getByText("acme/test-model-one", { selector: ".truncate" })).toBeTruthy();
  expect(document.querySelector('[aria-busy="true"]')).toBeNull();

  release?.();
  await waitFor(() =>
    expect(screen.queryByText("acme/test-model-one", { selector: ".truncate" })).toBeNull(),
  );
  expect(screen.getByText("No requests match these filters.")).toBeTruthy();
});

// A window whose request log outruns what the page auto-loads is drawn
// from the newest slice alone, and every chart says so.
test("a capped sample renders the truncation caption", async () => {
  const base = Date.parse(RECORD.timestamp);
  stubGateway({
    "/api/v1/admin/activity": (url: URL) => {
      const page = Number(url.searchParams.get("cursor") ?? "0");
      const data = Array.from({ length: 200 }, (_, index) => ({
        ...RECORD,
        request_id: `req_${page}_${index}`,
        timestamp: new Date(base - (page * 200 + index) * 60_000).toISOString(),
      }));
      return json({ data, next_cursor: String(page + 1) });
    },
  });
  openConsole("/usage");
  const captions = await screen.findAllByText(/^Newest 1,000 requests only/, {}, { timeout: 5000 });
  expect(captions.length).toBe(4);
});
