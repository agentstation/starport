# Starport

Starport is a self-hosted LLM inference gateway. It serves OpenAI-compatible
APIs at `/v1` and OpenRouter-compatible APIs at `/api/v1`.

Starport uses Starmap as its only source of provider, model, capability,
context, price, and service facts. Starport owns inference credentials,
gateway identities, routing policy, execution, and HTTP protocols.

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
export STARPORT_BIN="$PWD/starport"
"$STARPORT_BIN" --version
```

## Quick start

Set the executable once. The default uses a release installation from
`PATH`. Source builders keep the value from the preceding build commands.

```bash
export STARPORT_BIN="${STARPORT_BIN:-starport}"
```

You need an OpenAI inference key. Initialize one local Starport instance:

```bash
export STARPORT_PROVIDERS_OPENAI_API_KEY="replace-with-provider-inference-key"
"$STARPORT_BIN" init --provider openai
```

Initialization creates a provider-credential master key and one gateway
identity. It writes the OpenAI key, master key, and local state under the
owner-only platform configuration directory. It prints the new gateway API
key once. Save that key, then set it for the client examples:

```bash
export STARPORT_API_KEY="replace-with-gateway-api-key-from-init"
```

Inspect the effective paths and startup state:

```bash
"$STARPORT_BIN" config paths
"$STARPORT_BIN" config validate
"$STARPORT_BIN" doctor --probe
```

Start the gateway in this terminal. Use another terminal for the requests:

```bash
"$STARPORT_BIN" serve
```

Check readiness and the authenticated model catalog:

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

For local Ollama inference, run `starport init --provider ollama`. Add each
installed model to a reviewed Starmap workspace before startup. Then set
`STARPORT_CATALOG_WORKSPACE_PATH` to that workspace.

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

See the [configuration reference](.env.example) and
[operator guide](docs/OPERATOR-GUIDE.md) for production settings.

## Cloud credentials

Vertex AI and Azure OpenAI accept static credentials or renewable default
cloud credentials. Both adapters require an explicit `AUTH_MODE`.

```bash
export STARPORT_PROVIDERS_GOOGLE_VERTEX_AUTH_MODE=default
export STARPORT_PROVIDERS_GOOGLE_VERTEX_PROJECT_ID="replace-with-project-id"
export STARPORT_PROVIDERS_GOOGLE_VERTEX_LOCATION="us-central1"

export STARPORT_PROVIDERS_AZURE_OPENAI_AUTH_MODE=default
export STARPORT_PROVIDERS_AZURE_OPENAI_BASE_URL="https://replace-with-resource.openai.azure.com"
```

Default mode uses Google Application Default Credentials or Azure
`DefaultAzureCredential`. Static mode uses the provider `API_KEY` variable.
Starport rejects a static key in default mode.

Starmap catalog-acquisition credentials remain separate from Starport
inference credentials.

## Containers

Pull a versioned image and verify its GitHub attestation:

```bash
docker pull ghcr.io/agentstation/starport:1.0.0
gh attestation verify oci://ghcr.io/agentstation/starport:1.0.0 \
  --repo agentstation/starport \
  --signer-workflow agentstation/starport/.github/workflows/release.yaml
docker run --rm ghcr.io/agentstation/starport:1.0.0 --version
```

The Compose file builds Starport locally and uses Valkey for shared state:

```bash
export STARPORT_SECURITY_MASTER_KEY="replace-with-random-secret-at-least-32-bytes"
export STARPORT_PROVIDERS_OPENAI_API_KEY="replace-with-provider-inference-key"
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
- OpenAI, Anthropic, Google AI Studio, Vertex AI, Groq, Mistral, Azure OpenAI,
  and Ollama adapters.
- Encrypted provider credentials and renewable cloud credentials.
- Header-only gateway authentication and per-key rate limits.
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
