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

- A Starport v1 binary, container image, or Go 1.26.5 source toolchain.
- One configured inference provider.
- Two different secrets with at least 32 characters each.

Install a verified archive from the GitHub release:

```bash
gh release download v1.0.0 \
  --repo agentstation/starport \
  --pattern 'starport_1.0.0_linux_x86_64.tar.gz' \
  --pattern 'checksums.txt'
sha256sum --check --ignore-missing checksums.txt
gh attestation verify starport_1.0.0_linux_x86_64.tar.gz \
  --repo agentstation/starport \
  --signer-workflow agentstation/starport/.github/workflows/release.yaml
tar -xzf starport_1.0.0_linux_x86_64.tar.gz
./starport --version
```

The release also contains Linux and macOS archives and Windows zip files for
amd64 and arm64. You can build the exact tag from source:

```bash
go install github.com/agentstation/starport/cmd/starport@v1.0.0
```

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
export STARPORT_SECURITY_BOOTSTRAP_API_KEY=<bootstrap-key>
export STARPORT_PROVIDERS_OPENAI_API_KEY=<provider-inference-key>
docker compose up --build
```

For a single-node container, mount `/var/lib/starport/data` and pass the
required secrets through an environment file or secret manager.

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
