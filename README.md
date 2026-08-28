# Starport

Starport is a self-hosted LLM inference gateway. It serves OpenAI-compatible
APIs at `/v1` and OpenRouter-compatible APIs at `/api/v1`.

Starport uses Starmap as its only source of provider, model, capability,
context, price, and service facts. Starport owns inference credentials,
gateway identities, routing policy, execution, and HTTP protocols.

Starport adds less than 50 ms p99 gateway overhead per request. The number
excludes provider inference time, ships on every response as the
`x-starport-overhead-ms` header, and a CI benchmark fails the build when
the bound breaks. See [docs/PERFORMANCE.md](docs/PERFORMANCE.md) for the
measurement methodology.

## Install

Install the released cask on macOS or Linux:

```bash
brew install agentstation/tap/starport
starport --version
```

The current public release also contains checksummed archives for macOS,
Linux, and Windows. Download an archive from
[GitHub Releases](https://github.com/agentstation/starport/releases).

To build from source, install the Go version from `go.mod`, then run:

```bash
git clone https://github.com/agentstation/starport.git
cd starport
make build
./starport --version
```

## Quick start

Starport checks every provider in the active Starmap catalog. It registers each
provider whose transport and authentication primitive it supports. It
separately discovers deployment-owned inference credentials from the ordered
profiles in that catalog. You do not select a provider with `starport init` or
a provider-specific flag.

Two kinds of credential appear below and they are not interchangeable. A
**gateway API key** authenticates a client to Starport and carries its scopes
and limits. A **provider credential** pays a provider. A gateway API key never
pays a provider, and a provider credential never authenticates a client.

### Terminal 1: start Starport and open the console

Set one conventional provider credential. This example uses OpenAI:

```bash
export OPENAI_API_KEY="replace-with-provider-inference-key"
starport dev
```

The command starts an isolated gateway at `http://127.0.0.1:8080`. It uses
in-memory state, creates no configuration files, prints one temporary Starport
gateway API key, and opens the console in a browser:

```text
Starport development gateway
URL: http://127.0.0.1:8080
Authentication: required
Gateway API key (shown once): replace-with-generated-gateway-key
Console (one-time launch link): http://127.0.0.1:8080/launch?lt=replace-with-ticket
```

The console link is not a key. It is spent the first time it is followed and
exchanged for a browser session this machine issued, so nothing is pasted into
the browser and no key is stored there. Add `--no-open` to print the link
instead of opening a browser, which is what a machine reached over SSH needs.
`starport ui` opens a new one at any time.

A browser that reaches the console without a link is asked for this machine's
local admin token instead. `starport auth token --copy` puts it on the
clipboard of the machine running the gateway. Both ways in prove the same
thing — that you are at that machine — and both end in the same session.

Keep this terminal open.

### Terminal 2: call Starport

Copy the printed gateway key into a second terminal. This key authenticates the
client to Starport. It is not the provider inference key.

```bash
export STARPORT_API_KEY="replace-with-generated-gateway-key"
```

Readiness is independent of provider credentials. A ready response means that
the gateway can accept requests. The authenticated model response contains the
current Starmap catalog view.

```bash
curl --fail http://127.0.0.1:8080/health/ready
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  http://127.0.0.1:8080/api/v1/models
```

Send an OpenRouter-style chat request:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"openrouter/auto","messages":[{"role":"user","content":"Hello"}]}' \
  http://127.0.0.1:8080/api/v1/chat/completions
