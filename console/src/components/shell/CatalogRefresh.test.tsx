// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { CatalogAdminRead } from "@/components/shell/CatalogSummary";
import type { CatalogOperation } from "@/lib/api";
import { json, resetGateway, stubGateway } from "@/test/console";

import { CatalogRefresh, PIN_POINTER, REFRESH_EFFECT, runFacts } from "./CatalogRefresh";

afterEach(resetGateway);

const RUN = "0f6c2a9e-1111-4222-8333-444455556666";

// gateway answers the refresh routes in order and records each method.
function gateway(start: Response, reads: Response[], cancel?: Response) {
  const calls: string[] = [];
  stubGateway({});
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://localhost").pathname;
      const method = init?.method ?? "GET";
      calls.push(`${method} ${path}`);
      if (path === "/api/v1/admin/catalog/refresh") return start.clone();
      if (path === `/api/v1/admin/catalog/refreshes/${RUN}`) {
        if (method === "DELETE" && cancel) {
          // A closed run reads as closed from then on.
          reads.splice(0, reads.length, cancel);
          return cancel.clone();
        }
        return (reads.length > 1 ? reads.shift() : reads[0])?.clone() ?? json({}, 404);
      }
      return json({});
    }),
  );
  return calls;
}

function mount(working?: CatalogOperation) {
  const admin: CatalogAdminRead = { status: {}, admin: true, working };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <CatalogRefresh admin={admin} />
    </QueryClientProvider>,
  );
}

describe("catalog refresh", () => {
  it("names one action and its effect before the click", () => {
    gateway(json({}), []);
    mount();

    expect(screen.getByRole("button", { name: "Refresh sources" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Refresh catalog" })).toBeNull();
    expect(screen.getByText(REFRESH_EFFECT)).toBeTruthy();
    expect(screen.getByText(PIN_POINTER)).toBeTruthy();
  });

  it("follows the started run from queued to done through the run route", async () => {
    const calls = gateway(json({ id: RUN, state: "accepted", joined: false }, 202), [
      json({ id: RUN, state: "accepted" }),
      json({
        id: RUN,
        state: "succeeded",
        changed: true,
        generation_id: "01J9ABCDEFGHJKMNPQRSTVWXYZ",
        permission_at_completion: { new_attempts_allowed: true, admitted_streams_may_finish: true },
      }),
    ]);
    mount();

    fireEvent.click(screen.getByRole("button", { name: "Refresh sources" }));
    await screen.findByText("Queued");
    expect((screen.getByRole("button", { name: "Refresh sources" }) as HTMLButtonElement).disabled).toBe(true);
    await screen.findByText("Done", undefined, { timeout: 5000 });
    expect(screen.getByText(/The run accepted generation/)).toBeTruthy();
    expect(screen.getByText("At completion the catalog admitted new requests, and admitted streams could finish.")).toBeTruthy();
    expect(calls).toContain(`GET /api/v1/admin/catalog/refreshes/${RUN}`);
  });

  it("says that a request joined the run in flight", async () => {
    gateway(json({ id: RUN, state: "running", joined: true }, 202), [json({ id: RUN, state: "running" })]);
    mount();

    fireEvent.click(screen.getByRole("button", { name: "Refresh sources" }));
    await screen.findByText("Running");
    expect(screen.getByText("This request joined the run in flight. Overlapping requests share one run.")).toBeTruthy();
  });

  it("follows a scheduled run in flight and cancels it", async () => {
    const calls = gateway(json({}), [json({ id: RUN, state: "running" })], json({ id: RUN, state: "canceled", reason: "canceled" }));
    mount({ id: RUN, state: "running" });

    await screen.findByText("Running");
    fireEvent.click(screen.getByRole("button", { name: "Cancel refresh" }));
    await waitFor(() => expect(calls).toContain(`DELETE /api/v1/admin/catalog/refreshes/${RUN}`));
    await screen.findByText("Canceled");
    expect(screen.getByText("Cause: an operator or a shutdown ended the run.")).toBeTruthy();
  });

  it("says when the gateway no longer keeps the run", async () => {
    gateway(json({}), [json({ error: { message: "catalog operation is not found" } }, 404)]);
    mount({ id: RUN, state: "running" });

    await screen.findByText("The gateway no longer keeps this run.");
  });

  it("names the cause and the admission of a failed run", () => {
    expect(
      runFacts({
        id: RUN,
        state: "failed",
        reason: "runtime_generation_capacity",
        permission_at_completion: { new_attempts_allowed: false, admitted_streams_may_finish: true },
      }),
    ).toEqual([
      "Cause: retained requests delay the activation of a new generation.",
      "At completion the catalog did not admit new requests, and admitted streams could finish.",
    ]);
    expect(runFacts({ id: RUN, state: "succeeded", changed: false })).toEqual([
      "The sources had no change. The current catalog stays.",
    ]);
  });
});
