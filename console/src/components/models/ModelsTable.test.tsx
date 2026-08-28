// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, test, vi } from "vitest";

import type { Model } from "@/lib/api";

import { ModelsTable, operationLabel } from "./ModelsTable";

const navigate = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
  Link: ({
    to,
    params,
    children,
    ...rest
  }: {
    to: string;
    params?: Record<string, string>;
    children?: ReactNode;
  } & Record<string, unknown>) => {
    const path = Object.entries(params ?? {}).reduce(
      (resolved, [key, value]) => resolved.replace(`$${key}`, value),
      to,
    );
    return (
      <a href={path} {...rest}>
        {children}
      </a>
    );
  },
}));

afterEach(() => {
  cleanup();
  navigate.mockClear();
});

function catalog(count: number): Model[] {
  return Array.from({ length: count }, (_, index) => ({
    id: `author-${index % 20}/model-${index}`,
    name: `Model ${index}`,
    context_length: 131072,
    pricing: { prompt: "0.000001", completion: "0.000002" },
  }));
}

test("virtualizes the catalog: 422 models render a bounded number of rows", () => {
  const started = performance.now();
  render(<ModelsTable models={catalog(422)} />);
  const elapsed = performance.now() - started;

  const rows = document.querySelectorAll('[role="row"]');
  // Header row plus the visible window and overscan — never all 422.
  expect(rows.length).toBeGreaterThan(1);
  expect(rows.length).toBeLessThan(100);
  expect(
    document.querySelector('[role="table"]')?.getAttribute("aria-rowcount"),
  ).toBe("422");
  // Frame-budget smoke check: a virtualized render of the full catalog
  // stays well under a second even in jsdom.
  expect(elapsed).toBeLessThan(1000);
});

test("the model id links to the detail page", () => {
  render(<ModelsTable models={catalog(3)} />);

  const link = screen.getByText("author-0/model-0").closest("a");
  expect(link?.getAttribute("href")).toBe(
    "/models/$modelId".replace("$modelId", "author-0/model-0"),
  );
});

// RNK-V18. A rerank model answers no chat turn and reaches its own route, so a
// reader who cannot see which operation a model serves cannot tell it apart
// from a chat model that happens to be priced oddly.
const operationCatalog: Model[] = [
  {
    id: "cohere/rerank-v3.5",
    name: "Rerank 3.5",
    offerings: [
      {
        provider: "cohere",
        provider_model_id: "rerank-v3.5",
        operations: ["rerank"],
      },
    ],
  },
  {
    id: "meta/llama-3.1-8b-instruct",
    name: "Llama 3.1 8B",
    offerings: [
      {
        provider: "groq",
        provider_model_id: "llama-3.1-8b-instant",
        operations: ["chat-completions"],
      },
    ],
  },
];

test("the operations column names what each model serves", () => {
  render(<ModelsTable models={operationCatalog} />);

  const rerank = screen.getByText("cohere/rerank-v3.5").closest('[role="row"]');
  expect(rerank?.textContent).toContain("rerank");

  // The column reads every offering rather than the first, and it does not
  // hand one model's operation to another.
  const chat = screen.getByText("meta/llama-3.1-8b-instruct").closest('[role="row"]');
  expect(chat?.textContent).toContain("chat completions");
  expect(chat?.textContent).not.toContain("rerank");
});

// The catalog gains operations this console has not been rewritten for. A label
// table would render such a model with no operation at all, which reads as a
// model that serves nothing rather than one this console has not learned.
test("an operation this console has never seen renders under its catalog name", () => {
  render(
    <ModelsTable
      models={[
        {
          id: "acme/futurist",
          offerings: [
            {
              provider: "acme",
              provider_model_id: "futurist",
              operations: ["holograms-projections"],
            },
          ],
        },
      ]}
    />,
  );

  const row = screen.getByText("acme/futurist").closest('[role="row"]');
  expect(row?.textContent).toContain("holograms projections");
});

// Shortening an operation to its last word would render images-generations and
// videos-generations as one badge, which is the same failure as the fixed list
// above with a different cause.
test("two operations that end in the same word stay apart", () => {
  expect(operationLabel("images-generations")).not.toBe(
    operationLabel("videos-generations"),
  );
});
