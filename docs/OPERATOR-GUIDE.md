# Starport v1 Operator Guide

Last updated: 2026-08-09

This guide covers a single Starport process and a Valkey-backed multi-node
deployment. Starport starts only when storage, Starmap, provider credentials,
and the initial gateway identity are usable.

## Requirements

- Go 1.26.5 for a source build.
- One configured inference provider.
- A writable Badger path, or a reachable Valkey service.

Local initialization generates the provider-credential master key. A
production deployment must supply a master key with at least 32 bytes through
its environment or secret manager. Do not reuse that value as a gateway API
key. Starport stores only the SHA-256 hash of each gateway key.

## First Start

Build Starport:

```bash
make build
```

For OpenAI, supply the provider inference key and run local initialization:

```bash
export STARPORT_PROVIDERS_OPENAI_API_KEY=<provider-inference-key>
./starport init --provider openai
```

For Ollama, use this command instead:

```bash
./starport init --provider ollama
```

This command creates Starport state but does not invent catalog facts. Add
each installed Ollama model to a reviewed Starmap workspace. Then set
`STARPORT_CATALOG_WORKSPACE_PATH` before you start Starport.

Initialization writes the platform `config.env` file with mode `0600`. It
also creates one named identity in the platform Badger directory. The command
prints the new gateway API key once. Save it in a password manager or secret
manager. The command refuses to replace existing configuration or identity
storage. If it cannot write the gateway key, it removes the new state so that
you can retry.

Start the gateway:

```bash
./starport serve
```

The default listener is `http://127.0.0.1:8080`. The configuration package
selects the platform configuration directory. The Badger path is
`data/badger` under that directory.

Check process health:

```bash
curl --fail http://localhost:8080/health/live
curl --fail http://localhost:8080/health/ready
```

## Initialize Configured Storage

Production keeps configuration in environment variables or a secret manager.
Set the storage, master-key, and provider values before the first start. Then
create the first named identity directly:

```bash
starport init --configured-storage --name primary-admin
```

This form does not write a local configuration file. It opens the configured
Badger or Valkey service and refuses any identity repository that already
contains an identity. It prints the new gateway API key once. Save the key in
a secret manager before you start the gateway.

If credential output fails, the command atomically releases the initial
identity and its setup claim. You can then run the command again.

Always check the command exit status. If a remote write has an uncertain
result, the command prints the candidate key before it reports the storage
error. Keep that key while you inspect the repository state.

## Inspect Configuration and Startup State

Show each managed path:

```bash
starport config paths
```

Show the effective configuration without secret values:

```bash
starport config show
```

Validate the same effective configuration that `starport serve` loads:

```bash
starport config validate
```

Run passive startup checks:

```bash
starport doctor
```

Passive checks load configuration and Starmap facts. They also compile the
configured adapter and catalog intersection. They do not open configured
storage or use a network connection.

Add `--probe` to open Badger or Valkey in read-only mode. This probe verifies
the current catalog generation and gateway identity state. Diagnosis never
writes storage and never returns a configured secret.

After an unclean Badger shutdown, the probe can require writable recovery. It
marks the storage and identity checks as skipped. Start and stop
`starport serve` cleanly, and then rerun the probe.

Badger does not support read-only mode on Windows. On Windows, the probe marks
the storage and identity checks as skipped. Use `starport serve` to verify the
normal writable startup.

Each command accepts `--json` for stable machine-readable output. A failed
validation or diagnostic check returns a nonzero exit status.

## Client Base URLs

Use these substitutions in existing clients:

| Client contract | Base URL | API key |
| --- | --- | --- |
| OpenAI | `http://localhost:8080/v1` | A Starport gateway key |
| OpenRouter | `http://localhost:8080/api/v1` | A Starport gateway key |

Example OpenRouter-style request:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"openrouter/auto","messages":[{"role":"user","content":"Hello"}]}' \
  http://localhost:8080/api/v1/chat/completions
