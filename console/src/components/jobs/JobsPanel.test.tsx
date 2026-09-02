// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
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
    // A fetch here would mean the panel decided to play something. Every test
    // in this file asserts it did not, so a call is a failure rather than a
    // fixture.
    fetchJobAsset: async () => {
      throw new Error("no test in this file plays an asset");
    },
  };
});

const HOUR = 3600;

function nowSeconds(): number {
  return Math.floor(Date.now() / 1000);
}

beforeEach(() => {
  gateway.jobs = [];
  gateway.models = [];
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
