// @vitest-environment jsdom
import { screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test } from "vitest";

import { openConsole, resetGateway, stubGateway } from "@/test/console";

// The one model the stubbed gateway knows. Its id holds a slash, which is
// the shape every catalog id has and the shape a path segment cannot hold
// unencoded.
const MODEL = {
  id: "anthropic/claude-sonnet-5",
  name: "Claude Sonnet 5",
  context_length: 200_000,
  authors: [],
};

const answers = {
  "/api/v1/models": { data: [MODEL] },
  "/api/v1/admin/providers": { providers: [] },
};

beforeEach(() => stubGateway(answers));
afterEach(resetGateway);

// An unencoded id typed into the address bar spans two segments and matches
// no route. The page names the list the reader wanted and links to it,
// rather than the router's bare default.
test("an address the router cannot match names the missing model and links to the list", async () => {
  openConsole("/models/unknown/unknown");

  expect(await screen.findByRole("heading", { name: "No model at this address" })).toBeTruthy();
  expect(screen.getByText("unknown/unknown")).toBeTruthy();
  const back = screen
    .getAllByRole("link", { name: "Models" })
    .find((link) => !link.closest("nav"));
  expect(back?.getAttribute("href")).toBe("/models");
});

// The detail route matches, and its loader learns from the catalog that the
// id names nothing. That is the same page, reached through notFound().
test("a model the catalog does not hold renders the not-found page", async () => {
  openConsole("/models/nope");

  expect(await screen.findByRole("heading", { name: "No model at this address" })).toBeTruthy();
  expect(screen.getByText("nope")).toBeTruthy();
});

// The sidebar matches by prefix, so a reader on a detail page still sees
// which list they are in. The overview matches exactly, or it would light
// up everywhere.
test("the models link stays active on a model detail route", async () => {
  openConsole("/models/anthropic%2Fclaude-sonnet-5");

  await screen.findByRole("heading", { name: "Claude Sonnet 5" });
  const nav = screen.getByRole("navigation", { name: "Console" });
  expect(within(nav).getByRole("link", { name: "Models" }).getAttribute("aria-current")).toBe(
    "page",
  );
  expect(within(nav).getByRole("link", { name: "Overview" }).getAttribute("aria-current")).toBeNull();
});

// The router encodes a param into one segment and decodes it on the way
// back, so an id with a slash survives a navigation, the address bar, and
// the route that reads it.
test("a model id with a slash round-trips through the address as one segment", async () => {
  const router = openConsole("/models");
  await screen.findByRole("navigation", { name: "Console" });

  await router.navigate({ to: "/models/$modelId", params: { modelId: MODEL.id } });

  await waitFor(() =>
    expect(router.state.location.pathname).toBe("/models/anthropic%2Fclaude-sonnet-5"),
  );
  expect(await screen.findByRole("heading", { name: "Claude Sonnet 5" })).toBeTruthy();
  expect(screen.getByTitle("Copy model ID").textContent).toContain("anthropic/claude-sonnet-5");
});
