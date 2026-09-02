import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";

import { RelativeTime } from "./RelativeTime";

test("RelativeTime keeps the phrase on one line and names the UTC instant", () => {
  const iso = new Date(Date.now() - 5 * 60 * 1000).toISOString();
  render(<RelativeTime iso={iso} />);
  const element = screen.getByText("5m ago");
  expect(element.tagName).toBe("TIME");
  expect(element.className).toContain("whitespace-nowrap");
  expect(element.getAttribute("title")).toBe(iso);
  expect(element.getAttribute("datetime")).toBe(iso);
});

test("RelativeTime renders an absent stamp as a dash with no tooltip", () => {
  render(<RelativeTime iso="0001-01-01T00:00:00Z" />);
  const element = screen.getByText("—");
  expect(element.getAttribute("title")).toBeNull();
  expect(element.getAttribute("datetime")).toBeNull();
});
