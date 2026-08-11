# CDP6.1 Atomic runtime generation and connector draining

Status: `done`

Work commit: Starport `08ddf47`

## Fail-before evidence

- `Registry.Start` sealed a mutable connector map. A catalog refresh could not
  add, remove, or replace the configured connector set.
- The refresh loop activated a Starmap catalog generation without rebuilding
  provider configuration, credential source handles, connectors, operations,
  or adapter availability.
- Routing could read the catalog and connector registry at different times.
  Cache identity used a separate catalog read.
- Registry close did not retain an old connector until its active request or
  stream ended.

## Complete runtime generation

Starport now builds one candidate from an exact Starmap catalog state and
deployment settings. The candidate also contains source handles,
credential-free connectors, supported operations, endpoints, and adapter
availability. Starport validates the candidate without publishing it. An
invalid or incomplete candidate closes only its unowned connectors and leaves
the active generation unchanged.

The registry publishes a validated candidate with one atomic pointer swap. A
generation contains source handles but no resolved credential value. Credential
rotation changes the value returned by a source. It does not replace the
generation.

The application refresh transaction now does this work in order:

1. Get an unpublished Starmap state.
2. Resolve the deployment settings against that exact catalog.
3. Build all registrations and connectors.
4. Validate the complete catalog and adapter projection.
5. Publish the routable snapshot and registry generation.

During the two publication operations, an old registry lease detects the
catalog generation mismatch and continues to return its retained old snapshot.
A new registry lease is available only after the registry swap. A request can
therefore observe the complete old generation or the complete new generation.
It cannot combine them.

## Request and stream leases

Chat, streaming chat, embeddings, model discovery, provider discovery, endpoint
discovery, and response-cache identity use one retained runtime lease. The cache
middleware passes its lease through the request context, so routing does not
get a second generation. Catalog-derived discovery cache keys contain the
leased generation ID.

Non-streaming requests release the lease when the operation returns. Streams
release it after terminal read or close. Replacement marks the old generation
as draining. The last old lease closes its connectors exactly once. New
requests use only the new connectors.

## Contract tests

These named tests prove the CDP6.1 contracts:

- `TestRuntimeGenerationRejectsInvalidCandidates` proves that failed candidate
  validation keeps the active snapshot and connector.
- `TestInvalidRuntimeCandidateRetainsCacheIdentity` proves that a rejected
  candidate does not change the generation used for cache identity.
- `TestRuntimeGenerationDrainsConnectors` proves the old snapshot and connector
  stay available until the old lease ends, then close exactly once.
- `TestCredentialRotationDoesNotReplaceRuntimeGeneration` proves source value
  rotation through one unchanged generation.
- `TestAppRefreshPublishesCompleteRuntimeGeneration` proves application refresh
  replaces the full catalog and connector generation.
- `TestAppRefreshFailureRetainsPriorRuntimeGeneration` proves connector build
  failure leaves the prior generation active.
- `TestRouterRetainsRuntimeLeaseThroughRequestAndStream` proves request, stream,
  and borrowed-lease ownership.
- `TestCachedServiceRetainsOneRuntimeGeneration` proves cache lookup, upstream
  work, discovery identity, and stream lifetime use one borrowed lease.

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
go test -race ./internal/catalog ./internal/config ./internal/providers/connectors ./internal/registry ./internal/router ./internal/proxy ./internal/app
```

The final uncapped run completed `internal/registry` in 10.910 seconds,
`internal/proxy` in 10.676 seconds, and `internal/app` in 33.001 seconds. The
other packages used valid cached race results from the same source state. No
race report occurred. No command used `GOFLAGS`, `-p`, a scheduler cap, or a
timeout change.

The campaign verifier reported:

```text
Summary: 14 passed, 5 failed
```

CDP-V18 is green. CDP7, CDP8, and later tasks own CDP-V11, CDP-V15, CDP-V16,
CDP-V17, and CDP-V19.
