# Starport

Starport is a self-hosted LLM gateway written in Go. It exposes OpenAI and
OpenRouter HTTP contracts over one provider-neutral inference core. It uses
Starmap as the only source of model, provider, capability, context, and price
facts.

Status: pre-release. Starport has no legacy API, provider-ID, or durable-data
compatibility contract.

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
- One configured inference provider.
- two different secrets with at least 32 characters each.

Copy the example configuration:

```bash
cp .env.example .env
```

Set the provider-credential master key, the first gateway key, and one
provider key:

```text
STARPORT_SECURITY_MASTER_KEY=<random master secret>
STARPORT_SECURITY_BOOTSTRAP_API_KEY=<different random gateway key>
STARPORT_PROVIDERS_OPENAI_API_KEY=<provider inference key>
```

Build and start Starport:

```bash
make build
./starport serve
```

If identity storage is empty, Starport requires a bootstrap key. See the
[operator guide](docs/OPERATOR-GUIDE.md) for the first administrator-key
rotation procedure.

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

## Exact Provider IDs

Routing and BYOK APIs accept these adapter IDs:

- `openai`
- `anthropic`
- `google-ai-studio`
- `google-vertex`
- `groq`
- `mistral`
- `azure-openai`
- `ollama`

Starport does not normalize old or alternate provider names. Model IDs use
the exact `provider/model` form present in the active Starmap generation.

## Containers

The Compose file starts Starport with Valkey:

```bash
export STARPORT_SECURITY_MASTER_KEY=<master-secret>
export STARPORT_SECURITY_BOOTSTRAP_API_KEY=<bootstrap-key>
export STARPORT_PROVIDERS_OPENAI_API_KEY=<provider-inference-key>
docker compose up --build
```

The repository does not claim a published image before the first release.

## Development and Verification

```bash
go test ./...
bash scripts/verify-v1-architecture.sh
make lint
make build
bash scripts/smoke-openrouter-sdks.sh
```

The smoke runner always tests the raw HTTP contract. It reports optional
official SDK checks as `UNVERIFIED` when the SDK is not installed.

## Documentation

- [Architecture](docs/ARCHITECTURE.md)
- [Operator guide](docs/OPERATOR-GUIDE.md)
- [Model catalog contract](MODELS.md)
- [Development guide](DEVELOPMENT.md)
- [Documentation index](docs/README.md)

## License and Security

Starport uses the GNU AGPLv3 license. See [LICENSE](LICENSE).

Report security vulnerabilities as described in [SECURITY.md](SECURITY.md).
