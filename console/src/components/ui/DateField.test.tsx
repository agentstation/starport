// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, test } from "vitest";

import { DateField, formatIsoDay, parseIsoDay } from "./DateField";

afterEach(cleanup);

function Harness({ initial = "", clearable = true }: { initial?: string; clearable?: boolean }) {
  const [value, setValue] = useState(initial);
  return (
    <>
      <DateField value={value} onChange={setValue} placeholder="Never" clearable={clearable} />
      <output data-testid="value">{value}</output>
    </>
  );
}

test("the ISO day round-trips without a time zone shift", () => {
  const day = parseIsoDay("2026-03-01");
  expect(day?.getFullYear()).toBe(2026);
  expect(day?.getMonth()).toBe(2);
  expect(day?.getDate()).toBe(1);
  expect(formatIsoDay(day!)).toBe("2026-03-01");
  expect(parseIsoDay("")).toBeUndefined();
});

test("the trigger reads the chosen day, opens the month grid, and a pick closes it", async () => {
  render(<Harness initial="2026-09-30" />);
  const trigger = screen.getByRole("button", { name: /Sep 30, 2026/ });
  // The month grid is a lazy chunk, so the test loads the module first and
  // lets React settle the resolved import before it reads the grid.
  await import("./calendar");
  fireEvent.click(trigger);
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 50));
  });

  const grid = await screen.findByRole("grid", { name: "September 2026" });
  const day = screen
    .getAllByRole("gridcell")
    .find((cell) => cell.textContent === "15" && !cell.hasAttribute("data-outside"));
  fireEvent.click(day!.querySelector("button")!);
  expect(screen.getByTestId("value").textContent).toBe("2026-09-15");
  expect(grid.isConnected).toBe(false);
});

test("a locked field offers no clear control", () => {
  render(<Harness initial="2026-09-30" clearable={false} />);
  expect(screen.queryByRole("button", { name: "Clear date" })).toBeNull();
  cleanup();
  render(<Harness initial="2026-09-30" />);
  fireEvent.click(screen.getByRole("button", { name: "Clear date" }));
  expect(screen.getByTestId("value").textContent).toBe("");
  expect(screen.getByRole("button", { name: /Never/ })).toBeTruthy();
});
