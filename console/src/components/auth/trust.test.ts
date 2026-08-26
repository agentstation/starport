import { expect, test } from "vitest";

import { trustScope } from "./trust";

// The readout is the one claim on this page that is about the reader's own
// safety, so it has to be derived rather than written. These cases are the
// reason: the same page is served on a loopback address to a developer and on a
// routable one to whoever the operator exposed it to, and only the first can
// honestly say the port is unreachable from anywhere else.
test("a loopback address is the only thing reported as local", () => {
  for (const host of ["localhost", "127.0.0.1", "::1", "[::1]"]) {
    const scope = trustScope(host, false);
    expect(scope.local).toBe(true);
    expect(scope.label).toBe(`Local-only · ${host}`);
  }
});

test("a routable address says so, and names the address it says it about", () => {
  const scope = trustScope("gateway.internal", true);

  expect(scope.local).toBe(false);
  expect(scope.label).toBe("Network · gateway.internal");
  expect(scope.detail).toMatch(/admin credential/i);
});

// A hostname that merely resolves to loopback is not loopback as far as this
// page is concerned: it cannot resolve anything, and a readout that guessed
// would be asserting more than it knows. Reporting `starport.local` as local
// would be exactly the wrong error — it is the case where the reader is about
// to paste another machine's admin token over a hop they do not control.
test("a name that is not literally loopback is not treated as loopback", () => {
  expect(trustScope("starport.local", false).local).toBe(false);
  expect(trustScope("127.0.0.1.example.com", false).local).toBe(false);
});

// An unencrypted routable address is the worst of the three and has to read
// that way, because the token crosses the network in the clear.
test("an unencrypted network address is called out separately", () => {
  expect(trustScope("10.0.0.5", false).label).toBe("Network · 10.0.0.5 · not encrypted");
  expect(trustScope("10.0.0.5", true).label).toBe("Network · 10.0.0.5");
});
