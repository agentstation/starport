import { cleanup, render } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";

import { Sparkline } from "@/components/overview/Sparkline";

afterEach(cleanup);

test("a flat series still draws its zero baseline", () => {
  const { container } = render(<Sparkline points={[0, 0, 0, 0]} />);
  expect(container.querySelector('[data-testid="sparkline-baseline"]')).toBeTruthy();
  expect(container.querySelector("polyline")?.getAttribute("points")).toBe("0.0,18.0 24.0,18.0 48.0,18.0 72.0,18.0");
});

test("a shared max scales the series against another one", () => {
  const own = render(<Sparkline points={[0, 4]} />);
  const shared = render(<Sparkline points={[0, 4]} max={8} />);
  const ownPoints = own.container.querySelector("polyline")?.getAttribute("points") ?? "";
  const sharedPoints = shared.container.querySelector("polyline")?.getAttribute("points") ?? "";
  expect(ownPoints).not.toBe(sharedPoints);
  const ownTop = Number(ownPoints.split(" ").at(-1)?.split(",")[1]);
  const sharedTop = Number(sharedPoints.split(" ").at(-1)?.split(",")[1]);
  expect(sharedTop).toBeGreaterThan(ownTop);
});
