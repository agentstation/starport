import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { Section } from "@/components/settings/Section";
import { LoadFailed } from "@/components/ui/LoadFailed";
import { ApiError, type SystemInfo, type WebhookSummary } from "@/lib/api";
import { formatCount, formatRetention } from "@/lib/format";
import { queries } from "@/lib/queries";

// The deployment sections read what the gateway states about itself and
// name the environment variable that sets each value. Nothing here writes:
// every one of these settings is a process setting, and the process that
// holds it is the only place that can change it. The console says where.

// A Fact is one row: what the reader calls it, what the gateway says, and
// which variable an operator sets to change it. A row with no variable is
// a fact the build or the clock supplies.
type Fact = { label: string; value: ReactNode; variable?: string; detail?: string };

function FactList({ facts }: { facts: Fact[] }) {
  return (
    <dl className="grid max-w-2xl grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm sm:grid-cols-[auto_1fr_auto]">
      {facts.map((fact) => (
        <div key={fact.label} className="contents">
          <dt className="text-text-3">{fact.label}</dt>
          <dd className="min-w-0 text-text-2">
            <span className="font-mono">{fact.value}</span>
            {fact.detail && <span className="ml-2 text-text-3">{fact.detail}</span>}
          </dd>
          <dd className="col-start-2 font-mono text-xs text-text-4 sm:col-start-3 sm:text-right">
            {fact.variable}
          </dd>
        </div>
      ))}
    </dl>
  );
}

// Gate reads the system info once for every deployment section and turns
// the locked and failed states into one line each, so the five sections
// state the same thing when the admin plane is closed to this browser.
function Gate<T>({
  query,
  what,
  children,
}: {
  query: ReturnType<typeof useQuery<T>>;
  what: string;
  children: (data: T) => ReactNode;
}) {
  if (query.isPending) {
    return <p className="text-sm text-text-3">Reading {what}…</p>;
  }
  if (query.error) {
    if (query.error instanceof ApiError && query.error.needsKey) {
      return <p className="text-sm text-text-3">Reading {what} needs an admin-scoped key.</p>;
    }
    return <LoadFailed what={what} error={query.error} onRetry={() => void query.refetch()} />;
  }
  return <>{children(query.data as T)}</>;
}

function useSystemInfo() {
  return useQuery(queries.systemInfo());
}

// unstamped is what a plain go build reports. The section says so in
// words instead of showing a version that is not one.
function stamped(value: string | undefined): string {
  return !value || value === "dev" ? "unstamped build" : value;
}

function known(value: string | undefined, fallback = "unavailable"): string {
  return value && value !== "unavailable" ? value : fallback;
}

export function SystemSection() {
  const info = useSystemInfo();
  return (
    <Section
      title="System"
      description="The binary that answers this console, and the stores it opened. The gateway states each fact from its build stamp, its clock, or its loaded configuration."
    >
      <Gate query={info} what="the system state">
        {(data: SystemInfo) => (
          <FactList
            facts={[
              { label: "Gateway", value: location.origin },
              {
                label: "Version",
                value: stamped(data.version),
                detail: data.commit && data.commit !== "dev" ? `commit ${data.commit}` : undefined,
              },
              { label: "Built", value: stamped(data.build_time) },
              {
                label: "Uptime",
                value: known(data.uptime),
                detail:
                  data.started_at && data.started_at !== "unavailable"
                    ? `since ${data.started_at}`
                    : undefined,
              },
              { label: "Records", value: known(data.storage?.type), variable: "STARPORT_STORAGE_MODE" },
              {
                label: "Relational",
                value: known(data.storage?.relational),
                variable: "STARPORT_STORAGE_SQL_MODE",
              },
              { label: "File bytes", value: known(data.files?.backend, "none") },
              {
                label: "Platform",
                value: [data.go_version, data.os, data.arch].filter(Boolean).join(" · ") || "unavailable",
              },
            ]}
          />
        )}
      </Gate>
    </Section>
  );
}

const METRICS_MODES: Record<string, string> = {
  on: "open to every caller",
  admin: "admin scope only",
  off: "off",
};

const EXPORT_KINDS: Record<string, string> = {
  off: "off",
  http: "HTTP receiver",
  file: "local file",
};

