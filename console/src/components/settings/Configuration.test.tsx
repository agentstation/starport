// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { EffectiveReport, FieldSave, Receipt, SchemaSetting } from "@/lib/configuration";
import { json, resetGateway, stubGateway } from "@/test/console";

import { ConfigurationSection } from "./Configuration";

afterEach(resetGateway);

const LOADED = "a".repeat(64);
const SAVED = "b".repeat(64);
const CURRENT = "c".repeat(64);
const SECRET = "sk-live-sealed-value-0001";

function schema(key: string, extra: Partial<SchemaSetting> = {}): SchemaSetting {
  return {
    id: key.replaceAll("_", "."),
    key,
    environment: `STARPORT_${key.toUpperCase()}`,
    type: "string",
    scope: "deployment",
    mutability: "runtime-replacement",
    sensitive: false,
    applicability: ["all"],
    ...extra,
  };
}

const SCHEMA: SchemaSetting[] = [
  schema("catalog_source"),
  schema("catalog_source_url", { applicability: ["starmap", "file"] }),
  schema("catalog_source_api_key", { sensitive: true, applicability: ["starmap"] }),
  schema("catalog_source_token", { sensitive: true, applicability: ["public", "github"] }),
  schema("catalog_source_poll_interval", { type: "duration" }),
  schema("catalog_acquisition_interval", { type: "duration" }),
  schema("catalog_refresh_timeout", { type: "duration" }),
  schema("state_dir", { scope: "node", mutability: "restart" }),
];

function setting(key: string, value: string, origin = "default") {
  return {
    id: key.replaceAll("_", "."),
    name: `STARMAP_${key.toUpperCase()}`,
    value,
    scope: key === "state_dir" ? "node" : "deployment",
    authority: "local",
    origin,
  };
}

function report(extra: Partial<EffectiveReport> = {}): EffectiveReport {
  return {
    management: "local",
    revision: { authority: "local", checksum: LOADED, file_checksum: LOADED, retained: false },
    target: { kind: "local-file", path: "/etc/starport/starport.env" },
    paths: {
      relative_path_base: "/etc/starport",
      relative_path_base_origin: "config-file",
      config_dir: "/etc/starport",
      config_file: "/etc/starport/starport.env",
      data_dir: "/var/lib/starport",
      state_dir: "/var/lib/starport/state",
      cache_dir: "/var/cache/starport",
      runtime_dir: "/run/starport",
      baseline_dir: "/var/lib/starport/baseline",
      badger_dir: "/var/lib/starport/badger",
      sqlite_file: "/var/lib/starport/starport.db",
      files_dir: "/var/lib/starport/files",
      local_token_file: "/var/lib/starport/token",
      welcome_stamp_file: "/var/lib/starport/welcome",
      instance_id: "instance-1",
      deployment_id: "deployment-1",
      origins: { state: { path: "/var/lib/starport/state", origin: "platform-default" } },
    },
    storage: [
      { id: "kv", selection: "badger", lifetime: "local", location: "/var/lib/starport/badger" },
      { id: "sql", selection: "postgres", lifetime: "service" },
    ],
    settings: [
      setting("catalog_source", "starmap", "file:/etc/starport/starport.env"),
      setting("catalog_source_url", "https://catalog.example.com"),
      setting("catalog_source_api_key", "<redacted>", "file:/etc/starport/starport.env"),
      setting("catalog_source_token", ""),
      setting("catalog_source_poll_interval", "10m"),
      setting("catalog_acquisition_interval", "1h"),
      setting("catalog_refresh_timeout", "5m"),
      setting("state_dir", "/var/lib/starport/state"),
    ],
    ...extra,
  };
}

function receipt(extra: Partial<Receipt> = {}): Receipt {
  return {
    operation_id: "operation",
    deployment_id: "deployment-1",
    management: "local",
    status: "saved",
    saved: { revision: SAVED, sequence: 0, checksum: SAVED },
    applied: { revision: LOADED, sequence: 0, checksum: LOADED },
    actor: "console",
    created_at: "2026-10-03T00:00:00Z",
    ...extra,
  };
}

type Reply = Response | ((body: FieldSave) => Response);

// gateway answers the configuration routes and records each write body.
function gateway(effective: unknown, writes: Record<string, Reply[]> = {}) {
  const sent: Record<string, FieldSave[]> = {};
  stubGateway({});
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://localhost").pathname;
      if (path === "/api/v1/admin/config/schema") return json({ settings: SCHEMA });
      if (path === "/api/v1/admin/config/effective") {
        return effective instanceof Response ? effective.clone() : json(effective);
      }
      const route = path.split("/").pop() ?? "";
      if (init?.method === "POST") {
        const body = JSON.parse(String(init.body)) as FieldSave;
        (sent[route] ??= []).push(body);
        const reply = writes[route]?.shift();
        if (!reply) return json({ error: { message: "unexpected" } }, 500);
        return typeof reply === "function" ? reply(body) : reply;
      }
      return json({});
    }),
  );
  return sent;
}

