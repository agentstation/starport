---
title: Diagnostics and observability
area: operate-starport
order: 5
summary: Inspect a gateway before it starts, and read its metrics, traces, usage export, audit log, and webhook state while it runs.
---

Starport gives operators read-only startup checks and five runtime signals. No signal carries a credential value, an account in a metric label, or prompt content.

## Startup inspection commands

Run these commands before you start the server. They change no state.

| Command | Reports |
| --- | --- |
| `starport doctor --probe --json` | Each failed startup check. It opens configured storage in read-only mode. |
| `starport config validate` | Configuration errors |
| `starport config show` | The effective configuration with secrets redacted |
| `starport config paths --files --inspect --json` | Each managed file and its recovery policy |
| `starport config effective` | Each catalog setting with its authority, origin, and revision |

The doctor output keeps secret values redacted. Common messages and corrections are in [Resource failures](../troubleshoot/resource-failures.md).

## Metrics

`GET /metrics` serves a Prometheus scrape. `STARPORT_TELEMETRY_METRICS` controls access.

| Value | Effect |
| --- | --- |
| `on` | The default. The scrape needs no credential. |
| `admin` | The scrape needs a gateway key with the `admin` scope. |
| `off` | The route does not exist. |

Each metric name starts with `starport_`. Labels name the protocol, operation, provider, model, and outcome. Labels never carry an account or a key. These counters show lost signals.

| Counter | Counts |
| --- | --- |
| `starport_budget_refusals_total` | Budget refusals, by scope and dimension |
| `starport_usage_export_dropped_total` | Usage records that the export sink dropped |
| `starport_webhook_dead_letters_total` | Webhook events that Starport did not deliver |

## Traces

Starport exports OpenTelemetry traces over OTLP HTTP. Tracing is off until you set `OTEL_EXPORTER_OTLP_ENDPOINT`. `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` wins when you set both. These names have no `STARPORT_` prefix.

One chat request makes the spans `starport.request`, `starport.route_plan`, `starport.attempt`, and `starport.provider_call`. An inbound W3C `traceparent` header continues the trace of the caller.

## Usage export

`STARPORT_TELEMETRY_USAGE_EXPORT` streams each finished usage record. An `http://` or `https://` value posts NDJSON batches. Another value is a file path. The sink never stops a request.

Each batch gets three post attempts in total. After the third failure, the batch drops. The durable store keeps each record.

`GET /api/v1/activity/export` streams the records of the caller key with the `activity:read` scope. `GET /api/v1/admin/activity/export` streams the records of each key with the `admin` scope. Add `format=csv` for CSV.

## Audit log

Each admin change makes one durable record. The record has the actor, the target, the outcome, and the `request_id`. It never holds a credential value. Read the log with `GET /api/v1/admin/audit`. `STARPORT_AUDIT_RETENTION` sets the retention window. The default is `9600h`, which is 400 days.

## Webhooks

Webhooks are off until `STARPORT_EVENTS_WEBHOOK_URLS` names one or more receivers. `STARPORT_EVENTS_WEBHOOK_SECRET` signs each delivery in the `X-Starport-Signature` header. Set the secret with each receiver, because an empty secret authenticates nothing. `GET /api/v1/admin/webhooks` shows the receivers, the queue depth, and the dead letter count.

## Check the signals of a running gateway

### Audience

An operator who connects a gateway to a monitoring system.

### Before you start

- Get a gateway key with the `admin` scope in `$STARPORT_ADMIN_KEY`.

### Steps

1. Run `starport doctor --probe --json` on the gateway host.
2. Start the gateway.
3. Read the scrape and the webhook state.

```bash
curl -fsS <gateway-url>/metrics | grep -c '^starport_'
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" <gateway-url>/api/v1/admin/webhooks | jq '.configured, .dead_letters'
```

### Expected result

The scrape count is more than zero. The webhook response shows `configured` and a dead letter count of `0`.

### Verification

```bash
curl -sS -H "Authorization: Bearer $STARPORT_ADMIN_KEY" "<gateway-url>/api/v1/admin/audit?limit=5" | jq '.data | length'
```

The command returns the number of audit records in the newest page. The maximum is `5`.

### If it fails

A `401` from `/metrics` means that the mode is `admin`. Add the admin key as a bearer credential. A `404` means that the mode is `off`.

### Related settings

- `STARPORT_TELEMETRY_METRICS` sets scrape access.
- Refer to [Catalog updates](catalog-updates.md) for catalog freshness alerts.