export function ObservabilitySection() {
  const info = useSystemInfo();
  return (
    <Section
      title="Observability"
      description="Where this gateway reports itself: the Prometheus scrape, the trace collector, and the usage export stream."
    >
      <Gate query={info} what="the telemetry settings">
        {(data: SystemInfo) => {
          const telemetry = data.telemetry ?? {};
          const usageExport = telemetry.usage_export ?? {};
          const kind = usageExport.kind ?? "off";
          return (
            <FactList
              facts={[
                {
                  label: "Metrics scrape",
                  value: METRICS_MODES[telemetry.metrics ?? ""] ?? known(telemetry.metrics),
                  variable: "STARPORT_TELEMETRY_METRICS",
                },
                {
                  label: "Traces",
                  value: telemetry.traces?.endpoint_host ?? "off",
                  variable: "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
                },
                {
                  label: "Usage export",
                  value: EXPORT_KINDS[kind] ?? kind,
                  detail:
                    kind !== "off" && usageExport.dropped !== undefined
                      ? `${formatCount(usageExport.dropped)} dropped`
                      : undefined,
                  variable: "STARPORT_TELEMETRY_USAGE_EXPORT",
                },
              ]}
            />
          );
        }}
      </Gate>
    </Section>
  );
}

const PII_MODES: Record<string, string> = {
  redact: "redact the finding",
  refuse: "refuse the request",
};

export function GuardrailsSection() {
  const info = useSystemInfo();
  return (
    <Section
      title="Guardrails"
      description="The checks every request passes before a provider sees it, in the order they run."
    >
      <Gate query={info} what="the guardrail settings">
        {(data: SystemInfo) => {
          const guardrails = data.guardrails ?? {};
          const checks = guardrails.checks ?? [];
          return (
            <FactList
              facts={[
                {
                  label: "Checks",
                  value: checks.length ? checks.join(" → ") : "none",
                  variable: "STARPORT_GUARDRAILS_CHECKS",
                },
                {
                  label: "PII finding",
                  value: PII_MODES[guardrails.pii_mode ?? ""] ?? known(guardrails.pii_mode),
                  variable: "STARPORT_GUARDRAILS_PII_MODE",
                },
                {
                  label: "Moderation model",
                  value: guardrails.moderation_model || "not set",
                  variable: "STARPORT_GUARDRAILS_MODERATION_MODEL",
                },
              ]}
            />
          );
        }}
      </Gate>
    </Section>
  );
}

export function WebhooksSection() {
  const webhooks = useQuery(queries.webhooks());
  return (
    <Section
      title="Webhooks"
      description="Gateway events pushed to receivers you name. Each delivery carries an HMAC signature under the shared secret."
    >
      <Gate query={webhooks} what="the webhook state">
        {(data: WebhookSummary) => {
          const endpoints = data.endpoints ?? [];
          const queue = data.queue ?? {};
          return (
            <FactList
              facts={[
                {
                  label: "Receivers",
                  value: endpoints.length ? (
                    <ul aria-label="Webhook receivers" className="flex flex-col gap-0.5">
                      {endpoints.map((endpoint) => (
                        <li key={endpoint} className="break-all">
                          {endpoint}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    "none configured"
                  ),
                  variable: "STARPORT_EVENTS_WEBHOOK_URLS",
                },
                { label: "Signing secret", value: endpoints.length ? "set" : "not set", variable: "STARPORT_EVENTS_WEBHOOK_SECRET" },
                {
                  label: "Events",
                  value: (data.events ?? []).join(", ") || "none",
                },
                {
                  label: "Queue",
                  value: `${formatCount(queue.depth ?? 0)} of ${formatCount(queue.capacity ?? 0)}`,
                  detail: "undelivered events waiting",
                },
                {
                  label: "Dead letters",
                  value: formatCount(data.dead_letters ?? 0),
                  detail: "never delivered since the process started",
                },
              ]}
            />
          );
        }}
      </Gate>
    </Section>
  );
}

export function RetentionSection() {
  const info = useSystemInfo();
  return (
    <Section
      title="Retention"
      description="How long the gateway keeps what it stores. A sweep removes a record once its window closes."
    >
      <Gate query={info} what="the retention windows">
        {(data: SystemInfo) => {
          const retention = data.retention ?? {};
          return (
            <FactList
              facts={[
                {
                  label: "Audit log",
                  value: formatRetention(retention.audit_seconds),
                  variable: "STARPORT_AUDIT_RETENTION",
                },
                {
                  label: "Stored files",
                  value: formatRetention(retention.files_seconds),
                  variable: "STARPORT_FILES_RETENTION",
                },
                {
                  label: "Job assets",
                  value: formatRetention(retention.job_assets_seconds),
                  variable: "STARPORT_JOBS_ASSET_RETENTION",
                },
              ]}
            />
          );
        }}
      </Gate>
    </Section>
  );
}
