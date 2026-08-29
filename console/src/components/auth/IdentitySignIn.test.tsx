// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { IdentitySignIn, providerLabel } from "./IdentitySignIn";

// The component's one input is the gateway's provider list; the stub is that
// list. Everything else it does — draw or stay silent, and where each anchor
// points — follows from it.
const gateway = vi.hoisted(() => ({ providers: [] as string[] }));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    identityProviders: async () => gateway.providers,
  };
});

afterEach(cleanup);

test("a configured provider becomes a navigation to its dance", async () => {
  gateway.providers = ["github", "google"];
  render(<IdentitySignIn />);

  const google = await screen.findByRole("link", { name: "Sign in with Google" });
  expect(google.getAttribute("href")).toBe("/console/identity/google");
  const github = screen.getByRole("link", { name: "Sign in with GitHub" });
  expect(github.getAttribute("href")).toBe("/console/identity/github");
});

test("an unconfigured deployment draws nothing", async () => {
  gateway.providers = [];
  const { container } = render(<IdentitySignIn />);

  // Flush the provider fetch so the assertion covers the settled state, not
  // just the first frame.
  await act(async () => {});
  expect(container.innerHTML).toBe("");
});

test("a provider outside the label map still renders", () => {
  expect(providerLabel("google")).toBe("Google");
  expect(providerLabel("github")).toBe("GitHub");
  expect(providerLabel("okta")).toBe("Okta");
});
