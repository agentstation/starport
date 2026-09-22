// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, expect, test, vi } from "vitest";

import type { Model, ProviderRuntimeStatus } from "@/lib/api";

import { credentialText, OfferingTable } from "./ModelDetail";

// A model offering carries four unlike facts, and a reader acts on each one
// differently. The catalog says where the offering stands in its life and
// whether the provider still publishes it. The credential says whether this
// deployment can pay for it at all. The catalog generation says where the
// facts came from. The router says whether it plans a route to it now. One
// cell that mixed two of these would send a reader to the wrong repair.

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { to: string; params?: unknown; children?: ReactNode }) => (
    <a href="#">{children}</a>
  ),
}));

afterEach(cleanup);

const model: Model = {
  id: "openai/gpt-x",
  offerings: [
    {
      provider: "openai",
      provider_model_id: "gpt-x",
      lifecycle: "deprecated",
      availability: "available",
    },
  ],
};

const providers: ProviderRuntimeStatus[] = [
  {
    provider_id: "openai",
    operator_credential: { state: "missing", usable: false },
    offerings: [
      {
        provider_model_id: "gpt-x",
        state: "open",
        routing: { state: "excluded", reason: "circuit_open" },
      },
    ],
  },
];

test("lifecycle, availability, credential, circuit, and routing are five elements", () => {
  render(<OfferingTable model={model} providers={providers} generation="01J9ABCDEFGHJKMNPQRS" />);

  expect(screen.getByTestId("offering-lifecycle").textContent).toBe("deprecated");
  expect(screen.getByTestId("offering-availability").textContent).toBe("available");
  expect(screen.getByTestId("offering-credential").textContent).toBe("missing");
  expect(screen.getByTestId("offering-circuit").textContent).toBe("open");
  expect(screen.getByTestId("offering-routing").textContent).toBe("excluded");
});

test("the model detail names the provenance of the offering facts", () => {
  render(<OfferingTable model={model} providers={providers} generation="01J9ABCDEFGHJKMNPQRS" />);

  expect(screen.getByTestId("offering-provenance").textContent).toBe(
    "These offerings come from catalog generation 01J9ABCDEFGHJKMNPQRS.",
  );
});

test("an unreported generation says so rather than leaving the provenance blank", () => {
  render(<OfferingTable model={model} providers={providers} />);

  expect(screen.getByTestId("offering-provenance").textContent).toBe(
    "The catalog generation behind these offerings is not reported.",
  );
});

test("availability never borrows the circuit cell when no runtime state exists", () => {
  render(<OfferingTable model={model} providers={[]} />);

  // The circuit is unknown, and an unknown circuit is not an availability.
  expect(screen.getByTestId("offering-circuit").textContent).toBe("—");
  expect(screen.getByTestId("offering-availability").textContent).toBe("available");
  expect(screen.getByTestId("offering-credential").textContent).toBe("none");
  expect(screen.getByTestId("offering-routing").textContent).toBe("—");
});

test("availability is credential-specific", () => {
  expect(credentialText(undefined)).toBe("none");
  expect(credentialText({ state: "missing", usable: false })).toBe("missing");
  expect(credentialText({ state: "configured", usable: true })).toBe("usable");
  expect(credentialText({ usable: false })).toBe("unusable");
});
