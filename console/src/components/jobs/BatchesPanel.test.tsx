// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { BatchesPanel } from "./BatchesPanel";

// CPL-E7. A batch is a count of lines that moved, so the row has to carry
// the three counts a reader acts on: what completed, what failed, and what
// the file held. A cancel ends work the operator paid to start, so it
// travels only after a dialog confirms it. An error file is the reason for
// every failed line, so the row offers it under the held credential.

const gateway = vi.hoisted(() => ({
  batches: [] as unknown[],
  cancelled: [] as string[],
  fetched: [] as string[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    hasCredential: () => true,
    isCredentialRejected: () => false,
    onCredentialChange: () => () => {},
    listBatches: async () => ({ batches: gateway.batches, capped: false }),
    cancelBatch: async (batchID: string) => {
      gateway.cancelled.push(batchID);
      return {};
    },
    fetchStoredFile: async (fileID: string) => {
      gateway.fetched.push(fileID);
      return new Blob(["{}"], { type: "application/jsonl" });
    },
  };
});

function nowSeconds(): number {
  return Math.floor(Date.now() / 1000);
}

beforeEach(() => {
  gateway.batches = [];
  gateway.cancelled = [];
  gateway.fetched = [];
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <BatchesPanel />
    </QueryClientProvider>,
  );
}

test("a batch row carries the completed, failed, and total counts on its bar", async () => {
  gateway.batches = [
    {
      id: "batch_7",
      endpoint: "/v1/chat/completions",
      input_file_id: "file_in",
      status: "in_progress",
      created_at: nowSeconds() - 120,
      request_counts: { total: 10, completed: 7, failed: 2 },
    },
  ];

  mount();

  const progress = await screen.findByTestId("batch-progress");
  expect(progress.textContent).toContain("7 of 10 completed");
  expect(progress.textContent).toContain("2 failed");

  const meter = within(progress).getByRole("meter");
  expect(meter.getAttribute("aria-valuenow")).toBe("9");
  expect(meter.getAttribute("aria-valuemax")).toBe("10");
  expect(meter.getAttribute("aria-valuetext")).toBe("7 completed, 2 failed, 10 total");

  // The state is a lifecycle pill without the wire underscore.
  const pill = screen.getByText("in progress");
  expect(pill.getAttribute("class") ?? "").toContain("bg-info-tint");
  expect(screen.queryByText("in_progress")).toBeNull();
});

test("a failed batch names the reason and offers its error file", async () => {
  gateway.batches = [
    {
      id: "batch_failed",
      endpoint: "/v1/embeddings",
      input_file_id: "file_in",
      status: "failed",
      created_at: nowSeconds() - 600,
      failed_at: nowSeconds() - 300,
      error_file_id: "file_err",
      request_counts: { total: 4, completed: 1, failed: 3 },
      errors: { data: [{ message: "the input file held a line for another endpoint" }] },
    },
  ];

  mount();

  const reason = await screen.findByTestId("batch-failure");
  expect(reason.textContent).toContain("another endpoint");
  expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();

  const createObjectURL = vi.fn(() => "blob:starport/errors");
  const revokeObjectURL = vi.fn();
  Object.assign(URL, { createObjectURL, revokeObjectURL });
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

  fireEvent.click(screen.getByRole("button", { name: "Error file" }));

  await waitFor(() => expect(gateway.fetched).toEqual(["file_err"]));
  await waitFor(() => expect(click).toHaveBeenCalledTimes(1));
  expect(createObjectURL).toHaveBeenCalledTimes(1);
  expect(revokeObjectURL).toHaveBeenCalledWith("blob:starport/errors");
  click.mockRestore();
});

test("cancels a running batch only after the operator confirms", async () => {
  gateway.batches = [
    {
      id: "batch_running",
      endpoint: "/v1/chat/completions",
      input_file_id: "file_in",
      status: "validating",
      created_at: nowSeconds() - 5,
      request_counts: { total: 3, completed: 0, failed: 0 },
    },
  ];

  mount();

  fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
  const dialog = await screen.findByRole("dialog", { name: "Cancel batch" });
  expect(within(dialog).getByText("batch_running")).toBeTruthy();
  expect(gateway.cancelled).toEqual([]);

  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel batch" }));

  await waitFor(() => expect(gateway.cancelled).toEqual(["batch_running"]));
});
