// @vitest-environment jsdom
import { screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { openConsole, resetGateway, stubGateway } from "@/test/console";

afterEach(resetGateway);

const INFO = {
  service: "starport",
  version: "1.2.0",
  commit: "abc1234",
  build_time: "2026-09-01T00:00:00Z",
  started_at: "2026-09-01T10:00:00Z",
  uptime: "1h30m0s",
  go_version: "go1.26.5",
  os: "linux",
  arch: "arm64",
  storage: { type: "badger", relational: "sqlite", status: "connected" },
  files: { backend: "local" },
  telemetry: {
    metrics: "admin",
    traces: { endpoint_host: "otel.example.com:4318" },
    usage_export: { kind: "http", dropped: 3 },
  },
  guardrails: { checks: ["pii", "moderation"], pii_mode: "refuse", moderation_model: "omni-moderation-latest" },
  retention: { audit_seconds: 172800, files_seconds: 0, job_assets_seconds: 86400 },
};

const WEBHOOKS = {
  configured: true,
  endpoints: ["https://hooks.example.com/starport"],
  events: ["budget.exhausted", "job.completed"],
  queue: { depth: 2, capacity: 256 },
  dead_letters: 1,
};

describe("settings", () => {
  it("states each configured value with the variable that sets it", async () => {
    stubGateway({
      "/api/v1/admin/info": INFO,
      "/api/v1/admin/webhooks": WEBHOOKS,
    });
    openConsole("/settings");

    await screen.findByText("1.2.0");
    expect(screen.getByText("commit abc1234")).toBeTruthy();
    expect(screen.getByText("badger")).toBeTruthy();
    expect(screen.getByText("STARPORT_STORAGE_MODE")).toBeTruthy();
    expect(screen.getByText("sqlite")).toBeTruthy();
    expect(screen.getByText("STARPORT_STORAGE_SQL_MODE")).toBeTruthy();

    expect(screen.getByText("admin scope only")).toBeTruthy();
    expect(screen.getByText("STARPORT_TELEMETRY_METRICS")).toBeTruthy();
    expect(screen.getByText("otel.example.com:4318")).toBeTruthy();
    expect(screen.getByText("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")).toBeTruthy();
    expect(screen.getByText("HTTP receiver")).toBeTruthy();
    expect(screen.getByText("3 dropped")).toBeTruthy();
    expect(screen.getByText("STARPORT_TELEMETRY_USAGE_EXPORT")).toBeTruthy();

    expect(screen.getByText("pii → moderation")).toBeTruthy();
    expect(screen.getByText("STARPORT_GUARDRAILS_CHECKS")).toBeTruthy();
    expect(screen.getByText("refuse the request")).toBeTruthy();
    expect(screen.getByText("STARPORT_GUARDRAILS_PII_MODE")).toBeTruthy();
    expect(screen.getByText("omni-moderation-latest")).toBeTruthy();
    expect(screen.getByText("STARPORT_GUARDRAILS_MODERATION_MODEL")).toBeTruthy();

    await screen.findByText("https://hooks.example.com/starport");
    expect(screen.getByText("STARPORT_EVENTS_WEBHOOK_URLS")).toBeTruthy();
    expect(screen.getByText("STARPORT_EVENTS_WEBHOOK_SECRET")).toBeTruthy();
    expect(screen.getByText("budget.exhausted, job.completed")).toBeTruthy();
    expect(screen.getByText("2 of 256")).toBeTruthy();
    expect(screen.getByText("undelivered events waiting")).toBeTruthy();
    expect(screen.getByText("never delivered since the process started")).toBeTruthy();

    expect(screen.getByText("2 days")).toBeTruthy();
    expect(screen.getByText("STARPORT_AUDIT_RETENTION")).toBeTruthy();
    expect(screen.getByText("no expiry")).toBeTruthy();
    expect(screen.getByText("STARPORT_FILES_RETENTION")).toBeTruthy();
    expect(screen.getByText("1 day")).toBeTruthy();
    expect(screen.getByText("STARPORT_JOBS_ASSET_RETENTION")).toBeTruthy();
  });

  it("names an unstamped build and an empty webhook setup in words", async () => {
    stubGateway({
      "/api/v1/admin/info": {
        ...INFO,
        version: "dev",
        commit: "dev",
        build_time: "dev",
        telemetry: { metrics: "on", traces: null, usage_export: { kind: "off" } },
        guardrails: { checks: [], pii_mode: "redact", moderation_model: "" },
      },
      "/api/v1/admin/webhooks": {
        configured: false,
        endpoints: [],
        events: WEBHOOKS.events,
        queue: { depth: 0, capacity: 0 },
        dead_letters: 0,
      },
    });
    openConsole("/settings");

    expect((await screen.findAllByText("unstamped build")).length).toBe(2);
    expect(screen.queryByText(/commit dev/)).toBeNull();
    expect(screen.getByText("open to every caller")).toBeTruthy();
    expect(screen.getAllByText("off").length).toBe(2);
    expect(screen.getByText("none")).toBeTruthy();
    expect(screen.getByText("redact the finding")).toBeTruthy();
    await screen.findByText("none configured");
    expect(screen.getByText("Moderation model").nextElementSibling?.textContent).toBe("not set");
    expect(screen.getByText("Signing secret").nextElementSibling?.textContent).toBe("not set");
  });

  it("says the deployment sections need an admin-scoped key when the gateway refuses", async () => {
    stubGateway({
      "/api/v1/admin/info": () => new Response(JSON.stringify({ error: { message: "forbidden" } }), {
        status: 403,
        headers: { "content-type": "application/json" },
      }),
      "/api/v1/admin/webhooks": () => new Response(JSON.stringify({ error: { message: "forbidden" } }), {
        status: 403,
        headers: { "content-type": "application/json" },
      }),
    });
    openConsole("/settings");

    await screen.findByText("Reading the system state needs an admin-scoped key.");
    await waitFor(() =>
      expect(screen.getByText("Reading the webhook state needs an admin-scoped key.")).toBeTruthy(),
    );
  });
});
