import { expect, test } from "vitest";

import { destination } from "./destination";

// `next` arrives in a URL, so it is whatever the last link said. A console that
// honoured a full URL would be a redirector: follow a link, open your own
// gateway, and land on somebody else's page having just proved you trust this
// one. Every rejected form falls back to the overview rather than failing.
test("only a path on this gateway is honoured as a destination", () => {
  expect(destination("/usage")).toBe("/usage");
  expect(destination("/models?q=llama#top")).toBe("/models?q=llama#top");

  expect(destination(undefined)).toBe("/");
  expect(destination("https://example.com/steal")).toBe("/");
  expect(destination("//example.com/steal")).toBe("/");
  expect(destination("javascript:alert(1)")).toBe("/");
  // Sending a reader who just opened a session back to the page that opens one
  // is a loop, not a destination.
  expect(destination("/auth?next=/auth")).toBe("/");
});
