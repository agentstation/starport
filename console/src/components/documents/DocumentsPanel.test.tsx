// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { DocumentsPanel } from "./DocumentsPanel";

// PLG-V18. A page charge that appears in no view reads as an unexplained spend
// increase. Three facts have to survive to this screen, and each one fails
// silently on its own: the engine that read the document, the pages it read,
// and what those pages cost. A record that shows pages and no price cannot tell
// a free native read from a paid recognition pass, which is the one question
// this page exists to answer.

const gateway = vi.hoisted(() => ({
  records: [] as unknown[],
  models: [] as unknown[],
}));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    hasCredential: () => true,
    isCredentialRejected: () => false,
    onCredentialChange: () => () => {},
    listAdminActivity: async () => ({ data: gateway.records }),
    listActivity: async () => ({ data: gateway.records }),
    listModels: async () => gateway.models,
  };
});

const RECOGNIZED = {
  request_id: "req-ocr",
  timestamp: "2026-08-27T18:00:00Z",
  model_used: "google/gemini-2.5-flash",
  parser_engine: "recognition",
  document_pages: 12,
  recognized_pages: 12,
  extraction_millis: 3400,
  extraction_cost: { nano_usd: 1_200_000_000, currency: "USD" },
  cost: { nano_usd: 1_240_000_000, currency: "USD" },
};

const CACHED = {
  request_id: "req-cached",
  timestamp: "2026-08-27T18:05:00Z",
  model_used: "google/gemini-2.5-flash",
  parser_engine: "recognition",
  document_pages: 12,
  recognized_pages: 0,
  extraction_cached: true,
  extraction_millis: 2,
  cost: { nano_usd: 40_000_000, currency: "USD" },
};

const OCR_MODEL = {
  id: "mistral/mistral-ocr",
  offerings: [
    {
      provider: "mistral",
      provider_model_id: "mistral-ocr-2505",
      operations: ["documents-recognition"],
      pricing: { page_input: "0.001", currency: "USD" },
    },
  ],
};

beforeEach(() => {
  gateway.records = [];
  gateway.models = [];
});

afterEach(cleanup);

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <DocumentsPanel />
    </QueryClientProvider>,
  );
}

// PLG-V18, first statement. The engine, the page count, and the cost are three
// separate facts, and the reader needs all three together: 12 pages at no
// charge and 12 pages at $1.20 are the same row without the price.
test("a recognized request renders its engine, its pages, and its cost", async () => {
  gateway.records = [RECOGNIZED];
  mount();

  await waitFor(() => screen.getByTestId("document-row"));
  expect(screen.getByTestId("document-engine").textContent).toBe("recognition");
  expect(screen.getByTestId("document-pages").textContent).toContain("12");
  expect(screen.getByTestId("document-pages").textContent).toContain(
    "12 recognized",
  );
  // The extraction share alone, not the turn total: the point of the column is
  // to separate what reading the document cost from what answering cost.
  expect(screen.getByTestId("document-cost").textContent).toContain("$1.20");
  expect(screen.getByTestId("document-cost").textContent).not.toContain("$1.24");
});

// PLG-V18, second statement. A cache hit costs nothing, and so does a native
// read. Rendering both as a blank cost cell would credit the in-process engine
// for work a recognition model was already paid for once.
test("a cache hit renders as a saving rather than as an empty cost", async () => {
  gateway.records = [CACHED];
  mount();

  await waitFor(() => screen.getByTestId("document-row"));
  const cached = screen.getByTestId("document-cached");
  expect(cached.textContent).toContain("cached");
  expect(cached.textContent).toContain("no charge");
  expect(screen.getByTestId("document-pages").textContent).toContain("12");
});

// A native read is free because no provider saw the pages. Saying so is a
// different answer from saying nothing, which is what an unpriced page gets.
test("a natively read document says the pages cost nothing", async () => {
  gateway.records = [
    {
      ...RECOGNIZED,
      request_id: "req-native",
      parser_engine: "native",
      recognized_pages: 0,
      native_pages: 12,
      extraction_cost: undefined,
    },
  ];
  mount();

  await waitFor(() => screen.getByTestId("document-row"));
  expect(screen.getByTestId("document-engine").textContent).toBe("native");
  expect(screen.getByTestId("document-cost").textContent).toContain("free");
  expect(screen.getByTestId("document-pages").textContent).toContain(
    "read in process",
  );
});

// The projection drops a recognition offering that publishes no page price, so
// a record with recognized pages and no cost means the gateway lost its
// catalog. A zero there would understate a real charge.
test("a page the gateway could not price says so rather than showing nothing", async () => {
  gateway.records = [
    {
      ...RECOGNIZED,
      request_id: "req-unpriced",
      extraction_cost: undefined,
      cost: undefined,
      cost_unavailable_reason: "no_pricing",
    },
  ];
  mount();

  await waitFor(() => screen.getByTestId("document-row"));
  expect(screen.getByTestId("document-cost").textContent).toContain("unpriced");
  expect(screen.getByTestId("document-cost").textContent).toContain(
    "no_pricing",
  );
});

// An ordinary chat turn read no document. Listing it here with zero pages
// would bury the document turns among every other request the gateway served.
test("a request that attached nothing is not a document read", async () => {
  gateway.records = [
    { request_id: "req-chat", timestamp: "2026-08-27T18:01:00Z" },
  ];
  mount();

  await waitFor(() => screen.getByText(/attached a document/));
  expect(screen.queryByTestId("document-row")).toBeNull();
});

// The price a reader plans against comes from the catalog, not from this
// console. A deployment that serves no recognition has to say so, or a reader
// takes the empty list for a page that costs nothing. The price renders per
// 1K pages so a sub-cent page price stays readable ($1, not 0.001).
test("the catalogued recognition models render with their page price", async () => {
  gateway.models = [OCR_MODEL];
  mount();

  await waitFor(() => screen.getByTestId("recognition-row"));
  const row = screen.getByTestId("recognition-row");
  expect(row.textContent).toContain("mistral/mistral-ocr");
  expect(row.textContent).toContain("mistral-ocr-2505");
  expect(row.textContent).toContain("$1 USD");
  expect(screen.getByText("Per 1K pages")).toBeTruthy();
});

test("a catalog that serves no recognition says so", async () => {
  gateway.models = [
    {
      id: "openai/gpt-4.1",
      offerings: [
        {
          provider: "openai",
          provider_model_id: "gpt-4.1",
          operations: ["chat-completions"],
        },
      ],
    },
  ];
  mount();

  const notice = await waitFor(() => screen.getByTestId("recognition-models"));
  expect(notice.textContent).toContain("No provider in this catalog reads");
  expect(screen.queryByTestId("recognition-row")).toBeNull();
});
