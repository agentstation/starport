# Starport v1 Operator Guide

Last updated: 2026-08-11

This guide covers a single Starport process and a Valkey-backed multi-node
deployment. Starport starts when storage, Starmap, and the initial gateway
identity are usable. Provider credential state does not control gateway
readiness.

## Requirements

- Go 1.26.5 for a source build.
- A writable Badger path, or a reachable Valkey service.

Local initialization generates the credential-encryption master key. A
production deployment must supply a master key with at least 32 bytes through
its environment or secret manager. Do not reuse that value as a gateway API
key. Starport stores only the SHA-256 hash of each gateway key.

## Local Development

Set any conventional provider credentials that you want Starport to discover.
Then start an isolated development gateway:

```bash
export OPENAI_API_KEY="replace-with-provider-inference-key"
starport dev
```

The command binds to `127.0.0.1`, uses in-memory storage, and creates no
configuration file. It prints one temporary gateway API key:

```text
Starport development gateway
URL: http://127.0.0.1:8080
Gateway API key (shown once): replace-with-generated-gateway-key
```

Keep the development process open. In a second terminal, set the printed key
and check the gateway:

```bash
export STARPORT_API_KEY="replace-with-generated-gateway-key"
curl --fail http://127.0.0.1:8080/health/ready
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  http://127.0.0.1:8080/api/v1/models
```

The model response contains the active Starmap catalog view. It does not prove
that a provider accepts its resolved credential. The first inference attempt
provides that evidence.

## Initialize Persistent State

For a persistent single-process installation, run local initialization once:

```bash
starport init --name primary-admin
```

Initialization writes the platform `config.env` file with mode `0600`. It
also creates one named identity in the platform Badger directory. The command
prints the new gateway API key once. Save it in a password manager or secret
manager. The command refuses to replace existing configuration or identity
storage. If it cannot write the gateway key, it removes the new state so that
you can retry.

Provider inference credentials stay in the process environment or their
configured secret sources. Initialization does not select, copy, or persist
them. Start the gateway after you set the provider credentials:

```bash
starport serve
```

The default listener is `http://127.0.0.1:8080`. The configuration package
selects the platform configuration directory. The Badger path is
`data/badger` under that directory.

Check process health:

```bash
curl --fail http://localhost:8080/health/live
curl --fail http://localhost:8080/health/ready
```

### Initialize configured storage

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
storage or send a provider inference request. A selected cloud identity or
direct secret reference can use its authentication network during credential
resolution.

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

Starmap owns each provider's exact ID, credential fields, ordered conventional
environment names, authentication profiles, endpoint templates, and service
metadata. Starport evaluates every catalog provider against that contract. It
checks conventional names first. It then checks a derived
`STARPORT_<PROVIDER>_<FIELD>` name. For example, it checks `OPENAI_API_KEY`
before `STARPORT_OPENAI_API_KEY`.

Starport selects the first nonempty value in that order. If the selected value
does not satisfy the catalog field contract, resolution fails. Starport does
not continue to a later name.

`starport init` creates gateway security and identity state. It never selects
or writes provider inference credential material. Runtime credential
resolution uses the catalog contract.

Starmap catalog acquisition uses an independent credential plane. Starport
never copies an acquisition credential into an inference request.

### Automatic discovery and refresh

During startup, Starport resolves each provider from its ordered
catalog-declared inference profiles. Deployment-owned material can come from a
static environment value, an explicit secret reference, or a catalog-declared
default cloud credential chain. An authentication-free profile needs no
material. A missing or failed provider credential does not block other
providers, tenant BYOK, or gateway readiness.

The provider reconciler repeats this work every minute by default. Set
`STARPORT_CREDENTIAL_SOURCES_RECONCILE_INTERVAL` to another nonnegative
duration. Set it to `0s` to disable interval reconciliation. The
`STARPORT_CREDENTIAL_SOURCES_RECONCILE_TIMEOUT` value bounds one source
operation.

