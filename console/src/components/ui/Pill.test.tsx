// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";

import { Pill } from "./Pill";

// A pill reports lifecycle. Every state chip in the console shares this one
// recipe, so a drift here changes how a reader learns every state at once.

afterEach(cleanup);

test("a pill carries the tint of its tone, a full radius, and the label", () => {
  render(<Pill tone="success">processed</Pill>);

  const pill = screen.getByText("processed");
  const classes = pill.getAttribute("class") ?? "";
  expect(classes).toContain("rounded-full");
  expect(classes).toContain("bg-success-tint");
  expect(classes).toContain("text-success");
});

test("a neutral pill reads as a deliberate state rather than a fault", () => {
  render(<Pill tone="neutral">queued</Pill>);

  const classes = screen.getByText("queued").getAttribute("class") ?? "";
  expect(classes).toContain("bg-bg-raised");
  expect(classes).not.toContain("text-error");
});