```

The key needs `chat:write` or wildcard scope. Model discovery needs
`models:read`. Embeddings need `embeddings:write`, `chat:write`, or wildcard
scope.

## Provider Configuration

Starport uses exact adapter IDs. Current IDs are:

- `openai`
- `anthropic`
- `google-ai-studio`
- `google-vertex`
- `groq`
- `mistral`
- `azure-openai`
- `ollama`

Inference secrets use only `STARPORT_PROVIDERS_*_API_KEY` variables. Starmap
catalog acquisition uses the provider variables in its catalog, such as
`OPENAI_API_KEY`, or its configured cloud credential chain. Starport never
copies an acquisition credential into an inference adapter.

Vertex AI needs `STARPORT_PROVIDERS_GOOGLE_VERTEX_API_KEY`, a project ID, and a location.
The API-key value is an OAuth access token. Set the project with
`STARPORT_PROVIDERS_GOOGLE_VERTEX_PROJECT_ID` and the location with
`STARPORT_PROVIDERS_GOOGLE_VERTEX_LOCATION`. Ollama needs
`STARPORT_PROVIDERS_OLLAMA_ENABLED=true` or the `--enable-ollama` flag.

Starmap owns the model catalog. Only offerings from the active Starmap
generation and configured adapters are routable. A Starmap acquisition failure
does not add a static model list.

Set `STARPORT_CATALOG_REFRESH_ON_START=true` to run Starmap acquisition before
adapter activation. Set `STARPORT_CATALOG_REFRESH_INTERVAL` for later refreshes.
Use `STARPORT_CATALOG_WORKSPACE_PATH` for reviewed tenant facts, including
Azure deployment names and local Ollama model mappings. Those facts enter a
durable Starmap generation before Starport makes the adapter routable.

## Storage Modes

Badger is the default for one process:

```text
STARPORT_STORAGE_MODE=badger
STARPORT_STORAGE_BADGER_PATH=/absolute/path/to/starport/data/badger
```

Stop Starport before copying a Badger directory for backup or restore. Keep
the directory on persistent storage.

Use Valkey for shared state across nodes:

```text
STARPORT_STORAGE_MODE=valkey
STARPORT_STORAGE_VALKEY_URL=valkey://valkey.example:6379
```

Use `rediss://` for a TLS Valkey endpoint. Apply the Valkey service's normal
backup, access-control, and failover procedures.

## Container Start

The Compose file starts Starport with Valkey and requires two environment
values. Initialize the shared identity repository before you start Starport:

```bash
export STARPORT_SECURITY_MASTER_KEY=<master-secret>
export STARPORT_PROVIDERS_OPENAI_API_KEY=<provider-inference-key>
docker compose up --build -d valkey
docker compose run --rm starport init --configured-storage --name primary-admin
docker compose up -d starport
```

Save the gateway key from the initialization output. Do not run the
initialization command again for the same Valkey data set.

## Limits and Shutdown

The main HTTP controls are:

- `STARPORT_SERVER_REQUEST_TIMEOUT`
- `STARPORT_SERVER_MAX_REQUEST_SIZE`
- `STARPORT_SERVER_MAX_HEADER_BYTES`
- `STARPORT_SERVER_SHUTDOWN_TIMEOUT`

`SIGINT` and `SIGTERM` start graceful shutdown. Starport drains HTTP first.
It then closes background work, cache, providers, and storage in reverse
construction order.

## Failure Diagnosis

Run `starport doctor --probe` before you start the server. The output names
each failed check and keeps all secret values redacted.

- `provider credential master key is required`: set
  `STARPORT_SECURITY_MASTER_KEY`.
- `gateway identity is required; run "starport init"`: create the first named
  identity in local or configured storage.
- `at least one production provider is required`: configure one provider or
  enable Ollama.
- HTTP 401: the bearer value does not match an active stored identity.
- HTTP 403: the identity lacks the required scope or owns a different key.
- No route candidate: the model, provider policy, tenant policy, capability,
  context limit, or current offering availability rejected every route.
- SDK check is `UNVERIFIED`: install the named optional official SDK before
  treating that client as tested.

Starport emits structured JSON logs by default. Each request includes a
request ID. Do not log raw gateway keys, provider keys, or master keys.

Starport trusts only the direct TCP peer for client-IP logs. It ignores
`X-Forwarded-For` and `X-Real-IP`. A future trusted-proxy configuration must
name the proxy header and trust boundary explicitly.

## Release Gate

Run these checks before you promote a production image:

```bash
bash scripts/verify-v1-architecture.sh
go test ./...
go test -race ./internal/inference ./internal/catalog ./internal/routing ./internal/execution ./internal/availability ./internal/responsecache ./internal/app ./internal/server
go vet ./...
make lint
make build
docker build .
bash scripts/smoke-openrouter-sdks.sh
```

The verifier must report `Summary: 12 passed, 0 failed`. Required raw HTTP
smoke checks must pass. Optional SDK checks can be green or `UNVERIFIED`. Do
not report an unverified SDK as compatible.
