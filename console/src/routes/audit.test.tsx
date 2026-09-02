// @vitest-environment jsdom
import { screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { queries } from "@/lib/queries";
import { openConsole, resetGateway, stubGateway } from "@/test/console";

afterEach(resetGateway);

const RECORD = {
  id: 7,
  time: "2026-09-01T10:00:00Z",
  actor: "key:ci-deployer",
  action: "key.create",
  subject: "key_7",
  outcome: "ok",
  request_id: "req-42",
};

function auditUrls(): string[] {
  return vi
    .mocked(fetch)
    .mock.calls.map(([input]) => String(input))
    .filter((url) => url.includes("/api/v1/admin/audit"));
}

describe("audit log", () => {
  // The filters sit in the query key, so a changed action is a different
  // listing, and the same filters reach the gateway as parameters.
  it("carries the action filter into the query key and the request", async () => {
    stubGateway({ "/api/v1/admin/audit": { data: [RECORD] } });
    openConsole("/audit?action=key.create&range=all");

    await screen.findByTestId("audit-row");
    const url = new URL(auditUrls()[0] ?? "", "http://console.test");
    expect(url.searchParams.get("action")).toBe("key.create");
    expect(url.searchParams.get("since")).toBeNull();

    const key = queries.audit({ action: "key.create", limit: 100 }).queryKey;
    expect(key).toEqual(["audit", { action: "key.create", limit: 100 }]);
  });

  // A record's request is the join to the usage page, so the link carries
  // the id and the window that keeps the row reachable.
  it("links each record to its request on the usage page", async () => {
    stubGateway({ "/api/v1/admin/audit": { data: [RECORD] } });
    openConsole("/audit?range=all");

    const link = await screen.findByRole("link", { name: "req-42" });
    const href = link.getAttribute("href") ?? "";
    expect(href.startsWith("/usage?")).toBe(true);
    expect(new URL(href, "http://console.test").searchParams.get("request")).toBe("req-42");
  });

  // The actor reads as a name and the outcome as a state, not as the raw
  // enum and subject the store holds.
  it("names the actor and states the outcome", async () => {
    stubGateway({ "/api/v1/admin/audit": { data: [RECORD] } });
    openConsole("/audit?range=all");

    await screen.findByText("ci-deployer");
    expect(screen.queryByText("key:ci-deployer")).toBeNull();
    expect(screen.getByText("ok")).toBeTruthy();
  });

  // An empty bounded window says what it covered and offers the wider one.
  it("offers a wider range when the window is empty", async () => {
    stubGateway({ "/api/v1/admin/audit": { data: [] } });
    openConsole("/audit");

    await screen.findByText("Nothing was recorded in the last 30 days.");
    await waitFor(() => expect(screen.getByRole("button", { name: "Show all time" })).toBeTruthy());
    const url = new URL(auditUrls()[0] ?? "", "http://console.test");
    expect(url.searchParams.get("since")).not.toBeNull();
  });
});
