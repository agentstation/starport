# Starport v1 Operator Guide

Last updated: 2026-08-03

This guide covers a single Starport process and a Valkey-backed multi-node
deployment. Starport starts only when storage, Starmap, provider credentials,
and the initial gateway identity are usable.

## Requirements

- Go 1.26.5 for a source build.
- One configured inference provider.
- A provider-credential master key with at least 32 random characters.
- A different bootstrap API key with at least 32 random characters for empty
  identity storage.
- A writable Badger path, or a reachable Valkey service.

Do not reuse the provider master key as a gateway API key. Starport stores the
gateway key as a SHA-256 hash. It uses the master key to encrypt provider
credentials.

## First Start

Copy the example configuration and set the required secrets:

```bash
cp .env.example .env
```

At minimum, set these values in the environment or `.env`:

```text
STARPORT_SECURITY_MASTER_KEY=<random secret with at least 32 characters>
STARPORT_SECURITY_BOOTSTRAP_API_KEY=<different random key with at least 32 characters>
STARPORT_PROVIDERS_OPENAI_API_KEY=<provider inference key>
```

Any supported provider can replace OpenAI. Starport reads `local.env` before
`.env`. Existing process environment values have the highest priority.

Build and start the gateway:

```bash
make build
./starport serve
```

The default listener is `http://0.0.0.0:8080`. The default Badger data path is
`./data/starport`.

Check process health:

```bash
curl --fail http://localhost:8080/health/live
curl --fail http://localhost:8080/health/ready
```

## Bootstrap and Rotate the Admin Key

The bootstrap key has wildcard scope. Use it only to create the first named
administrator key:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_SECURITY_BOOTSTRAP_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"primary_admin","scopes":["*"]}' \
  http://localhost:8080/api/v1/admin/keys/
```

The response shows the new key once. Save it in a secret manager. Stop the
process, remove `STARPORT_SECURITY_BOOTSTRAP_API_KEY`, and start Starport
again. Startup succeeds because identity storage is no longer empty.

If storage is empty and the bootstrap value is absent, startup fails. If the
same bootstrap value remains configured, startup is idempotent and does not
create duplicate identities.

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
STARPORT_STORAGE_BADGER_PATH=./data/starport
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

The Compose file starts Starport with Valkey and requires three environment
values:

```bash
export STARPORT_SECURITY_MASTER_KEY=<master-secret>
export STARPORT_SECURITY_BOOTSTRAP_API_KEY=<bootstrap-key>
export STARPORT_PROVIDERS_OPENAI_API_KEY=<provider-inference-key>
docker compose up --build
```

For an existing identity store, remove the bootstrap requirement from your
deployment manifest after the first administrator key is safe.

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

- `provider credential master key is required`: set
  `STARPORT_SECURITY_MASTER_KEY`.
- `bootstrap API key is required when identity storage is empty`: set the
  bootstrap key for the first start.
- `at least one production provider is required`: configure one provider or
  enable Ollama.
- HTTP 401: the bearer value does not match an active stored identity.
- HTTP 403: the identity lacks the required scope or owns a different key.
- No route candidate: the model, provider policy, tenant policy, capability,
  context limit, or current offering availability rejected every route.
- SDK check is `UNVERIFIED`: install the named optional official SDK before
  treating that client as tested.

Starport emits structured JSON logs by default. Each request includes a
request ID. Do not log raw gateway keys, provider keys, or bootstrap values.

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
