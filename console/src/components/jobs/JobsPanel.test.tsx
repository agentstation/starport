// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { JobsPanel } from "./JobsPanel";

// AMJ-V17. A job is the one answer this console reads more than once, and the
// two states a reader cannot act on without words are the two under test. A
// failed job that showed only the word "failed" would send a reader to the
// gateway log for the reason. A finished job whose bytes are gone would render
// a player that fetches a refusal, which reads as a broken console rather than
// as a window that closed.

const gateway = vi.hoisted(() => ({
  jobs: [] as unknown[],
  models: [] as unknown[],
  cancelled: [] as string[],
  cancelStatus: "cancelled",
  checked: [] as string[],
  check: vi.fn(),
  announce: vi.fn(),
  report: vi.fn(),
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    hasCredential: () => true,
    isCredentialRejected: () => false,
    onCredentialChange: () => () => {},
    listJobs: async () => ({ jobs: gateway.jobs, capped: false }),
    listModels: async () => gateway.models,
    cancelJob: async (jobID: string) => {
      gateway.cancelled.push(jobID);
      return { id: jobID, status: gateway.cancelStatus };
    },
    reconcileJob: async (jobID: string) => {
      gateway.checked.push(jobID);
      return gateway.check(jobID);
    },
    // A fetch here would mean the panel decided to play something. Every test
    // in this file asserts it did not, so a call is a failure rather than a
    // fixture.
    fetchJobAsset: async () => {
      throw new Error("no test in this file plays an asset");
    },
  };
});

vi.mock("@/lib/mutations", () => ({ announce: gateway.announce, report: gateway.report }));

const HOUR = 3600;

function nowSeconds(): number {
  return Math.floor(Date.now() / 1000);
}

beforeEach(() => {
  gateway.jobs = [];
  gateway.models = [];
  gateway.cancelled = [];
  gateway.cancelStatus = "cancelled";
  gateway.checked = [];
  gateway.check.mockReset();
  gateway.announce.mockReset();
  gateway.report.mockReset();
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <JobsPanel />
    </QueryClientProvider>,
  );
}

test("a failed job names the reason the provider gave", async () => {
  gateway.jobs = [
    {
      id: "job-failed",
      model: "mock/video-1",
      status: "failed",
      created_at: nowSeconds() - 120,
      completed_at: nowSeconds() - 60,
      error: { message: "the safety filter refused the prompt" },
    },
  ];

  mount();

  const reason = await screen.findByTestId("job-failure");
  expect(reason.textContent).toContain("the safety filter refused the prompt");

  // The state word alone is not the finding. A reader who sees only "failed"
  // has to open a gateway log to learn whether to change the prompt or the
  // model.
  const row = screen.getByTestId("job-row");
  expect(row.textContent).toContain("failed");
  expect(screen.queryByTestId("job-player")).toBeNull();
});

test("an expired job shows the marker and never a player", async () => {
  gateway.jobs = [
    {
      id: "job-expired",
      model: "mock/video-1",
      status: "completed",
      created_at: nowSeconds() - 4 * HOUR,
      completed_at: nowSeconds() - 3 * HOUR,
      // The window closed. The record stays behind it, so the job still reads
      // completed and the bytes are gone.
      expires_at: nowSeconds() - HOUR,
    },
  ];

  mount();

  const marker = await screen.findByTestId("job-expired");
  expect(marker.textContent).toContain("no longer holds");

  // A player element is the defect this test exists to catch. Rendering one
  // over an expired asset would fetch a 410 and report a working gateway as
  // broken.
  expect(screen.queryByTestId("job-player")).toBeNull();
  expect(document.querySelector("video")).toBeNull();
  expect(screen.queryByText("Play")).toBeNull();
});

test("a job whose window is still open offers the video", async () => {
  gateway.jobs = [
    {
      id: "job-playable",
      model: "mock/video-1",
      status: "completed",
      created_at: nowSeconds() - 300,
      completed_at: nowSeconds() - 60,
      expires_at: nowSeconds() + 20 * HOUR,
    },
  ];

  mount();

  // The same completed state reads the other way when the window is open. This
  // is what keeps the expired test above from passing against a panel that
  // never offers a video at all.
  await waitFor(() => expect(screen.queryByTestId("job-row")).not.toBeNull());
  expect(screen.queryByTestId("job-expired")).toBeNull();
  expect(screen.getByText("Play")).toBeTruthy();
});

test("only a model that serves the operation can be submitted", async () => {
  gateway.jobs = [];
  gateway.models = [
    {
      id: "mock/video-1",
      offerings: [{ provider: "mock", provider_model_id: "video-1", operations: ["videos-generations"] }],
    },
    {
      id: "mock/chat-1",
      offerings: [{ provider: "mock", provider_model_id: "chat-1", operations: ["chat-completions"] }],
    },
  ];

  mount();

  // Offering a chat model here would let a reader submit one and read a
  // routing refusal that says nothing about the mistake. The catalog already
  // names what each offering serves.
  const chooser = (await screen.findByLabelText("Model")) as HTMLSelectElement;
  await waitFor(() => expect(chooser.options.length).toBeGreaterThan(1));
  const offered = Array.from(chooser.options).map((option) => option.value);
  expect(offered).toContain("mock/video-1");
  expect(offered).not.toContain("mock/chat-1");
});