function refusal(status: number, reason: string, message: string, extra: Record<string, string> = {}) {
  return json({ error: { message }, refusal: { reason, message, ...extra } }, status);
}

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <ConfigurationSection />
    </QueryClientProvider>,
  );
}

async function draft(label: string, value: string) {
  fireEvent.click(await screen.findByRole("button", { name: `Change ${label}` }));
  fireEvent.change(screen.getByLabelText(`New value for ${label}`), { target: { value } });
}

beforeEach(() => {
  vi.stubGlobal("crypto", { randomUUID: vi.fn(() => `operation-${Math.random()}`) });
});

describe("configuration", () => {
  it("names the local file before a save and saves against the receipt revision next", async () => {
    const sent = gateway(report(), {
      save: [json(receipt()), json(receipt({ saved: { revision: CURRENT, sequence: 0, checksum: CURRENT } }))],
    });
    mount();

    const target = "local file /etc/starport/starport.env";
    expect((await screen.findAllByText(target)).length).toBeGreaterThan(0);
    await draft("Source poll interval", "15m");
    const row = screen.getByLabelText("New value for Source poll interval").closest("div");
    expect(within(row as HTMLElement).getByText(target)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: `Save to ${target}` }));

    await screen.findByText(/Saved local file \/etc\/starport\/starport.env at revision bbbbbbbbbbbb/);
    expect(screen.getByText(/serves revision aaaaaaaaaaaa until it loads the file again/)).toBeTruthy();
    expect(sent.save?.[0]).toMatchObject({
      deployment_id: "deployment-1",
      expected_revision: LOADED,
      edits: { catalog_source_poll_interval: "15m" },
    });

    await draft("Source poll interval", "20m");
    fireEvent.click(screen.getByRole("button", { name: `Save to ${target}` }));
    await waitFor(() => expect(sent.save).toHaveLength(2));
    expect(sent.save?.[1]?.expected_revision).toBe(SAVED);
    expect(sent.save?.[1]?.operation_id).not.toBe(sent.save?.[0]?.operation_id);
  });

  it("names the next shared revision and reports the applied receipt", async () => {
    const shared = report({
      management: "shared",
      namespace: "production",
      revision: { authority: "shared", desired: 7, applied: 7, retained: false },
      target: { kind: "shared-revision" },
    });
    const sent = gateway(shared, {
      save: [
        json(
          receipt({
            management: "shared",
            status: "applied",
            saved: { revision: "8", sequence: 8, checksum: "x" },
            applied: { revision: "8", sequence: 8, checksum: "x" },
          }),
        ),
      ],
    });
    mount();

    expect((await screen.findAllByText("shared revision 8")).length).toBeGreaterThan(0);
    await draft("Acquisition interval", "2h");
    fireEvent.click(screen.getByRole("button", { name: "Save to shared revision 8" }));
    await screen.findByText(/Saved shared revision 8\. This process serves the new revision\./);
    expect(sent.save?.[0]?.expected_revision).toBe("7");
  });

  it("keeps an external controller read only and shows the diff", async () => {
    const sent = gateway(report({ management: "external", controller: "external", target: { kind: "external-controller" } }));
    mount();

    expect((await screen.findAllByText("external controller (read only, shows the diff)")).length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "Test connection" })).toBeNull();
    await draft("Source poll interval", "15m");
    const edits = screen.getByRole("list", { name: "Pending edits" });
    expect(edits.textContent).toContain("Source poll interval: 10m → 15m");
    expect(screen.getByText(/Apply this diff in the external controller/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^Save/ })).toBeNull();
    expect(sent).toEqual({});
  });

  it("offers a stale local save against the current revision with a new operation", async () => {
    const sent = gateway(report(), {
      save: [
        refusal(409, "stale_revision", "the configuration file changed", { expected: LOADED, current: CURRENT }),
        json(receipt({ saved: { revision: SAVED, sequence: 0, checksum: SAVED } })),
      ],
    });
    mount();

    await draft("Source poll interval", "15m");
    fireEvent.click(await screen.findByRole("button", { name: /^Save to local file/ }));
    await screen.findByText(/The save named revision aaaaaaaaaaaa, and the current revision is cccccccccccc\. Nothing was saved\./);
    fireEvent.click(screen.getByRole("button", { name: "Save against revision cccccccccccc" }));
    await waitFor(() => expect(sent.save).toHaveLength(2));
    expect(sent.save?.[1]?.expected_revision).toBe(CURRENT);
    expect(sent.save?.[1]?.operation_id).not.toBe(sent.save?.[0]?.operation_id);
  });

  it("says that nothing was saved when the shared revision store is cold", async () => {
    gateway(
      report({
        management: "shared",
        revision: { authority: "shared", desired: 3, applied: 3, retained: true },
        target: { kind: "shared-revision" },
      }),
      { save: [refusal(503, "unavailable", "the configuration store is not reachable")] },
    );
    mount();

    expect(await screen.findByText(/The last read of the revision store failed\. This process keeps serving applied revision 3\./)).toBeTruthy();
    await draft("Source poll interval", "15m");
    fireEvent.click(screen.getByRole("button", { name: "Save to shared revision 4" }));
    await screen.findByText(
      "The shared revision store is not available. Nothing was saved. The configuration store is not reachable.",
    );
  });

  it("asks for an admin-scoped key when the gateway denies the read", async () => {
    gateway(json({ error: { message: "forbidden" } }, 403));
    mount();

    await screen.findByText("Reading the effective configuration needs an admin-scoped key.");
  });

  it("reports a saved change that this process did not apply", async () => {
    gateway(report(), {
      save: [json(receipt({ activation_error: "the catalog source refused the candidate", audit_error: "the audit record was not written" }))],
    });
    mount();

    await draft("Source poll interval", "15m");
    fireEvent.click(await screen.findByRole("button", { name: /^Save to local file/ }));
    await screen.findByText(
      /Not applied: the catalog source refused the candidate\. This process still serves revision aaaaaaaaaaaa\. The audit record failed: the audit record was not written\./,
    );
  });

  it("retries a save with a lost answer as the same operation", async () => {
    const sent = gateway(report(), {
      save: [json({ error: { message: "upstream closed" } }, 502), json(receipt())],
    });
    mount();

    await draft("Source poll interval", "15m");
    const save = await screen.findByRole("button", { name: /^Save to local file/ });
    fireEvent.click(save);
    await screen.findByText(/The save may have completed\. Save again to retry the same operation\./);
    fireEvent.click(save);
    await waitFor(() => expect(sent.save).toHaveLength(2));
    expect(sent.save?.[1]?.operation_id).toBe(sent.save?.[0]?.operation_id);
  });

  it("keeps node settings out of a field save", async () => {
    gateway(report());
    mount();

    fireEvent.click(await screen.findByRole("button", { name: "Advanced details" }));
    expect(screen.getByText("STARPORT_STATE_DIR")).toBeTruthy();
    expect(screen.getByText("Set in the environment of each process.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Change State dir" })).toBeNull();
  });

  it("opens advanced details from a button that reports its state", async () => {
    gateway(report());
    mount();

    const toggle = await screen.findByRole("button", { name: "Advanced details" });
    expect(toggle.tagName).toBe("BUTTON");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText("Paths")).toBeNull();
    toggle.focus();
    expect(document.activeElement).toBe(toggle);
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    const region = document.getElementById(toggle.getAttribute("aria-controls") ?? "");
    expect(region).not.toBeNull();
    expect(within(region as HTMLElement).getByText("Paths")).toBeTruthy();
    expect(within(region as HTMLElement).getByText("from platform-default")).toBeTruthy();
    expect(within(region as HTMLElement).getByText(/kept on this machine/)).toBeTruthy();
    expect(within(region as HTMLElement).getByText(/kept by an external service/)).toBeTruthy();
    expect(within(region as HTMLElement).getByText(/does not use it/)).toBeTruthy();
  });

  it("the console never prints a sealed value", async () => {
    const sent = gateway(report(), {
      validate: [
        json({
          valid: true,
          current: { revision: LOADED, sequence: 0, checksum: LOADED },
          preview: { revision: SAVED, sequence: 0, checksum: SAVED },
        }),
      ],
      "test-connection": [json({ reachable: false, source: "starmap", error: "the catalog source answered 401" })],
      save: [json(receipt({ settings: ["catalog_source_api_key"] }))],
    });
    mount();

    // The gateway returns the marker, and the page prints the marker.
    expect(await screen.findByText("<redacted>")).toBeTruthy();
    expect(screen.getByText("sealed")).toBeTruthy();

    await draft("Source API key", SECRET);
    const input = screen.getByLabelText("New value for Source API key") as HTMLInputElement;
    expect(input.type).toBe("password");
    const printed = () => document.body.textContent ?? "";
    expect(printed()).not.toContain(SECRET);
    expect(screen.getByRole("list", { name: "Pending edits" }).textContent).toContain("<redacted> → new sealed value");

    fireEvent.click(screen.getByRole("button", { name: "Check" }));
    await screen.findByText(/The change is valid/);
    expect(printed()).not.toContain(SECRET);

    fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
    await screen.findByText(/did not answer/);
    expect(printed()).not.toContain(SECRET);

    fireEvent.click(screen.getByRole("button", { name: /^Save to local file/ }));
    await screen.findByText(/Saved local file/);
    expect(sent.save?.[0]?.edits).toEqual({ catalog_source_api_key: SECRET });
    expect(document.body.innerHTML).not.toContain(SECRET);
  });
});