An administrator can inspect the secret-free state or trigger the same shared
reconciliation work:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  http://127.0.0.1:8080/api/v1/admin/providers

curl --fail-with-body \
  -X POST \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  http://127.0.0.1:8080/api/v1/admin/providers/refresh
```

The status response separates compiled adapter support, operator credential
state, and offering availability. It contains stable reason codes and no
credential material. A manual refresh returns revision numbers, whether the
runtime changed, configured provider IDs, and a failure count. Concurrent
manual and scheduled refreshes share one operation. Request cancellation
cancels that caller's wait and any source work that it owns.

Starport does not send a billable provider request during discovery. A
resolved credential becomes `ready` before a provider accepts it. The
first inference result can change credential state to authentication,
permission, quota, or billing failure. Offering failures remain separate.

A running Starport process does not receive environment changes from another
process. Restart Starport after you change an environment value. File and
remote secret sources can return new material during scheduled or manual
refreshes. The resolver keeps cache hits off the network and uses the source
lifecycle for renewal and expiry.

### Direct secret sources

For each catalog field, Starport derives a product name such as
`STARPORT_OPENAI_API_KEY`. Add `_REFERENCE` to select a direct secret source:

```bash
export STARPORT_OPENAI_API_KEY_REFERENCE='aws-secrets-manager:starport/openai#api-key'
```

This table defines the supported reference resources and source
authentication:

| Backend | Resource | Source authentication |
| --- | --- | --- |
| `gcp-secret-manager` | `projects/PROJECT/secrets/SECRET` or `projects/PROJECT/locations/LOCATION/secrets/SECRET` | Google Application Default Credentials |
| `azure-key-vault` | `https://VAULT_HOST/secrets/SECRET` | `DefaultAzureCredential` |
| `aws-secrets-manager` | A secret name or ARN | AWS default credential chain |
| `vault` | `MOUNT/PATH` for a KV v2 secret | Vault client environment |
| `openbao` | `MOUNT/PATH` for a KV v2 secret | OpenBao client environment |

The full grammar is
`backend:resource?version=VERSION#field`. The version is optional. A
`#field` suffix selects one exact top-level JSON string from Google, Azure, or
AWS. Without a field, these sources preserve the complete scalar payload.
Vault and OpenBao select one exact string field. Without `#field`, their KV v2
record must contain exactly one string value.

For Google, `VERSION` is a version number or alias. For Azure, it is the secret
version. For AWS, it is `VersionId`. Vault and OpenBao require a positive KV v2
version number.

Quote a reference that contains `#field` in a shell or environment file.

An explicit reference precedes conventional and product environment values.
It fails closed by default. To use ambient discovery only when the direct
source reports `not_configured`, set the derived fallback name to `true`:

```bash
export STARPORT_OPENAI_API_KEY_REFERENCE_FALLBACK_AMBIENT=true
```

Denied access, invalid material, source unavailability, timeout, and
cancellation never fall back. A reference contains resource identity only. It
must not contain credentials for the secret store. Starport uses the store's
default identity chain or client environment for that authentication.

Use `env:NAME` when an operator-chosen environment variable must override the
catalog's conventional ambient discovery. For example, this value makes
`TEAM_OPENAI_API_KEY` authoritative even when `OPENAI_API_KEY` is also set:

```bash
export TEAM_OPENAI_API_KEY="replace-with-provider-inference-key"
export STARPORT_OPENAI_API_KEY_REFERENCE='env:TEAM_OPENAI_API_KEY'
```

The selected explicit value must satisfy the catalog field contract. Starport
does not continue to an ambient value after an invalid explicit value. Set
`STARPORT_OPENAI_API_KEY_REFERENCE_FALLBACK_AMBIENT=true` only if an absent
`TEAM_OPENAI_API_KEY` can fall back to conventional discovery.

