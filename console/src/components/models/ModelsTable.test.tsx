// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, test, vi } from "vitest";

import type { Model } from "@/lib/api";

import { ModelsTable } from "./ModelsTable";

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
