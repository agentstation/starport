// @vitest-environment jsdom
import { screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, test } from "vitest";

import { openConsole, resetGateway, stubGateway } from "@/test/console";

beforeEach(() => stubGateway({ "/api/v1/models": { data: [] } }));
afterEach(resetGateway);

const FOCUSABLE = 'a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])';

// A keyboard reader's first Tab lands on the skip link, which jumps past the
// sidebar to the main landmark.
test("the skip link is the first focusable element and targets the main landmark", async () => {
  openConsole("/models");

  const link = await screen.findByRole("link", { name: "Skip to content" });
  expect(document.querySelector(FOCUSABLE)).toBe(link);
  expect(link.getAttribute("href")).toBe("#main");
  expect(document.getElementById("main")?.tagName).toBe("MAIN");
});