Use `file:/absolute/path` for a mounted or projected secret. The file must be a
nonempty regular file no larger than 1 MiB. Starport preserves every byte and
does not trim a trailing newline. The resolver detects these replacement
patterns without depending only on modification time:

- An in-place rewrite.
- An atomic file replacement or rename.
- A symbolic-link target swap.
- A Kubernetes projected-volume `..data` symbolic-link swap.
- Mounted-content replacement by a CSI driver.
- A secret-agent rerender that replaces the file.

Use a new material version for a deliberate rotation. Concurrent resolutions
share one refresh operation. A cache hit makes no file or network request.
Environment values that a wrapper injects belong to that child process. Restart
the process to receive a changed value unless the wrapper owns a supervised
restart policy.

### Secret-manager command wrappers

These wrappers can inject the catalog-declared conventional names, such as
`OPENAI_API_KEY`, without a Starport-specific integration. Authenticate and
select the wrapper project or environment first. Then use one of these verified
forms:

```bash
# Doppler
doppler run -- starport serve

# 1Password: .env.starport contains NAME=op://vault/item/field references
op run --env-file="./.env.starport" -- starport serve

# Infisical
infisical run -- starport serve
```

See the official command references for
[Doppler](https://docs.doppler.com/docs/cli),
[1Password](https://www.1password.dev/cli/reference/commands/run), and
[Infisical](https://infisical.com/docs/cli/commands/run). Apply each product's
least-privilege workload authentication and production lifecycle guidance.

Vault uses its standard client environment, such as `VAULT_ADDR`,
`VAULT_TOKEN`, and `VAULT_NAMESPACE`. OpenBao uses `BAO_ADDR`, `BAO_TOKEN`, and
`BAO_NAMESPACE`.

Starport resolves a direct reference before inference uses the material. It
caches the result in memory and refreshes it through the credential lifecycle.
A cache hit does not contact the secret store. By default, the next resolution
after five minutes refreshes direct-source material. Set
`STARPORT_CREDENTIAL_SOURCES_REMOTE_REFRESH_INTERVAL` to a different positive
duration. Starport never logs or serializes the returned material.

Vertex AI needs a project ID, one location, and Google Application Default
Credentials:

```bash
export GOOGLE_CLOUD_PROJECT="replace-with-project-id"
export GOOGLE_CLOUD_LOCATION="us-central1"
```

Azure OpenAI needs a resource base URL. Select Azure
`DefaultAzureCredential` with this value:

```bash
export AZURE_OPENAI_ENDPOINT="https://replace-with-resource.openai.azure.com"
```

Set `AZURE_OPENAI_API_KEY` to select the catalog's static API-key profile.
Without that key, Starport uses the catalog's `azure-default` profile. Starport
refreshes default bearer tokens before expiry. Request cancellation also
cancels credential acquisition.

Ollama uses the catalog default `OLLAMA_BASE_URL` value when the environment
does not set it. You do not need a provider-specific CLI flag.

Starmap owns the model catalog. Only offerings from the active Starmap
generation and configured adapters are routable. A Starmap acquisition failure
does not add a static model list.

### Tenant provider credentials

An authenticated gateway identity can own an encrypted provider credential at
`/api/v1/keys/{key_id}/provider-keys`. Set its
`provider_credential_strategy` metadata to one of these exact values:

- `operator_first`: try deployment-owned material, then tenant material.
- `user_first`: try tenant material, then deployment-owned material.
- `user_only`: use only tenant material.

The default is `operator_first`. Starport can advance to the next credential
only when material is not configured or when the provider reports an
authentication or rate-limit failure. Permission, invalid-material, timeout,
cancellation, and internal failures are terminal. Each credential advance
uses the existing total attempt budget. It does not create a provider-health
failure or a hidden retry budget.

`user_only` does not read or test deployment-owned material. Its external
missing-credential error is the same whether deployment-owned material exists
or not. Tenant credential lookup uses the exact authenticated gateway-key ID.
It never merges a global stored record.

Set `STARPORT_CATALOG_REFRESH_ON_START=true` to run Starmap acquisition before
runtime activation. Set `STARPORT_CATALOG_REFRESH_INTERVAL` for later catalog
refreshes. These values update catalog facts. They do not control inference
credential reconciliation.
Use `STARPORT_CATALOG_WORKSPACE_PATH` for reviewed tenant facts, including
Azure deployment names and local Ollama model mappings. Those facts enter a
durable Starmap generation before Starport makes the adapter routable.

## Remote Starmap Catalogs

Set one versioned Starmap API base URL to receive verified catalog generations:

```bash
export STARPORT_CATALOG_REMOTE_URL="https://catalog.example.com/api/v1"
export STARPORT_CATALOG_REMOTE_API_KEY="replace-if-the-server-requires-one"
export STARPORT_CATALOG_REMOTE_ACTIVATION_INTERVAL="250ms"
```

Starport sends the optional API key as `X-API-Key`. Configuration inspection
redacts the key and the remote URL. A non-loopback publisher must use HTTPS
with a valid certificate chain. Starmap accepts plain HTTP only for a loopback
publisher. The URL identifies the publisher origin and must include the
server's versioned path, normally `/api/v1`.

Remote mode is mutually exclusive with
`STARPORT_CATALOG_WORKSPACE_PATH`,
`STARPORT_CATALOG_REFRESH_ON_START=true`, and a nonzero
`STARPORT_CATALOG_REFRESH_INTERVAL`. The remote API key is invalid without the
remote URL. `STARPORT_CATALOG_REFRESH_TIMEOUT` bounds manifest and payload
requests. It does not impose a timeout on the SSE connection. Starmap heartbeat
and liveness rules own that connection.

Starmap verifies the manifest, schema range, immutable payload identity,
content type, size, and SHA-256 checksum before it publishes one atomic
candidate. Starport then validates the complete routable catalog, connector,
and credential projection before it accepts that generation. A failed
candidate leaves the current routes, connectors, and response-cache identity
unchanged.

Starport stores two current pointers over shared immutable generation records:

- The remote head records the latest Starmap-verified generation.
- The accepted head records the latest generation that passed the complete
  Starport runtime transaction.

This separation prevents a Starmap-valid but Starport-incompatible generation
from replacing restart-safe routing state. On restart, the accepted generation
is the pinned bootstrap. A network failure keeps that last accepted state while
the subscriber uses bounded reconnect and catch-up. HTTP 401 and 403 stop the
active subscriber lifecycle and require corrected credentials or access before
a new process starts it again.

The activation interval samples Starmap's in-memory atomic state. It causes no
catalog network request and is not on the inference path. `starport doctor`
and `starport doctor --probe` inspect the durable accepted generation without a
remote fetch.

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

The Compose file starts Starport with Valkey. Its optional `.env` file passes
catalog-declared provider values into the Starport container without a provider
roster in the Compose file. Copy the example, set the master key, and set the
provider values that this deployment needs. This example uses OpenAI:

```bash
cp .env.example .env
# Edit .env. Set STARPORT_SECURITY_MASTER_KEY and OPENAI_API_KEY.
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
- Provider credential state is `not_configured`: set one of the conventional,
  derived, cloud, or secret-reference values that its Starmap profile declares.
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
go test -race ./internal/inference ./internal/catalog ./internal/routing ./internal/execution ./internal/availability ./internal/response/cache ./internal/app ./internal/server
go vet ./...
make lint
make build
docker build .
bash scripts/smoke-openrouter-sdks.sh
```

The verifier must report `Summary: 12 passed, 0 failed`. Required raw HTTP
smoke checks must pass. Optional SDK checks can be green or `UNVERIFIED`. Do
not report an unverified SDK as compatible.