```

The first provider request proves whether the provider accepts the resolved
credential and whether the account can use the selected offering. Starport
records authentication, permission, quota, billing, rate-limit, and service
failures in its scoped provider state.

### Serve without a gateway API key

Starport requires a gateway API key by default. To serve open — a workstation,
a private container, a test rig — add `--no-auth` to `starport dev` or
`starport serve`, or use the switch in the console under Settings. An open
gateway can be closed again from the machine running it.

Starport refuses `--no-auth` on an address the network can reach unless you
also pass `--allow-remote-no-auth`. See
[Authentication mode](docs/OPERATOR-GUIDE.md#authentication-mode).

### Keep the gateway

For persistent local or production state, run `starport init` once. The command
creates a Starport master key and initial gateway identity. It does not select a
provider or persist provider inference credentials. Then use `starport serve`,
and `starport ui` to open the console. Issue further gateway API keys in the
console under Keys.
See the [operator guide](docs/OPERATOR-GUIDE.md#initialize-persistent-state).

Local Ollama inference needs no credential. Add each installed model to a
reviewed Starmap workspace, and set `STARPORT_CATALOG_WORKSPACE_PATH` before
startup.

## Replace an existing gateway URL

Use a Starport gateway key for client authentication.

| Client contract | Base URL |
| --- | --- |
| OpenAI | `http://127.0.0.1:8080/v1` |
| OpenRouter | `http://127.0.0.1:8080/api/v1` |

OpenAI Python example:

```python
import os

from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8080/v1",
    api_key=os.environ["STARPORT_API_KEY"],
)

response = client.chat.completions.create(
    model="openai/gpt-4o-mini",
    messages=[{"role": "user", "content": "Hello"}],
)
```

For an OpenRouter client, replace its default base URL with
`http://127.0.0.1:8080/api/v1`. Keep the client request and response types.

## Configuration

Starport reads `config.env` from the platform user configuration directory.
Process environment variables override the file. `starport config paths`
prints the resolved paths.

Set `STARPORT_CONFIG_DIR` to an absolute path for an isolated development or
CI instance. This value changes the configuration, data, and rate-limit paths
together.

`starport config show` prints the effective schema and hides secret values.
`starport doctor` runs passive checks. Add `--probe` for read-only storage and
identity checks.

Provider IDs, credential fields, conventional environment names, defaults,
authentication profiles, and endpoints come from the active Starmap catalog.
For example, Starport checks `OPENAI_API_KEY` before
`STARPORT_OPENAI_API_KEY`. A provider that uses an already compiled transport
and authentication primitive needs no Starport provider switch.

Starport resolves all catalog providers at startup and, by default, reconciles
them every minute. Set `STARPORT_CREDENTIAL_SOURCES_RECONCILE_INTERVAL` to
change that interval. An administrator can also trigger the same shared work:

```bash
curl --fail-with-body \
  -X POST \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  http://127.0.0.1:8080/api/v1/admin/providers/refresh
```

Another process cannot change Starport's process environment. Restart Starport
after you change an environment value. File and remote secret sources can
return new material during interval or manual reconciliation.

Add `_REFERENCE` to the catalog-derived Starport name to select a direct secret
source. Starport supports Google Cloud Secret Manager, Azure Key Vault, AWS
Secrets Manager, HashiCorp Vault KV v2, and OpenBao KV v2. For example:

```bash
export STARPORT_OPENAI_API_KEY_REFERENCE='aws-secrets-manager:starport/openai#api-key'
```

