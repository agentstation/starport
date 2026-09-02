// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { openConsole, resetGateway, stubGateway } from "@/test/console";

afterEach(resetGateway);

const RECORD = {
  request_id: "req-1",
  key_id: "key-a",
  timestamp: "2026-09-01T10:00:00Z",
  protocol: "openai",
  operation: "chat",
  model_requested: "openai/gpt-5",
  model_used: "openai/gpt-5",
  provider: "openai",
  status: "error",
  status_code: 400,
  latency_ms: 12,
  cache_status: "HIT",
  cache_semantic: true,
  cache_similarity: 0.93,
  guardrail_verdict: "refuse",
  guardrail_check: "prompt-injection",
};

function exportUrls(): URL[] {
  return vi
    .mocked(fetch)
    .mock.calls.map(([input]) => String(input))
    .filter((url) => url.includes("/activity/export"))
    .map((url) => new URL(url, "http://console.test"));
}

function stubDownload() {
  const createObjectURL = vi.fn(() => "blob:starport/export");
  const revokeObjectURL = vi.fn();
  Object.assign(URL, { createObjectURL, revokeObjectURL });
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => undefined);
  return { createObjectURL, revokeObjectURL, click };
}

describe("usage page", () => {
  // The export streams the rows the listing shows: the admin route, the
  // active filters, and the format, fetched under the held credential and
  // handed to the browser as a file.
  it("exports the filtered listing as NDJSON through the admin export route", async () => {
    stubGateway({
      "/api/v1/admin/activity": { data: [RECORD] },
      "/api/v1/admin/activity/export": (url: URL) =>
        new Response(url.searchParams.get("format") === "csv" ? "request_id\nreq-1\n" : '{"request_id":"req-1"}\n'),
    });
    const download = stubDownload();
    openConsole("/usage?status=error&range=all");

    // The control waits for the listing, so a download never races its rows.
    await screen.findByText(/requests · all keys/);
    const button = screen.getByRole("button", { name: "Export NDJSON" });
    fireEvent.click(button);

    await waitFor(() => expect(download.click).toHaveBeenCalledTimes(1));
    const url = exportUrls()[0];
    expect(url?.pathname).toBe("/api/v1/admin/activity/export");
    expect(url?.searchParams.get("format")).toBe("ndjson");
    expect(url?.searchParams.get("status")).toBe("error");
    expect(url?.searchParams.get("limit")).toBeNull();
    expect(download.createObjectURL).toHaveBeenCalledTimes(1);
    expect(download.revokeObjectURL).toHaveBeenCalledWith("blob:starport/export");
    download.click.mockRestore();
  });

  // A guardrail verdict is a facet of the listing: the select carries it
  // beside the statuses, the row names it, and the gateway filters on it.
  it("reads the guardrail facet from the address and names the verdict on the row", async () => {
    stubGateway({ "/api/v1/admin/activity": { data: [RECORD] } });
    openConsole("/usage?guardrail=refuse&range=all");

    await screen.findByText(/requests · all keys/);
    const row = document.querySelector('[role="row"][aria-rowindex]') ?? document.querySelectorAll('[role="row"]')[1];
    expect(row?.textContent).toContain("refused");
    expect(row?.textContent).toContain("semantic");
    const select = screen.getByLabelText("Filter by status") as HTMLSelectElement;
    expect(select.value).toBe("guardrail:refuse");

    const listing = vi
      .mocked(fetch)
      .mock.calls.map(([input]) => new URL(String(input), "http://console.test"))
      .find((url) => url.pathname === "/api/v1/admin/activity" && url.searchParams.get("guardrail"));
    expect(listing?.searchParams.get("guardrail")).toBe("refuse");
  });
});
