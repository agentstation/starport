# SVA14 Catalog-backed Provider Activation

Date: 2026-08-03

Status: `done`

## Outcome

Starport now derives an active inference provider from three contracts. It
requires a Starmap provider with a compatible offering, one compiled Starport
adapter, and valid operator inference configuration. Startup fails closed when
one contract is absent.

One typed adapter registry owns compiled provider dispatch, operation support,
configuration validation, endpoint projection, inference credential fields,
credential validation, request authentication, and optional credential probes.
BYOK delegates to this registry. It no longer has a provider switch or accepts
AWS Bedrock without an adapter.

Starmap acquisition and Starport inference authentication are separate. The
Starport loader no longer copies provider acquisition variables, such as
`OPENAI_API_KEY`, into inference configuration. Inference keys use only the
`STARPORT_PROVIDERS_*` namespace. Google AI Studio puts its inference key in
`x-goog-api-key`. No inference key enters a URL.

## Fail-before evidence

The contract tests first failed because Starport had no adapter registry or
catalog-backed activation API. Mistral, Azure OpenAI, and Ollama adapter code
could exist without a Starmap route. BYOK had a separate provider switch,
Bedrock validation had no adapter, and Google AI Studio put its key in a query
parameter.

Starport also constructed only a read-only Starmap client. The registry called
connector model APIs and maintained a second model observation state. No
Starport composition path could publish a Starmap acquisition result or a
tenant offering generation.

## Implemented seams

- `internal/providers/connectors` contains one immutable adapter registry keyed
  by `catalogs.ProviderID`.
- Adapter activation requires a Starmap provider and at least one non-retired,
  compatible offering. Azure and Ollama therefore require reviewed tenant
  offerings before startup can activate their adapters.
- Stable service URLs and operation endpoints come from Starmap. Starport keeps
  explicit operator overrides and inference secrets.
- Vertex inference credentials are OAuth access tokens. Workload identity and
  cloud credential chains remain Starmap acquisition concerns.
- Azure deployment IDs are catalog facts, not credential fields. Azure BYOK
  validates only its inference API key.
- `internal/catalog` adapts the configured Starport KV store to Starmap's
  immutable generation store. It owns explicit acquisition, publication,
  refresh, and tenant observation activation.
- Starmap now accepts validated non-network observations through
  `acquisition.Syncer.PublishObservations`. It reconciles them with the same
  authority rules and durable publication transaction as other acquisition
  paths.
- App composition can refresh Starmap before adapter activation and at a
  configured interval. It waits for the refresh loop before storage shutdown.
- Registry startup no longer calls `Connector.Models`, stores provider
  configuration, or publishes connector-owned model observations.

## Acceptance contracts

These tests pass:

- `TestActiveProviderIntersection`
- `TestConfiguredProviderMissingCatalogFailsStartup`
- `TestConfiguredProviderWithoutOfferingFailsStartup`
- `TestAdapterRegistryDrivesInferenceCredentialValidation`
- `TestGoogleAPIKeyUsesInferenceHeader`
- `TestAuthPlanesAreIsolated`
- `TestStarmapAcquisitionPublishesRefresh`
- `TestTenantOfferingsEnterCatalogGeneration`
- `TestPublishObservationsCommitsTenantOfferingGeneration`

The tenant contract publishes an Ollama model mapping and activates its exact
offering. It reopens the catalog runtime from the same KV store and reads the
same generation. The same observation path supports reviewed Azure deployment
mappings without embedding installation-specific records in Starmap.

## Verification

- The SVA14 deterministic package gate passes.
- The app, catalog, BYOK, and connector packages pass with the race detector.
- The full Starport Go suite passes.
- Starmap's acquisition package and focused acquisition race gate pass.
- `go vet ./...` and `git diff --check` pass in both repositories.
- Ownership conditions O01 through O04 and O11 pass. O05 through O10 and O12
  remain assigned to SVA15 and SVA16.

Starport uses a temporary local module replacement for the changed Starmap
contract. A tagged Starmap release and final module pin require owner approval
and remain part of the SVA16 release gate.

I did not create a commit, branch, release, or publication.
