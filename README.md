# Starport

Starport is a self-hosted LLM gateway written in Go. It exposes OpenAI and
OpenRouter HTTP contracts over one provider-neutral inference core. It uses
Starmap as the only source of model, provider, capability, context, and price
facts.

Status: v1. Starport uses direct breaking changes. Starport does not yet
publish a compatibility policy. It has no legacy provider aliases, storage
prefixes, or schema readers.

## Version 1 Scope

The current version includes:

- OpenAI-compatible APIs under `/v1`.
- OpenRouter-compatible APIs under `/api/v1`.
- Chat completions, streaming chat, embeddings, and model discovery.
- Exact model and provider routing with fallback and `openrouter/auto`.
- One total attempt budget and offering-level availability state.
- OpenAI, Anthropic, Google AI Studio, Vertex AI, Groq, Mistral, Azure
  OpenAI, and Ollama adapters.
- Encrypted BYOK provider credentials.
- Header-only gateway authentication and per-key rate limits.
- Tenant-safe response caching.
- Badger storage for one node and Valkey storage for multiple nodes.
- One binary with explicit startup and shutdown ownership.

Content moderation, preset APIs, OpenTelemetry, complete billing analytics,
webhooks, and enterprise SSO/RBAC are outside the current v1 scope.

## First Start

Requirements:

- Go 1.26.5 for a source build.
- An OpenAI inference key or a running Ollama service.

Build Starport:

```bash
make build
./starport version
```

Initialize an OpenAI-backed local instance. The command reads the provider
credential from the named environment variable. It generates the credential
master key and one named gateway identity.

```bash
export STARPORT_PROVIDERS_OPENAI_API_KEY=<provider-inference-key>
./starport init --provider openai
```

For Ollama, use this command instead:

```bash
./starport init --provider ollama
```

The Ollama profile creates local Starport state. Before you start Starport,
add each installed Ollama model to a reviewed Starmap workspace and set
`STARPORT_CATALOG_WORKSPACE_PATH`. Starmap owns the model identity,
capabilities, and Ollama offering facts.

The command writes `config.env` and the Badger identity store under the user
configuration directory. It refuses existing configuration or identity
storage. It prints the new gateway API key once. Save that key. For the OpenAI
profile, start Starport:

```bash
./starport serve
```

Check health:

```bash
curl --fail http://localhost:8080/health/live
curl --fail http://localhost:8080/health/ready
```

## Client Configuration

Use a Starport gateway API key for client authentication.

| Contract | Base URL |
| --- | --- |
| OpenAI | `http://localhost:8080/v1` |
| OpenRouter | `http://localhost:8080/api/v1` |

OpenAI SDK example:

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key="<starport-gateway-key>",
)

response = client.chat.completions.create(
    model="openai/gpt-4o-mini",
    messages=[{"role": "user", "content": "Hello"}],
)
```

OpenRouter-style request:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $STARPORT_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"openrouter/auto","messages":[{"role":"user","content":"Hello"}]}' \
  http://localhost:8080/api/v1/chat/completions
```

## Catalog Identities

Starmap is the source of provider IDs, model IDs, offerings, capabilities,
prices, and provider service metadata. Read the active values from
`GET /api/v1/providers` and `GET /api/v1/models`. Starport keeps each ID exact
and opaque. It does not normalize old or alternate names.

## Containers

Pull the signed-release identity by version, not by an unverified digest:

```bash
docker pull ghcr.io/agentstation/starport:1.0.0
gh attestation verify oci://ghcr.io/agentstation/starport:1.0.0 \
  --repo agentstation/starport \
  --signer-workflow agentstation/starport/.github/workflows/release.yaml
docker run --rm ghcr.io/agentstation/starport:1.0.0 --version
```

The Compose file builds Starport locally and starts it with Valkey:

```bash
export STARPORT_SECURITY_MASTER_KEY=<master-secret>
export STARPORT_PROVIDERS_OPENAI_API_KEY=<provider-inference-key>
docker compose up --build -d valkey
docker compose run --rm starport init --configured-storage
docker compose up -d starport
```

The initialization command prints the gateway API key once. It refuses a
Valkey identity repository that already contains an identity.

For a single-node container, mount `/var/lib/starport/data`. Run
`starport init --configured-storage` with that mount and the required
environment values before the first `starport serve` command.

## Development and Verification

```bash
go test ./...
bash scripts/verify-starmap-ownership.sh
bash scripts/verify-v1-architecture.sh
bash scripts/verify-v1-release.sh
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
```

The smoke runner tests raw HTTP plus the pinned official OpenRouter Python,
TypeScript, and Go SDKs. A missing or incompatible SDK is a failed gate.

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Operator guide](docs/OPERATOR-GUIDE.md)
- [Model catalog contract](MODELS.md)
- [Development guide](DEVELOPMENT.md)
- [Documentation index](docs/README.md)

## License and Security

Starport uses the GNU AGPLv3 license. See [LICENSE](LICENSE).

Report security vulnerabilities as described in [SECURITY.md](SECURITY.md).