// A state is a lifecycle fact, and DESIGN.md renders lifecycle as a pill. The
// wire state keeps its underscore; the pill does not, because a reader is not
// a parser.
test("a running job renders its state as a lifecycle pill", async () => {
  gateway.jobs = [
    {
      id: "job-running",
      model: "mock/video-1",
      status: "in_progress",
      created_at: nowSeconds() - 30,
    },
  ];

  mount();

  const pill = await screen.findByText("in progress");
  const classes = pill.getAttribute("class") ?? "";
  expect(classes).toContain("rounded-full");
  expect(classes).toContain("bg-info-tint");
  expect(screen.queryByText("in_progress")).toBeNull();
});

// A cancel ends a job the operator paid to start, so it travels only after
// the dialog that names the job confirms it.
test("cancels a running job only after the operator confirms", async () => {
  gateway.jobs = [
    {
      id: "job-running",
      model: "mock/video-1",
      status: "in_progress",
      created_at: nowSeconds() - 30,
    },
  ];
  mount();

  fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
  const dialog = await screen.findByRole("dialog", { name: "Cancel job" });
  expect(within(dialog).getByText("job-running")).toBeTruthy();
  expect(gateway.cancelled).toEqual([]);

  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel job" }));

  await waitFor(() => expect(gateway.cancelled).toEqual(["job-running"]));
});


test("a paused job requires an explicit provider check and retains its state on error", async () => {
  gateway.jobs = [{ id: "job-paused", model: "mock/video-1", status: "in_progress", created_at: nowSeconds() - 2 * HOUR, polling_status: "paused" }];
  let refuse!: (error: Error) => void;
  gateway.check.mockImplementation(() => new Promise((_resolve, reject) => { refuse = reject; }));
  mount();
  expect((await screen.findByTestId("job-polling-paused")).textContent).toContain("holds a job slot");
  expect(gateway.checked).toEqual([]);
  fireEvent.click(screen.getByRole("button", { name: "Check provider" }));
  await waitFor(() => expect(gateway.checked).toEqual(["job-paused"]));
  expect((await screen.findByRole("button", { name: "Checking…" }) as HTMLButtonElement).disabled).toBe(true);
  refuse(new Error("provider unavailable"));
  await waitFor(() => expect(gateway.report).toHaveBeenCalledWith("Provider check failed: provider unavailable"));
  expect(screen.getByText("in progress")).toBeTruthy();
  expect(screen.getByTestId("job-polling-paused")).toBeTruthy();
  expect(gateway.announce).not.toHaveBeenCalled();
  expect(gateway.cancelled).toEqual([]);
});

test("a confirmed provider check replaces the paused state", async () => {
  gateway.jobs = [{ id: "job-paused", model: "mock/video-1", status: "queued", created_at: nowSeconds() - 2 * HOUR, polling_status: "paused" }];
  gateway.check.mockImplementation(async () => {
    const result = { id: "job-paused", model: "mock/video-1", status: "failed", created_at: nowSeconds() - 2 * HOUR, error: { message: "provider stopped" } };
    gateway.jobs = [result];
    return result;
  });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Check provider" }));
  await waitFor(() => expect(gateway.announce).toHaveBeenCalledWith("Provider reports failed for job-paused"));
  expect((await screen.findByTestId("job-failure")).textContent).toContain("provider stopped");
  expect(screen.queryByTestId("job-polling-paused")).toBeNull();
  expect(screen.queryByRole("button", { name: "Check provider" })).toBeNull();
  expect(gateway.checked).toEqual(["job-paused"]);
});

test("an unconfirmed submission cannot poll an unknown provider handle", async () => {
  gateway.jobs = [{ id: "job-uncertain", model: "mock/video-1", status: "queued", created_at: nowSeconds(), submission_status: "unconfirmed" }];
  mount();
  await screen.findByTestId("job-row");
  expect(screen.queryByRole("button", { name: "Check provider" })).toBeNull();
  expect(gateway.checked).toEqual([]);
});

test("a pending cancellation reports the provider state without claiming cancellation", async () => {
  gateway.jobs = [{ id: "job-running", model: "mock/video-1", status: "in_progress", created_at: nowSeconds() }];
  gateway.cancelStatus = "in_progress";
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
  const dialog = await screen.findByRole("dialog", { name: "Cancel job" });
  expect(dialog.textContent).toContain("cancellation does not establish a refund");
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel job" }));
  await waitFor(() => expect(gateway.announce).toHaveBeenCalledWith("Provider reports in progress for job-running"));
  expect(gateway.announce).not.toHaveBeenCalledWith("Cancelled job-running");
});
