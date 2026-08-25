// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";

import type { ActivityRecord } from "@/lib/api";

import { ServedCredentialPanel } from "./ServedCredentialPanel";

afterEach(cleanup);

// rowText reads each row as "<plane> <count>". The two are separate elements
// so that the count can be tabular, which is why they are joined here rather
// than read off textContent.
function rowText(): string[] {
  return screen
    .getAllByRole("listitem")
    .map((row) =>
      [...row.querySelectorAll("span")]
        .map((span) => span.textContent?.trim())
        .join(" "),
    );
}

function record(source: string | undefined): ActivityRecord {
  return {
    timestamp: "2026-08-25T00:00:00Z",
    provider: "acme",
    status: "ok",
    credential_source: source,
  };
}

// The panel's whole job is to separate planes an operator would otherwise
// read as one number. A window in which the deployment's own credential and
// an account's BYOK both served has to show both, with their counts, or the
// screen says "requests happened" and nothing about who paid for them.
test("a window served by more than one plane names each one and its count", () => {
  render(
    <ServedCredentialPanel
      records={[
        record("gateway"),
        record("gateway"),
        record("byok"),
        record("environment"),
      ]}
    />,
  );

  const rows = rowText();
  // Environment first, then gateway, then BYOK: the order the gateway itself
  // reaches the planes, so the list reads as the fallback chain.
  expect(rows).toEqual(["Environment 1", "Gateway credential 2", "BYOK 1"]);
});

// A record written before the gateway recorded planes must not be folded into
// one, because attributing spend to a plane that may not have paid is worse
// than admitting the record predates the field.
test("a record with no source is counted as unrecorded, not as a plane", () => {
  render(<ServedCredentialPanel records={[record("byok"), record(undefined)]} />);

  const rows = rowText();
  expect(rows).toEqual(["BYOK 1", "Unrecorded 1"]);
});

test("an empty window says so instead of showing a zeroed breakdown", () => {
  render(<ServedCredentialPanel records={[]} />);

  expect(screen.queryAllByRole("listitem")).toHaveLength(0);
  expect(
    screen.getByText(/No requests through this provider in the last hour/),
  ).toBeTruthy();
});
