# SVA15 Snapshot-only Provider Facts

Date: 2026-08-03

Status: `done`

## Outcome

Starport now gets provider, model, operation, endpoint, prompt-cache, and price
facts from one immutable Starmap-backed routable snapshot. Provider adapters
send inference requests only. They do not discover models, probe health with a model,
cache a provider catalog, or create placeholder models.

An operation becomes routable when the exact Starmap offering and the compiled
adapter descriptor both support it. A route also requires a compatible
offering endpoint. The planner carries that operation, endpoint URL, and wire
protocol into each attempt.

Provider model IDs are opaque. Starport sends the exact
`ProviderModelID` from Starmap without prefixes, suffix removal, family checks,
or publisher inference. Vertex selects Google or Anthropic request encoding
from the offering endpoint protocol. Starmap owns the author-specific Vertex
protocol projection.

## Removed Duplicate Facts

- Removed `Models` and `Health` from the inference connector contract.
- Removed connector model discovery, model-list caches, static health models,
  and placeholder Azure deployments.
- Removed Anthropic model-ID rewriting and Vertex model-name dispatch.
- Removed provider-wide prompt-cache support and sample-price tables.
- Removed dormant BYOK cost calculation that had no v1 consumer.
- Removed model-family sorting and request-time connector discovery.
- Projected provider metadata and model endpoints from the current snapshot.

Google AI Studio embedding inference now follows its declared adapter
capability and uses the exact Starmap offering endpoint. Missing catalog facts,
adapter capability, endpoint, cache support, or price fail closed.

## Starmap Contract Change

`ProviderInferenceEndpoint` lets Starmap select the wire protocol, non-stream
path, and stream path for an author-specific cloud offering. The Vertex
provider record selects Anthropic protocol and raw-predict paths for
Anthropic-authored offerings. It selects Google Cloud protocol and Gemini
content-generation paths for Google-authored offerings.

`BindOfferingEndpoint` applies only runtime base-URL, project, and location
values. It rejects unresolved catalog template variables. Starport does not
infer a provider path or a default location. The Starmap generator rebuilt the
endpoint projection and bootstrap manifest.

## Acceptance Contracts

These named tests pass:

- `TestSnapshotOnlyDiscovery`
- `TestEmbeddingRequiresCatalogAndAdapterCapability`
- `TestExactProviderModelIDIsOpaque`
- `TestOfferingEndpointSelectsProtocol`
- `TestOfferingCacheCapability`
- `TestOfferingPriceHasNoFallback`
- `TestStarmapFactMutationContract`
- `TestEndpointBindingsAndStreamURLComeFromStarmap`
- `TestBindOfferingEndpoint`
- `TestEmbeddedProviderContracts`

The mutation contract changes a definition, operation, protocol, endpoint,
prompt-cache capability, and price between two Starmap generations. Starport
projects each change without a provider or model conditional. A retained old
snapshot remains generation-consistent.

## Verification

- The SVA15 deterministic package gate passes.
- The named SVA15 race gate passes.
- The full Starport Go suite passes.
- The full Starmap Go suite passes.
- The focused Starmap provider-contract race gate passes.
- `go vet ./...` and `git diff --check` pass in both repositories.
- The ownership verifier reports `Summary: 12 passed, 0 failed`.

I did not create a commit, branch, release, publication, or pull request.
