// @vitest-environment jsdom
import { screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { openConsole, resetGateway, stubGateway } from "@/test/console";

afterEach(resetGateway);

const BATCH = {
  id: "batch_1",
  object: "batch",
  endpoint: "/v1/chat/completions",
  input_file_id: "file_in",
  status: "completed",
  output_file_id: "file_out",
  created_at: 1_756_800_000,
  completed_at: 1_756_800_300,
  request_counts: { total: 2, completed: 2, failed: 0 },
};

describe("jobs page", () => {
  // The open panel lives in the address, so a link to the batches tab opens
  // on the batches tab and a plain link still opens on video jobs.
  it("opens the batches panel from the tab search parameter", async () => {
    stubGateway({
      "/v1/batches": { object: "list", data: [BATCH] },
      "/v1/videos": { object: "list", data: [] },
    });
    openConsole("/jobs?tab=batches");

    const row = await screen.findByTestId("batch-row");
    expect(row.textContent).toContain("batch_1");
    expect(row.textContent).toContain("2 of 2 completed");

    const tab = screen.getByRole("tab", { name: "Batches" });
    expect(tab.getAttribute("aria-selected")).toBe("true");
    expect(screen.getByRole("tab", { name: "Video jobs" }).getAttribute("aria-selected")).toBe(
      "false",
    );
  });

  it("opens on video jobs by default", async () => {
    stubGateway({
      "/v1/videos": { object: "list", data: [] },
      "/v1/models": { data: [] },
    });
    openConsole("/jobs");

    const tab = await screen.findByRole("tab", { name: "Video jobs" });
    expect(tab.getAttribute("aria-selected")).toBe("true");
    expect(await screen.findByText(/run no video jobs/)).toBeTruthy();
  });
});
