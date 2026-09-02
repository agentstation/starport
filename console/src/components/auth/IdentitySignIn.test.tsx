// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { IdentitySignIn, providerLabel } from "./IdentitySignIn";

// The component's one input is the gateway's provider list; the stub is that
// list. Everything else it does — draw or stay silent, and where each anchor
// points — follows from it.
const gateway = vi.hoisted(() => ({ providers: [] as string[], failures: 0 }));

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return {
    ...actual,
    identityProviders: async () => {
      if (gateway.failures > 0) {
        gateway.failures -= 1;
        throw new Error("gateway unreachable");
      }
      return gateway.providers;
    },
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

test("a failed provider read shows a failure with a retry, not silence", async () => {
  gateway.providers = ["github"];
  gateway.failures = 1;
  render(<IdentitySignIn />);

  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("Could not load sign-in options");
  expect(alert.textContent).toContain("gateway unreachable");

  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await screen.findByRole("link", { name: "Sign in with GitHub" });
  expect(screen.queryByRole("alert")).toBeNull();
});

test("a provider outside the label map still renders", () => {
  expect(providerLabel("google")).toBe("Google");
  expect(providerLabel("github")).toBe("GitHub");
  expect(providerLabel("workos")).toBe("SSO");
  expect(providerLabel("okta")).toBe("Okta");
});
