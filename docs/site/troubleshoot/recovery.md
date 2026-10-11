---
title: Recover without a console session
area: troubleshoot
order: 1
summary: Find the state of a gateway and recover it with health checks, CLI commands, logs, and these pages when the console is not available.
---

This topic is for an operator who cannot open a console session. Each step uses a health route, a CLI command on the gateway host, the logs, or a repository procedure.

## Health checks

Two health routes answer without a credential. They need no gateway API key and no console session.

| Route | Question that it answers | Response |
| --- | --- | --- |
| `GET /health/live` | Does the process run? | `200` with `"status": "ok"`. |
| `GET /health/ready` | Can the gateway serve requests now? | `200` with `"status": "ok"`, or `503` with `"status": "not_ready"` and `Retry-After: 1`. |

The readiness check reads only process memory. It does not send a request to storage or to a provider. A ready gateway does not prove that a caller key, an account permission, or a budget is valid.

```bash
curl -sS -i <gateway-url>/health/live
curl -sS -i <gateway-url>/health/ready
```

## CLI inspection commands

Run these commands on the gateway host with the same configuration as the gateway. Each command reads state. It does not change the deployment.

| Command | Result |
| --- | --- |
| `starport doctor --json` | Read-only startup checks. Add `--probe` to open configured storage in read-only mode. |
| `starport config show --json` | The effective configuration with secrets redacted. |
| `starport config validate --json` | The validation result of the effective configuration. |
| `starport config effective --json` | Each catalog setting with its authority, origin, and revision. |
| `starport config paths --files --inspect --json` | The files and directories that the configuration uses, with their state. |
| `starport auth status --json` | The age, generation, and exposure of the local admin token. |

## Local admin token and the console

The local admin token is a file on the gateway host. It is not a gateway API key, and no account owns it. Use it to open the console from the host.

- `starport ui` makes a one-time launch link and opens it in a browser.
- `starport ui --no-open` prints the launch link. Use it on a host without a browser.
- `starport auth url --copy` copies a one-time launch link to the clipboard.

The console session uses a cookie. You do not paste a gateway API key into the browser. For token rotation, refer to [Authentication](../operate-starport/authentication.md).

## Logs and request IDs

Starport writes logs to a stream. The default format is JSON and the default output is `stdout`. Each request log line has a `request_id` field. Find the request ID of a failed request, and then search the logs for that value.

```bash
grep '"request_id":"<request-id>"' <log-file>
```

The `<log-file>` is the file where your process manager keeps the gateway output.

## Read these pages without a session

The gateway serves these pages at `<gateway-url>/docs/`. The pages need no session and no gateway API key. The same pages also open from disk with no network.

## Coordinated recovery

To restore a deployment or to adopt a populated deployment, use the coordinated recovery procedure in [RECOVERY.md](../../RECOVERY.md). That procedure keeps every writer fenced until activation and gateway readiness succeed.

## Recover a gateway

### Audience

An operator with shell access to the gateway host.

### Before you start

- Get the gateway address and shell access to the host.
- Use the configuration that the gateway uses.
- Keep every recovery file. Do not delete a journal or a lock.

### Steps

1. Send `GET /health/live`. If it fails, start the process and read its first log lines.
2. Send `GET /health/ready`. Record the status and the `Retry-After` header.
3. Run `starport doctor --json` and record each failed check.
4. Run `starport config validate --json`. Correct each reported setting.
5. Run `starport ui` to open the console with the local admin token.
6. For a failed request, find its `request_id` in the logs.
7. For a restore or an adoption, follow [RECOVERY.md](../../RECOVERY.md).

### Expected result

The live route and the ready route both return `200`. The console opens from the launch link.

### Verification

```bash
curl -sS <gateway-url>/health/ready
```

```json
{"status":"ok","timestamp":"<time>","service":"<service>","version":"<release>"}
```

### If it fails

- If the ready route stays at `503`, read [Catalog failures](catalog-failures.md).
- If storage, disk, lease, or schema errors appear, read [Resource failures](resource-failures.md).
- If a command reports an unknown journal, read [Starmap journals and recovery](../operate-starmap/journals-and-recovery.md).

### Related settings

- `STARPORT_LOGGING_LEVEL` sets the log level. The default is `info`.
- `STARPORT_LOGGING_FORMAT` sets the log format. The default is `json`.
- `STARPORT_SERVER_HOST` and `STARPORT_SERVER_PORT` set the listener. The defaults are `127.0.0.1` and `7827`.