The [operator guide](docs/OPERATOR-GUIDE.md#direct-secret-sources) defines the
resource syntax, source authentication, version selection, and fallback rule.

To consume verified catalog publications from a Starmap server, set its
versioned API base URL:

```bash
export STARPORT_CATALOG_REMOTE_URL="https://catalog.example.com/api/v1"
export STARPORT_CATALOG_REMOTE_API_KEY="replace-if-the-server-requires-one"
```

Remote mode keeps the last accepted generation for restart and recovery. It is
mutually exclusive with a local catalog workspace and local acquisition. See
the [remote catalog guide](docs/OPERATOR-GUIDE.md#remote-starmap-catalogs).

See the [configuration reference](.env.example) and
[operator guide](docs/OPERATOR-GUIDE.md) for production settings.

## Cloud credentials

Vertex AI and Azure OpenAI can use renewable default cloud credentials. Their
project, location, and endpoint fields use the conventional names declared by
Starmap:

```bash
export GOOGLE_CLOUD_PROJECT="replace-with-project-id"
export GOOGLE_CLOUD_LOCATION="us-central1"

export AZURE_OPENAI_ENDPOINT="https://replace-with-resource.openai.azure.com"
```

Vertex AI uses Google Application Default Credentials. Azure OpenAI uses
`AZURE_OPENAI_API_KEY` when present. Without it, Azure OpenAI uses
`DefaultAzureCredential`. Starport gets renewable bearer tokens before an
inference request uses them.

Starmap catalog-acquisition credentials remain separate from Starport
inference credentials.

## Containers

Pull a versioned image and verify its GitHub attestation:

```bash
STARPORT_VERSION="$(gh release view \
  --repo agentstation/starport \
  --json tagName \
  --jq '.tagName | ltrimstr("v")')"
docker pull "ghcr.io/agentstation/starport:$STARPORT_VERSION"
gh attestation verify "oci://ghcr.io/agentstation/starport:$STARPORT_VERSION" \
  --repo agentstation/starport \
  --signer-workflow agentstation/starport/.github/workflows/release.yaml
docker run --rm "ghcr.io/agentstation/starport:$STARPORT_VERSION" --version
```

The Compose file builds Starport locally and uses Valkey for shared state. Put
the master key and any catalog-declared provider values in the ignored `.env`
file. This example uses OpenAI:

```bash
cp .env.example .env
# Edit .env. Set STARPORT_SECURITY_MASTER_KEY and OPENAI_API_KEY.
docker compose up --build -d valkey
docker compose run --rm starport init --configured-storage --name primary-admin
docker compose up -d starport
```

Save the gateway key from initialization. Do not initialize the same identity
repository again.

## Version 1 scope

Version 1 includes:

- Chat completions, streaming chat, embeddings, and model discovery.
- Exact provider and model routing with fallback and `openrouter/auto`.
- Provider routing preferences: order, sort, price caps, and model variants.
- Presets with `@preset/` model references.
- Catalog-driven providers over the compiled OpenAI, Anthropic, Google Cloud,
  Google AI Studio, and Ollama transport primitives.
- Encrypted provider credentials, renewable cloud credentials, and direct
  secret-source references.
- Header-only gateway authentication, per-key rate limits, per-key budgets,
  and allowed-model limits.
- Request logs and usage accounting with catalog-priced costs at
  `/api/v1/activity`.
- An embedded web console with overview, chat with model comparison, models,
  providers, usage, presets, keys, files, and settings pages.
- A file store at `/v1/files` that keeps a document for a later chat request.
  It writes to a local filesystem or an S3-compatible bucket.
- A `file-parser` plugin that reads an attached document before the chat model
  sees it. The `native` engine reads a text layer in process and charges
  nothing. The `recognition` engine sends a scanned page to a catalog model
  that serves `documents-recognition`, and the record reports what the pages
  cost.
- Reranking at `/v1/rerank` and `/api/v1/rerank`, which scores a document list
  against one query. It needs the `rerank:write` scope, and Starmap owns the
  offerings, the billing basis, and the price.
- Tenant-safe response caching.
- Badger storage for one process and Valkey storage for multiple processes.

Starport uses direct changes and has no legacy provider aliases or storage
readers. It does not yet promise a compatibility window.

## Develop

```bash
make deps
make check
bash scripts/smoke-first-run.sh
bash scripts/smoke-openrouter-sdks.sh
```

`make check` reads files but does not change them. Use `make format` or
`make tidy` when you want to change source or module files.

See the [development guide](DEVELOPMENT.md) and
[contribution guide](docs/CONTRIBUTING.md).

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Operator guide](docs/OPERATOR-GUIDE.md)
- [Vertex AI configuration](docs/VERTEX_AI_CONFIG.md)
- [Model catalog contract](MODELS.md)
- [Documentation index](docs/README.md)

## License and security

Starport uses the GNU AGPLv3 license. See [LICENSE](LICENSE).

Report vulnerabilities through the process in [SECURITY.md](SECURITY.md).
