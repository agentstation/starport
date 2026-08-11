# CDP6 Credential-free connectors and primitive registries

Status: `done`

Work commit: Starport `442f4c0`

## Fail-before evidence

- `AdapterRegistry` used a compiled provider-ID roster to select transport,
  authentication, credential fields, and supported operations.
- `ProviderConfig` stored an API key, an authentication mode, and a credential
  source. A connector could use that one operator credential for every request.
- Azure OpenAI, Groq, and Mistral had provider-specific connector wrappers even
  though they used an existing wire transport.
- Starport rejected a catalog-only `acme` provider because it had no compiled
  provider descriptor.
- BYOK validation used provider-specific dispatch instead of the active Starmap
  credential contract.

## Primitive-owned runtime activation

Starport now has two compiled registries. Starport keys the transport registry
by Starmap endpoint type and operation. Starport keys the authentication
registry by Starmap authentication primitive. A provider ID labels runtime
state. It does not select compiled behavior.

`providers.Activate` selects a configured provider only when the active Starmap
catalog and compiled primitives support it. It requires the exact selected
credential profile from Starmap. It validates the inference base URL, endpoint
bindings, offering operations, endpoint types, and authentication primitive
before it creates a connector.

The provider connector composes all endpoint transports that the provider's
offerings need. Azure OpenAI, Groq, and Mistral now use the OpenAI-compatible
transport without provider wrappers. Google AI Studio, Google Cloud, Anthropic,
OpenAI-compatible, and Ollama behavior stays transport-owned. The audit removed
a Google stream branch that selected response behavior from the provider ID.
Ollama now applies the same catalog-declared authentication contract as every
other transport, so a hosted Ollama-compatible provider can use a supported
authentication primitive.

Unsupported authentication and transport primitives return typed errors that
work with `errors.Is`. Starport does not add a provider compatibility path.

## Request-bound credential material

Connector configuration contains only operational values. It contains no
credential value or source. Each registry registration owns one material
source outside the connector.

Routing resolves material for each chat or stream attempt. The embeddings path
does the same before execution. The selected material travels on the request
value and enters the typed authentication applicator only after route and
provider selection. Concurrent requests can use different material through one
connector without shared credential state.

The catalog-driven BYOK validator now reads exact provider, field, profile, and
pattern facts from the active Starmap snapshot. It contains no provider switch
and accepts only fields that an inference profile declares. CDP7 still owns
request policy, BYOK strategy order, setup, and diagnosis operator surfaces.

## Contract tests

These named tests prove the CDP6 contracts:

- `TestSyntheticCatalogProviderInferenceContract` creates a catalog-only
  `acme` provider that uses the OpenAI endpoint type. It proves chat, SSE
  streaming, embeddings, authentication placement, and the exact opaque model
  IDs `opaque/chat@001` and `opaque/embed@002` without a provider branch.
- `TestStarportProductionHasNoProviderRoster` proves that the provider roster
  files remain deleted and that an `acme` provider can use a compiled transport.
- `TestTransportAuthenticationRegistriesUsePrimitives` proves the production
  endpoint types and authentication primitives.
- `TestConnectorsStoreNoCredentialMaterial` inspects every connector type for
  credential values and sources.
- `TestConcurrentRequestsUseOnlySelectedCredentialMaterial` makes 64 concurrent
  requests through one connector and proves exact request isolation.
- `TestActivationRejectsUnsupportedAuthenticationPrimitive` and
  `TestTransportRegistryRejectsUnsupportedPrimitive` prove typed fail-closed
  behavior.
- `TestRequestAuthenticationAppliesCatalogPlacements` proves typed header and
  HTTPS query placement. The related tests prove HTTP query rejection and
  typed Google quota-project application.
- The Ollama chat test proves that the Ollama transport applies a catalog
  authentication primitive. Other Ollama tests use an explicit `none` profile.
- `TestValidateKeyUsesCatalogCredentialContracts` proves catalog-driven BYOK
  validation without network access or provider membership code.

## Verification

These checks passed after the final source change:

- `make format-check`.
- `go test ./...` across all Starport packages.
- `go vet ./...`.
- `make lint` with zero issues.
- `make build`.
- `bash scripts/verify-starmap-ownership.sh`: 12 passed, 0 failed.
- `bash scripts/verify-v1-architecture.sh`: 12 passed, 0 failed.
- `bash scripts/smoke-first-run.sh`.
- `bash scripts/smoke-openrouter-sdks.sh`, including raw HTTP and the Python,
  TypeScript, and Go OpenRouter SDKs.
- `git diff --check`.

This focused race command passed:

```text
go test -race ./internal/providerauth ./internal/providers ./internal/providers/connectors ./internal/registry ./internal/router ./internal/proxy ./internal/app ./internal/diagnosis ./internal/providers/byok
```

The final uncapped run completed `internal/providers` in 8.641 seconds,
`internal/providers/connectors` in 3.621 seconds, `internal/app` in 33.228
seconds, and `internal/diagnosis` in 18.566 seconds. The other packages used
valid cached race results from the same source state. No race report occurred.
No command used `GOFLAGS`, `-p`, a scheduler cap, or a timeout change.

The campaign verifier reported:

```text
Summary: 13 passed, 6 failed
```

CDP-V09, CDP-V12, CDP-V13, and CDP-V14 are green. CDP6.1 and later tasks own
CDP-V11 and CDP-V15 through CDP-V19.
